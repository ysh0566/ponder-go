package historical

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"testing"
	"time"
)

func TestRuntimeSetupFailures(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, mode := range []string{"handler_second", "rpc_error", "cancel_second", "after_setup_commit"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				continuousRoot(t)
				p := newFiniteRPC(t, 2)
				inject := true
				calls := 0
				observed := 0
				var cancel context.CancelFunc
				var r *RuntimeRunner
				p.setHook(func(_ context.Context, q finiteRPCRequest) error {
					if inject && mode == "rpc_error" && q.Chain == 2 && q.Method == "eth_getBalance" {
						return fmt.Errorf("injected setup RPC failure")
					}
					return nil
				})
				setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					calls++
					id := finiteChain(a)
					if _, err := a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("init-%d", id), "balance": big.NewInt(99)}, store.Conflict{}); err != nil {
						return err
					}
					if mode == "rpc_error" {
						if _, err := a.Context.Client.GetBalance(ctx, "0x1111111111111111111111111111111111111111"); err != nil {
							return err
						}
					}
					if inject && id == 2 {
						if mode == "handler_second" {
							return fmt.Errorf("injected second setup handler failure")
						}
						if mode == "cancel_second" {
							cancel()
							return ctx.Err()
						}
					}
					return nil
				})
				b := finiteBuild(t, p, 2, true, indexing.Handler(finiteIncrement), setup)
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "setup-failures-" + mode, OpenID: "setup-failure-first", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				if mode == "after_setup_commit" {
					o.OnSetupCommitted = func(ctx context.Context) error {
						observed++
						if err := r.Close(ctx); !errors.Is(err, store.ErrManagedLiveBusy) {
							return fmt.Errorf("setup observer Close gate=%v", err)
						}
						c, err := r.Control(ctx)
						if err != nil || c.Batches != 1 || c.Bootstraps != 1 {
							return fmt.Errorf("observer before authenticated setup commit: %+v %v", c, err)
						}
						if inject {
							return fmt.Errorf("injected after setup commit")
						}
						return nil
					}
				}
				r = runtimeOpen(t, backend, o)
				ctx, stop := context.WithCancel(context.Background())
				cancel = stop
				out, err := r.Run(ctx)
				stop()
				if err == nil || out.Completed || out.Ready {
					t.Fatalf("setup fault accepted=%+v err=%v", out, err)
				}
				c, err := r.Control(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				committed := uint64(0)
				if mode == "after_setup_commit" {
					committed = 1
				}
				if c.Batches != committed || c.Bootstraps != committed {
					t.Fatalf("setup partial control=%+v", c)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
					rows := managedRows(t, db, "SELECT id FROM "+ns+".account ORDER BY id")
					if len(rows) != int(committed)*2 {
						t.Fatalf("all-chain setup rollback/commit rows=%v", rows)
					}
					states := managedRows(t, db, "SELECT chain_id FROM "+ns+"._ponder_go_progress")
					if len(states) != int(committed)*2 {
						t.Fatalf("setup/checkpoints not atomic %v", states)
					}
					journal := managedRows(t, db, "SELECT operation_id FROM "+ns+"._ponder_go_journal")
					if len(journal) != 0 {
						t.Fatalf("setup initial journal leaked %v", journal)
					}
				})
				inject = false
				o.OpenID = "setup-failure-reopen"
				r = runtimeOpen(t, backend, o)
				out, err = r.Run(context.Background())
				if err != nil || !out.Ready || !out.Completed {
					t.Fatalf("setup retry/recovery=%+v err=%v", out, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				want := 4
				if committed == 1 {
					want = 2
				}
				if calls != want {
					t.Fatalf("setup invocation count=%d want=%d", calls, want)
				}
				if mode == "after_setup_commit" && observed != 1 {
					t.Fatalf("post-commit setup repeated: %d", observed)
				}
				managedInspect(t, f, func(db store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
					rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+ns+".account ORDER BY id")
					if len(rows) != 4 || fmt.Sprint(rows[0]["balance"]) != "2" || fmt.Sprint(rows[1]["balance"]) != "2" || fmt.Sprint(rows[2]["balance"]) != "99" || fmt.Sprint(rows[3]["balance"]) != "99" {
						t.Fatalf("setup retry duplicate/lost rows %v", rows)
					}
				})
			})
		}
	}
}
