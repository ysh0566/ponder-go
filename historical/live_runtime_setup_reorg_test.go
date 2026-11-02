package historical

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setupReorgBuild(t *testing.T, p *finiteRPC, end, interval uint64, cache bool, h, setup indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":600,"cacheRpcRequests":%t},"two":{"id":2,"rpc":%q,"reorgWindow":600,"cacheRpcRequests":%t}},"contracts":{"Setup":{"abi":[],"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":%d}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":%d,"interval":%d}}}`, p.server.URL+"/1", cache, p.server.URL+"/2", cache, end, end, interval)
	cfg, err := config.ParseSourceConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range base.RPCClients() {
		r.RequestsPerSecond = 0
		r.Retries = 0
	}
	b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: h}, {Name: "Setup:setup", Fn: setup}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestRuntimeSetupReorg(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"shared_rows", "empty_segments"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				continuousRoot(t)
				s := newContinuousRPC(t, 3)
				trace := []continuousSegment{}
				setupCalls := 0
				setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					setupCalls++
					id := finiteChain(a)
					row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "setup"})
					if err != nil {
						return err
					}
					v := big.NewInt(0)
					if row != nil {
						v.SetString(fmt.Sprint(row["balance"]), 10)
					}
					v.Mul(v, big.NewInt(10))
					v.Add(v, new(big.Int).SetUint64(id))
					if row == nil {
						_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "setup", "balance": v}, store.Conflict{})
					} else {
						_, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "setup"}, store.Row{"balance": v})
					}
					return err
				})
				var pending uint64
				var r *RuntimeRunner
				step := 0
				pauses, reorgs, ready := 0, 0, 0
				var saved map[uint64]string
				handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					id := finiteChain(a)
					setupRow, se := a.Context.DB.Find(ctx, "account", store.Row{"id": "setup"})
					if se != nil || setupRow == nil {
						return fmt.Errorf("setup base missing: %v %v", setupRow, se)
					}
					wantSetup := int64(12)
					for _, seg := range trace {
						if seg.code != 0 {
							wantSetup++
						}
					}
					if fmt.Sprint(setupRow["balance"]) != fmt.Sprint(wantSetup) || setupCalls != 2 {
						return fmt.Errorf("setup reorg base/once contract row=%v want=%d calls=%d", setupRow, wantSetup, setupCalls)
					}
					if _, se = a.Context.DB.Update(ctx, "account", store.Row{"id": "setup"}, store.Row{"balance": big.NewInt(wantSetup + 1)}); se != nil {
						return se
					}
					n, e := continuousNumber(a)
					if e != nil {
						return e
					}
					row, e := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
					if e != nil {
						return e
					}
					v := big.NewInt(0)
					if row != nil {
						var ok bool
						v, ok = new(big.Int).SetString(fmt.Sprint(row["balance"]), 10)
						if !ok {
							return fmt.Errorf("bad bigint")
						}
					}
					if v.Cmp(continuousFold(trace)) != 0 {
						return fmt.Errorf("global undo/replay differs before %d:%d: row=%v trace=%v", id, n, row, trace)
					}
					balance, e := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
					if e != nil {
						return e
					}
					s.mu.Lock()
					want := int64(42)
					if s.forked[fmt.Sprint(s.canonical[id][n]["hash"])] {
						want = 99
					}
					s.mu.Unlock()
					if balance.Int64() != want {
						return fmt.Errorf("orphaned numeric RPC cache survived reorg: chain%d:%d balance=%s want=%d", id, n, balance, want)
					}
					pending = id*100 + n*10 + 1
					if want == 99 {
						pending++
					}
					v.Mul(v, big.NewInt(1000))
					v.Add(v, new(big.Int).SetUint64(pending))
					if row == nil {
						_, e = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "balance": v}, store.Conflict{})
					} else {
						_, e = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"balance": v})
					}
					return e
				})
				interval := uint64(1)
				if kind == "empty_segments" {
					interval = 2
				}
				b := setupReorgBuild(t, s.p, 6, interval, kind == "shared_rows", handler, setup)
				for _, c := range b.Executable().Chains {
					c["reorgWindow"] = uint64(600)
				}
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "setup-global-" + kind, OpenID: "continuous-global-1", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
					switch e.Type {
					case "block":
						cp, _ := checkpoint.Decode(e.Head.Checkpoint)
						trace = append(trace, continuousSegment{id: e.ChainID, n: cp.BlockNumber, code: pending})
						pending = 0
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						both := true
						for _, h := range c.Cursors {
							n, _ := checkpoint.Decode(h.Checkpoint)
							both = both && n.BlockNumber >= 3
						}
						if both && step == 0 {
							saved, err = r.PublicCheckpoints(ctx)
							if err != nil {
								return err
							}
							step = 1
							s.fork(1, 3)
						}
					case "paused":
						pauses++
						if yes, err := r.Ready(ctx); err != nil || yes {
							return fmt.Errorf("reorg exposed ready: %v %v", yes, err)
						}
						// Other-chain commits before this pause are legitimate. Bind
						// the exact public snapshot immediately before coordination.
						var err error
						saved, err = r.PublicCheckpoints(ctx)
						if err != nil {
							return err
						}
					case "reorg":
						reorgs++
						a, _ := checkpoint.Decode(e.Head.Checkpoint)
						cut := len(trace)
						for i, x := range trace {
							if x.id == e.ChainID && x.n > a.BlockNumber {
								cut = i
								break
							}
						}
						trace = trace[:cut]
						public, err := r.PublicCheckpoints(ctx)
						if err != nil {
							return err
						}
						if !reflect.DeepEqual(public, saved) {
							return fmt.Errorf("public heads moved before replay commit: %v saved=%v", public, saved)
						}
						c, err := r.Control(ctx)
						if err != nil || len(c.ReplayTargets) == 0 || c.Serving {
							return fmt.Errorf("missing private replay guard %+v %v", c, err)
						}
					case "ready":
						ready++
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						if c.Reorgs == 1 && step == 1 {
							saved, err = r.PublicCheckpoints(ctx)
							if err != nil {
								return err
							}
							step = 2
							s.fork(1, 2)
							s.setLatest(5)
						} else if c.Reorgs == 2 && step == 2 {
							step = 3
							s.setLatest(6)
						}
					}
					return nil
				}}}
				r = runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || reorgs != 2 || pauses != 2 || ready != 3 || step != 3 {
					t.Fatalf("actual reorg result=%+v reorg=%d paused=%d ready=%d step=%d trace=%v err=%v", out, reorgs, pauses, ready, step, trace, err)
				}
				c, err := r.Control(context.Background())
				if err != nil || c.Reorgs != 2 || len(c.ReplayTargets) != 0 || !c.Serving || c.Version != "managed-live-setup-v8" || c.Bootstraps != 1 || setupCalls != 2 {
					t.Fatalf("final reorg control=%+v err=%v", c, err)
				}
				for _, h := range c.Cursors {
					cp, _ := checkpoint.Decode(h.Checkpoint)
					if cp.BlockNumber != 6 {
						t.Fatal(c)
					}
				}
				if len(trace) != 12 {
					t.Fatalf("empty/executed segments lost after global replay: %v", trace)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
					if len(rows) != 1 || fmt.Sprint(rows[0]["balance"]) != continuousFold(trace).String() {
						t.Fatalf("actual shared row=%v trace=%v", rows, trace)
					}
					wantSetup := int64(12)
					for _, seg := range trace {
						if seg.code != 0 {
							wantSetup++
						}
					}
					setupRows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='setup'")
					if len(setupRows) != 1 || fmt.Sprint(setupRows[0]["balance"]) != fmt.Sprint(wantSetup) {
						t.Fatalf("setup base after global undo=%v want=%d", setupRows, wantSetup)
					}
					segments := managedRows(t, db, "SELECT ordinal FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".\"_ponder_go_live_segments\"")
					if len(segments) != 12 {
						t.Fatalf("empty segments missing: %d", len(segments))
					}
				})
			})
		}
	}
}
