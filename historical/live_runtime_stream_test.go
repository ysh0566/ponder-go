package historical

import (
	"context"
	"errors"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeContinuousCompletion(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 1)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var active atomic.Bool
			order := []uint64{}
			s.gate = func(ctx context.Context, id uint64, method string, params []any) error {
				if active.Load() && id == 1 && method == "eth_getBlockByNumber" && params[0] == "latest" {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-release:
					}
				}
				return nil
			}
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				order = append(order, finiteChain(a))
				return finiteIncrement(ctx, a)
			})
			b := runtimeBuild(t, s.p, 3, 1, h)
			for _, c := range b.Executable().Chains {
				c["reorgWindow"] = uint64(600)
			}
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-completion-v1", OpenID: "completion-1", Requesters: runtimeFactory(s.p, &epochs), OnActivated: func(context.Context) error { active.Store(true); return nil }, Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(_ context.Context, e RuntimeLiveProgress) error {
				if e.Type == "block" && e.ChainID == 2 {
					once.Do(func() { s.setLatest(3); close(release) })
				}
				return nil
			}}}
			r := runtimeOpen(t, backend, o)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			out, err := r.Run(ctx)
			if err != nil || !out.Ready || !out.Completed || len(order) != 6 || order[0] != 2 {
				t.Fatalf("continuous completion blocked on slower chain: %+v order=%v err=%v", out, order, err)
			}
		})
	}
}

func continuousUnboundedBuild(t *testing.T, p *finiteRPC, h indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":600,"cacheRpcRequests":true},"two":{"id":2,"rpc":%q,"reorgWindow":600,"cacheRpcRequests":true}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"interval":1}}}`, p.server.URL+"/1", p.server.URL+"/2")
	cfg, err := config.ParseSourceConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range base.RPCClients() {
		r.Retries = 0
		r.RequestsPerSecond = 0
	}
	b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: h}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestRuntimeContinuousInfiniteRetry(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 2)
			var escaped *indexing.Client
			var escapedContext context.Context
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				escaped = a.Context.Client
				escapedContext = context.WithoutCancel(ctx)
				return finiteIncrement(ctx, a)
			})
			b := continuousUnboundedBuild(t, s.p, h)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			var r *RuntimeRunner
			retries := 0
			failed := false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var waitChecked atomic.Bool
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-infinite-v1", OpenID: "infinite-1", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{HeaderCacheCapacity: 1, PollingInterval: time.Millisecond, RetryInterval: time.Millisecond, Wait: func(call context.Context, d time.Duration) error {
				if r.transitioning.Load() && !r.domains.RuntimeCallbackContext(call) && waitChecked.CompareAndSwap(false, true) {
					bounded, cancel := context.WithTimeout(call, 200*time.Millisecond)
					err := r.Close(bounded)
					cancel()
					if !errors.Is(err, store.ErrManagedLiveBusy) {
						return fmt.Errorf("Wait callback Close did not reject self-wait: %v", err)
					}
				}
				timer := time.NewTimer(d)
				defer timer.Stop()
				select {
				case <-call.Done():
					return call.Err()
				case <-timer.C:
					return nil
				}
			}, OnProgress: func(call context.Context, e RuntimeLiveProgress) error {
				if e.Type == "fetch-retry" {
					retries++
					if ready, err := r.Ready(call); err != nil || ready {
						return fmt.Errorf("fetch failure exposed ready: %v %v", ready, err)
					}
				}
				if e.Type == "block" && !failed {
					c, err := r.Control(call)
					if err != nil {
						return err
					}
					both := true
					for _, h := range c.Cursors {
						cp, _ := checkpoint.Decode(h.Checkpoint)
						both = both && cp.BlockNumber >= 2
					}
					if both {
						failed = true
						s.mu.Lock()
						s.failLatest[1] = 1
						s.failByHash = 1
						s.latest[1], s.latest[2] = 3, 3
						s.mu.Unlock()
					}
				}
				if e.Type == "ready" && retries > 0 {
					c, err := r.Control(call)
					if err != nil {
						return err
					}
					both := true
					for _, h := range c.Cursors {
						cp, _ := checkpoint.Decode(h.Checkpoint)
						both = both && cp.BlockNumber >= 3
					}
					if both {
						cancel()
						return ctx.Err()
					}
				}
				return nil
			}}}
			r = runtimeOpen(t, backend, o)
			out, err := r.Run(ctx)
			if !errors.Is(err, context.Canceled) || out.Ready || out.Completed || retries != 2 || !waitChecked.Load() {
				t.Fatalf("infinite retry/cancel %+v retries=%d waitGuard=%v err=%v", out, retries, waitChecked.Load(), err)
			}
			if _, err = r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "already attempted") {
				t.Fatalf("duplicate Run accepted: %v", err)
			}
			if _, err = escaped.GetBalance(escapedContext, "0x"+strings.Repeat("1", 40)); err == nil {
				t.Fatal("old epoch client remained usable")
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			s.setLatest(4)
			ctx2, cancel2 := context.WithCancel(context.Background())
			defer cancel2()
			o.OpenID = "infinite-2"
			o.Live = &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(_ context.Context, e RuntimeLiveProgress) error {
				if e.Type == "ready" {
					cancel2()
					return ctx2.Err()
				}
				return nil
			}}
			r2 := runtimeOpen(t, backend, o)
			out, err = r2.Run(ctx2)
			if !errors.Is(err, context.Canceled) || !out.Recovered || out.Ready || out.Completed {
				t.Fatalf("infinite reopen %+v err=%v", out, err)
			}
			if err = r2.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "4" || fmt.Sprint(rows[1]["balance"]) != "4" {
					t.Fatalf("infinite replay duplicated writes: %v", rows)
				}
			})
			if len(epochs) < 7 {
				t.Fatalf("fresh epoch factories missing: %v", epochs)
			}
		})
	}
}

