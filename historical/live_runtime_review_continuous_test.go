package historical

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeContinuousPendingReorg(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 3)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var gated atomic.Bool
			s.gate = func(ctx context.Context, id uint64, method string, params []any) error {
				if gated.Load() && id == 1 && method == "eth_getBlockByNumber" && params[0] == "latest" {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-release:
					}
				}
				return nil
			}
			b := runtimeBuild(t, s.p, 6, 1, indexing.Handler(finiteIncrement))
			for _, c := range b.Executable().Chains {
				c["reorgWindow"] = uint64(600)
			}
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			var r *RuntimeRunner
			phase := 0
			var publicB string
			reorgs := 0
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-pending-highwater-v1", OpenID: "pending-highwater-1", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
				c, err := r.Control(ctx)
				if err != nil {
					return err
				}
				switch e.Type {
				case "block":
					if phase == 0 {
						both := true
						for _, h := range c.Cursors {
							cp, _ := checkpoint.Decode(h.Checkpoint)
							both = both && cp.BlockNumber >= 3
						}
						if both {
							phase = 1
							s.fork(1, 3)
						}
					}
					if phase == 2 && e.ChainID == 2 {
						cp, _ := checkpoint.Decode(e.Head.Checkpoint)
						if cp.BlockNumber == 5 {
							if len(c.ReplayTargets) == 0 || c.Serving {
								return fmt.Errorf("first replay unexpectedly completed")
							}
							pub, err := r.PublicCheckpoints(ctx)
							if err != nil {
								return err
							}
							publicB = pub[2]
							at, _ := checkpoint.Decode(publicB)
							if at.BlockNumber != 5 {
								return fmt.Errorf("chain B did not publish actual new height5")
							}
							phase = 3
							s.fork(1, 2)
							once.Do(func() { close(release) })
						}
					}
					if phase >= 4 && e.ChainID == 2 {
						cp, _ := checkpoint.Decode(e.Head.Checkpoint)
						if cp.BlockNumber < 5 {
							pub, err := r.PublicCheckpoints(ctx)
							if err != nil {
								return err
							}
							if pub[2] != publicB {
								return fmt.Errorf("public B5 regressed during private replay at%d: %v", cp.BlockNumber, pub)
							}
						}
					}
				case "reorg":
					reorgs++
					if reorgs == 1 {
						phase = 2
						gated.Store(true)
						s.mu.Lock()
						s.latest[2] = 5
						s.mu.Unlock()
					} else if reorgs == 2 {
						target, err := checkpoint.Decode(c.ReplayTargets[2])
						if err != nil || target.BlockNumber != 5 {
							return fmt.Errorf("second reorg dropped public highwater: target=%+v want B5 control=%+v err=%v", target, c, err)
						}
						pub, err := r.PublicCheckpoints(ctx)
						if err != nil || pub[2] != publicB {
							return fmt.Errorf("second reorg moved published B5: %v %v", pub, err)
						}
						phase = 4
					}
				case "ready":
					if c.Reorgs == 2 {
						if phase != 4 {
							return fmt.Errorf("ready before second replay phase=%d", phase)
						}
						b, _ := checkpoint.Decode(c.Cursors[2].Checkpoint)
						if b.BlockNumber < 5 {
							return fmt.Errorf("ready below published B5")
						}
						phase = 5
						s.setLatest(6)
					}
				}
				return nil
			}}}
			r = runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || reorgs != 2 || phase != 5 {
				t.Fatalf("pending reorg result=%+v reorgs=%d phase=%d err=%v", out, reorgs, phase, err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "6" || fmt.Sprint(rows[1]["balance"]) != "6" {
					t.Fatalf("second reorg replay lost/duplicated rows: %v", rows)
				}
			})
		})
	}
}

func TestRuntimeContinuousRetainedFinality(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 1)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var active atomic.Bool
			var faultArmed, faultFired atomic.Bool
			retries := 0
			s.gate = func(ctx context.Context, id uint64, method string, params []any) error {
				if faultArmed.Load() && id == 2 && method == "eth_getBlockByHash" && params[0] == finiteBlock(2, 6)["hash"] && faultFired.CompareAndSwap(false, true) {
					return fmt.Errorf("controlled retained-finality proof acquisition failure")
				}
				if active.Load() && id == 2 && method == "eth_getBlockByNumber" && params[0] == "latest" {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-release:
					}
				}
				return nil
			}
			b := runtimeBuild(t, s.p, 7, 1, indexing.Handler(finiteIncrement))
			b.Executable().Chains[0]["reorgWindow"] = uint64(600)
			b.Executable().Chains[1]["reorgWindow"] = uint64(1)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			var r *RuntimeRunner
			forked, rewound, reactivated := false, false, false
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-retained-finality-v1", OpenID: "retained-finality-1", Requesters: runtimeFactory(s.p, &epochs), OnActivated: func(context.Context) error { active.Store(true); return nil }, Live: &RuntimeLiveOptions{HeaderCacheCapacity: 2, PollingInterval: time.Millisecond, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
				if e.Type == "fetch-retry" {
					retries++
					if ready, err := r.Ready(ctx); err != nil || ready {
						return fmt.Errorf("proof retry exposed ready: %v %v", ready, err)
					}
				}
				c, err := r.Control(ctx)
				if err != nil {
					return err
				}
				if e.Type == "block" && e.ChainID == 1 {
					once.Do(func() { s.mu.Lock(); s.latest[2] = 7; s.mu.Unlock(); close(release) })
				}
				if e.Type == "finalize" && e.ChainID == 2 && !forked {
					cp, _ := checkpoint.Decode(c.Finality[2].Checkpoint)
					if cp.BlockNumber == 6 {
						if c.SafeOrdinal != 0 {
							return fmt.Errorf("A1 did not block global safe prefix")
						}
						forked = true
						s.fork(1, 1)
					}
				}
				if e.Type == "reorg" {
					at, _ := checkpoint.Decode(c.Cursors[2].Checkpoint)
					final, _ := checkpoint.Decode(c.Finality[2].Checkpoint)
					if at.BlockNumber >= final.BlockNumber || final.BlockNumber != 6 {
						return fmt.Errorf("counterexample did not rewind private B below public B6: %+v", c)
					}
					rewound = true
					faultArmed.Store(true)
				}
				if e.Type == "ready" && c.Reorgs == 1 {
					reactivated = true
					s.setLatest(7)
				}
				return nil
			}}}
			r = runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || !forked || !rewound || !reactivated || !faultFired.Load() || retries != 1 {
				t.Fatalf("retained finality replay result=%+v fork=%v rewind=%v ready=%v fault=%v retries=%d err=%v", out, forked, rewound, reactivated, faultFired.Load(), retries, err)
			}
			c, err := r.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			at, _ := checkpoint.Decode(c.Finality[2].Checkpoint)
			if at.BlockNumber != 6 || c.Reorgs != 1 {
				t.Fatalf("retained finality changed: %+v", c)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "7" || fmt.Sprint(rows[1]["balance"]) != "7" {
					t.Fatalf("retained finality replay duplicated writes: %v", rows)
				}
			})
		})
	}
}
