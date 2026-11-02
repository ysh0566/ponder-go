package historical

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"sync"
	"testing"
	"time"
)

// Supplemental real RuntimeRunner/native coverage, no source-mapping credit.
func TestRuntimeSetupHistory(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, mode := range []string{"cached", "uncached"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				continuousRoot(t)
				s := newContinuousRPC(t, 3)
				order := []uint64{}
				var mu sync.Mutex
				rpcCalls := []string{}
				s.gate = func(_ context.Context, id uint64, method string, args []any) error {
					if method == "eth_getBalance" {
						mu.Lock()
						rpcCalls = append(rpcCalls, fmt.Sprintf("%d:%v", id, args[1]))
						mu.Unlock()
						if args[1] != "0x1" {
							return fmt.Errorf("setup RPC wrong block %v", args)
						}
					}
					return nil
				}
				setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					if a.Event != nil {
						return fmt.Errorf("setup unexpectedly has event body")
					}
					id := finiteChain(a)
					order = append(order, id)
					if _, ok := a.Context.Contracts["Setup"]; !ok {
						return fmt.Errorf("setup contracts absent")
					}
					v, err := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
					if err != nil || v.Cmp(big.NewInt(42)) != 0 {
						return fmt.Errorf("setup RPC=%v err=%v", v, err)
					}
					row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "setup"})
					if err != nil {
						return err
					}
					n := big.NewInt(0)
					if row != nil {
						if _, ok := n.SetString(fmt.Sprint(row["balance"]), 10); !ok {
							return fmt.Errorf("bad setup row")
						}
					}
					n.Mul(n, big.NewInt(10))
					n.Add(n, new(big.Int).SetUint64(id))
					if row == nil {
						_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "setup", "balance": n}, store.Conflict{})
					} else {
						_, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "setup"}, store.Row{"balance": n})
					}
					return err
				})
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					if len(order) != 2 || order[0] != 1 || order[1] != 2 {
						return fmt.Errorf("history/live before complete setup: %v", order)
					}
					row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "setup"})
					if err != nil || row == nil {
						return fmt.Errorf("setup effects absent row=%v err=%v", row, err)
					}
					n, ok := new(big.Int).SetString(fmt.Sprint(row["balance"]), 10)
					if !ok || n.Cmp(big.NewInt(12)) < 0 {
						return fmt.Errorf("setup state lost %v", row)
					}
					n.Add(n, big.NewInt(1))
					if _, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "setup"}, store.Row{"balance": n}); err != nil {
						return err
					}
					return finiteIncrement(ctx, a)
				})
				b := finiteBuild(t, s.p, 3, mode == "cached", h, setup)
				for _, c := range b.Executable().Chains {
					c["reorgWindow"] = uint64(1)
				}
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "setup-history", OpenID: "setup-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				r := runtimeOpen(t, backend, o)
				if ready, err := r.Ready(context.Background()); err != nil || ready {
					t.Fatalf("ready before setup: %v %v", ready, err)
				}
				out, err := r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || out.HistoricalBlocks != 4 || out.LiveBlocks != 2 {
					t.Fatalf("setup run=%+v err=%v", out, err)
				}
				c, err := r.Control(context.Background())
				if err != nil || c.Version != "managed-live-setup-v8" || c.Bootstraps != 1 {
					t.Fatalf("setup control=%+v %v", c, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				o.OpenID = "setup-second"
				r = runtimeOpen(t, backend, o)
				if ready, err := r.Ready(context.Background()); err != nil || ready {
					t.Fatalf("recovery exposed ready: %v %v", ready, err)
				}
				out, err = r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("setup replay=%+v err=%v", out, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				if fmt.Sprint(order) != "[1 2]" {
					t.Fatalf("setup repeated/order changed: %v", order)
				}
				mu.Lock()
				calls := append([]string(nil), rpcCalls...)
				mu.Unlock()
				if fmt.Sprint(calls) != "[1:0x1 2:0x1]" {
					t.Fatalf("setup RPC repeats/block mismatch %v", calls)
				}
				managedInspect(t, f, func(db store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
					rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+ns+".account ORDER BY id")
					if len(rows) != 3 || fmt.Sprint(rows[0]["balance"]) != "3" || fmt.Sprint(rows[1]["balance"]) != "3" || fmt.Sprint(rows[2]["balance"]) != "18" {
						t.Fatalf("setup/global-replay real rows %v", rows)
					}
				})
			})
		}
	}
}