func TestRuntimeContinuousFinalityGuard(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 2)
			b := runtimeBuild(t, s.p, 6, 1, indexing.Handler(finiteIncrement))
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			var r *RuntimeRunner
			forked := false
			finalizations := 0
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-finality-v1", OpenID: "finality-1", Requesters: runtimeFactory(s.p, &epochs), OnActivated: func(context.Context) error { s.setLatest(4); return nil }, Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
				if e.Type != "finalize" {
					return nil
				}
				finalizations++
				c, err := r.Control(ctx)
				if err != nil {
					return err
				}
				both := len(c.Finality) == 2
				for _, head := range c.Finality {
					cp, _ := checkpoint.Decode(head.Checkpoint)
					both = both && cp.BlockNumber >= 3
				}
				if both && !forked {
					forked = true
					s.fork(1, 3)
				}
				return nil
			}}}
			r = runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if !errors.Is(err, store.ErrManagedLiveDeepReorg) || out.Ready || out.Completed || !forked || finalizations < 2 {
				t.Fatalf("finality crossing accepted %+v fork=%v finalizations=%d err=%v", out, forked, finalizations, err)
			}
			c, err := r.Control(context.Background())
			if err != nil || c.Reorgs != 0 || c.Serving {
				t.Fatalf("finalized branch was rewound: %+v %v", c, err)
			}
			for _, head := range c.Finality {
				cp, _ := checkpoint.Decode(head.Checkpoint)
				if cp.BlockNumber != 3 {
					t.Fatalf("finality differs %+v", c)
				}
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "4" || fmt.Sprint(rows[1]["balance"]) != "4" {
					t.Fatalf("finalized fork mutated business data: %v", rows)
				}
			})
			syncFixture := f
			syncFixture.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
			managedInspect(t, syncFixture, func(db store.Backend) {
				migrator, err := store.NewSyncMigrator(db, f.options.Owned.Storage.SyncNamespace)
				if err != nil {
					t.Fatal(err)
				}
				rows := managedRows(t, db, "SELECT count(*)::text AS n FROM "+schema.Quote(migrator.LatestSchema())+".blocks WHERE chain_id IN (1,2) AND number<=3")
				if len(rows) != 1 || fmt.Sprint(rows[0]["n"]) != "6" {
					t.Fatalf("finalized raw cache not genuinely persisted: %v", rows)
				}
			})
			o.OpenID = "finality-2"
			var next *RuntimeRunner
			if backend == "postgres" {
				next, err = OpenRuntimePostgres(context.Background(), o)
			} else {
				next, err = OpenRuntimePGlite(context.Background(), o)
			}
			if err == nil {
				_ = next.Close(context.Background())
				t.Fatal("reopen accepted replacement finalized identity")
			}
		})
	}
}
