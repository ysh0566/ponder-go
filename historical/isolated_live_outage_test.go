package historical

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeIsolatedLiveOutage(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			node := newContinuousRPC(t, 1)
			var offline atomic.Bool
			var failures atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/1" && offline.Load() {
					failures.Add(1)
					http.Error(w, "chain one unavailable", http.StatusServiceUnavailable)
					return
				}
				node.p.server.Config.Handler.ServeHTTP(w, req)
			}))
			t.Cleanup(gateway.Close)
			raw := fmt.Sprintf(`{"ordering":"experimental_isolated","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":20},"two":{"id":2,"rpc":%q,"reorgWindow":20}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":5,"interval":1}}}`, gateway.URL+"/1", gateway.URL+"/2")
			calls := map[uint64][]uint64{}
			handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				height, err := continuousNumber(a)
				if err != nil {
					return err
				}
				id := finiteChain(a)
				calls[id] = append(calls[id], height)
				_, err = a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "height": int(height), "hash": a.Event.Get("block").(map[string]any)["hash"]}, store.Conflict{})
				return err
			})
			cfg, err := config.ParseSourceConfiguration([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			offline.Store(true)
			build, err := config.BuildRuntimeSourceIndexing(t.Context(), cfg, config.SourceBuildOptions{}, []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: handler}})
			if err != nil || failures.Load() != 0 {
				t.Fatalf("compile with a participating chain offline: requests=%d, err=%v", failures.Load(), err)
			}
			offline.Store(false)
			f := newManagedFiniteFixture(t, backend, build)
			f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "seen", PrimaryKey: []string{"chain_id", "height"}, Columns: []schema.Column{
				{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "height", Type: "integer", NotNull: true}, {Name: "hash", Type: "text", NotNull: true},
			}}}}
			var epochs []uint64
			var r *RuntimeRunner
			var original store.ManagedLiveHead
			var retryChains []uint64
			var forks int
			var recovered bool
			peerAdvanced := make(chan struct{})
			const retryInterval = 7 * time.Millisecond
			var hold atomic.Bool
			live := &RuntimeLiveOptions{PollingInterval: time.Millisecond, RetryInterval: retryInterval, HeaderCacheCapacity: 1}
			live.Wait = func(ctx context.Context, d time.Duration) error {
				if d == retryInterval && hold.Load() {
					select {
					case <-peerAdvanced:
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(40 * time.Second):
						return fmt.Errorf("healthy chain failed to commit while its peer waited to reconnect")
					}
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(d):
					return nil
				}
			}
			registry := metrics.New()
			live.OnProgress = func(ctx context.Context, p RuntimeLiveProgress) error {
				switch p.Type {
				case "ready":
					if original.Hash == "" {
						node.setLatest(2)
					} else {
						recovered = true
					}
				case "fetch-retry":
					retryChains = append(retryChains, p.ChainID)
					if len(retryChains) == 2 {
						hold.Store(true)
						node.mu.Lock()
						node.latest[2] = 4
						node.mu.Unlock()
					}
				case "reorg":
					if p.ChainID != 1 {
						return fmt.Errorf("unexpected peer reorg on %d", p.ChainID)
					}
					assertSyncMetrics(t, registry, "one", p.Head, 1, -1)
					forks++
				case "block":
					cp, err := checkpoint.Decode(p.Head.Checkpoint)
					if err != nil {
						return err
					}
					if p.ChainID == 1 && cp.BlockNumber == 2 && original.Hash == "" {
						original = p.Head
						offline.Store(true)
					}
					if p.ChainID == 2 && cp.BlockNumber == 4 {
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						ready, err := r.Ready(ctx)
						if err != nil {
							return err
						}
						if ready || c.Serving || !offline.Load() || failures.Load() < 2 || c.Cursors[1] != original || c.Cursors[2] != p.Head {
							return fmt.Errorf("unexpected state during outage: ready=%v serving=%v cursors=%v failures=%d", ready, c.Serving, c.Cursors, failures.Load())
						}
						assertSyncMetrics(t, registry, "one", original, 1, -1)
						assertSyncMetrics(t, registry, "two", p.Head, 1, -1)
						node.fork(1, 2)
						node.setLatest(5)
						offline.Store(false)
						close(peerAdvanced)
					}
				}
				return nil
			}
			o := RuntimeOptions{BuildID: "isolated-live-outage", OpenID: "initial", Historical: runtimeFixtureInput(f), Requesters: runtimeFactory(&finiteRPC{server: gateway}, &epochs), Live: live}
			r = runtimeOpen(t, backend, o)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			out, err := r.Run(metrics.WithChainSync(metrics.WithDatabase(ctx, registry), registry))
			if err != nil || !out.Ready || !out.Completed || !recovered || len(retryChains) < 2 || forks != 1 {
				t.Fatalf("run=%+v retry chains=%v forks=%d recovered=%t callbacks=%v: %v", out, retryChains, forks, recovered, calls, err)
			}
			body, err := registry.Collect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, pool := range []string{"user", "sync"} {
				sample := fmt.Sprintf(`ponder_postgres_query_total{pool=%q}`, pool)
				if strings.Contains(body, sample) != (backend == "postgres") {
					t.Fatalf("unexpected %s sample on %s", sample, backend)
				}
			}
			for _, id := range retryChains {
				if id != 1 {
					t.Fatalf("retry marked healthy chain %d as disconnected", id)
				}
			}
			if !slices.Equal(calls[1], []uint64{1, 2, 2, 3, 4, 5}) || !slices.Equal(calls[2], []uint64{1, 2, 3, 4, 5}) {
				t.Fatalf("callback heights: %v", calls)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT chain_id::text AS id,height::text AS height,hash FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".seen ORDER BY chain_id,height")
				if len(rows) != 10 {
					t.Fatalf("business rows after recovery: %v", rows)
				}
				node.mu.Lock()
				defer node.mu.Unlock()
				for i, row := range rows {
					id, height := uint64(i/5+1), uint64(i%5+1)
					if row["id"] != fmt.Sprint(id) || row["height"] != fmt.Sprint(height) || row["hash"] != node.canonical[id][height]["hash"] {
						t.Fatalf("row differs from canonical chain %d height %d: %v", id, height, row)
					}
				}
			})
		})
	}
}

// An initialized writer can audit both stores and let an available chain
// commit even when the other RPC is absent throughout the cold opening.
func TestRuntimeIsolatedOfflineReopen(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, deepFork := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deep_fork_%t", backend, deepFork), func(t *testing.T) {
				continuousRoot(t)
				ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
				defer cancel()
				node := newContinuousRPC(t, 2)
				var offline, peerOffline atomic.Bool
				gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if (offline.Load() && req.URL.Path == "/1") || (peerOffline.Load() && req.URL.Path == "/2") {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					node.p.server.Config.Handler.ServeHTTP(w, req)
				}))
				defer gateway.Close()
				raw := fmt.Sprintf(`{"ordering":"experimental_isolated","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":0},"two":{"id":2,"rpc":%q,"reorgWindow":0}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":5,"interval":1}}}`, gateway.URL+"/1", gateway.URL+"/2")
				calls := map[uint64][]uint64{}
				handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					id := finiteChain(a)
					n, err := continuousNumber(a)
					if err != nil {
						return err
					}
					block, err := omniLogsFields(a.Event.Get("block"), "hash")
					if err != nil {
						return err
					}
					calls[id] = append(calls[id], n)
					_, err = a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "height": int(n), "hash": block["hash"]}, store.Conflict{})
					return err
				})
				b := runtimeSourceBuild(t, raw, config.SourceIndexingFunction{Name: "Blocks:block", Fn: handler})
				f := newManagedFiniteFixture(t, backend, b)
				f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "seen", PrimaryKey: []string{"chain_id", "height"}, Columns: []schema.Column{
					{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "height", Type: "integer", NotNull: true}, {Name: "hash", Type: "text", NotNull: true},
				}}}}
				stop := errors.New("stop after first catch-up")
				var epochs []uint64
				o := RuntimeOptions{BuildID: "isolated-offline", OpenID: "first", Historical: runtimeFixtureInput(f), Requesters: runtimeFactory(&finiteRPC{server: gateway}, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, RetryInterval: 20 * time.Millisecond}}
				o.OnActivated = func(context.Context) error { return stop }
				r := runtimeOpen(t, backend, o)
				if _, err := r.Run(ctx); !errors.Is(err, stop) {
					t.Fatal(err)
				}
				prior, err := r.Control(ctx)
				if err != nil || !store.ManagedLiveReachedBlock(prior, 1, 2) || !store.ManagedLiveReachedBlock(prior, 2, 2) {
					t.Fatalf("opening cursors: %v, err=%v", prior.Cursors, err)
				}
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				o.OpenID, o.OnActivated = "cold", nil
				offline.Store(true)
				peerOffline.Store(deepFork)
				node.setLatest(3)
				held := false
				registry := metrics.New()
				o.OnCommitted = func(ctx context.Context, g RuntimeCommit) error {
					n, err := checkpoint.Decode(g.Head.Checkpoint)
					if err != nil {
						return err
					}
					if g.ChainID == 1 {
						if n.BlockNumber == 3 {
							assertSyncMetrics(t, registry, "one", g.Head, 1, -1)
						}
						return nil
					}
					switch n.BlockNumber {
					case 3:
						node.setLatest(4)
					case 4:
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						ready, err := r.Ready(ctx)
						if err != nil {
							return err
						}
						if !offline.Load() || ready || c.Serving || c.Cursors[1] != prior.Cursors[1] || !slices.Equal(calls[1], []uint64{1, 2}) {
							return fmt.Errorf("peer failed to advance independently: control=%+v calls=%v", c, calls)
						}
						held = true
						from := uint64(3)
						if deepFork {
							from = 2
						}
						node.fork(1, from)
						node.setLatest(5)
						offline.Store(false)
					}
					return nil
				}
				r = runtimeOpen(t, backend, o)
				peerOffline.Store(false)
				out, err := r.Run(metrics.WithHistorical(metrics.WithChainSync(ctx, registry), registry))
				assertBackfillTimes(t, registry, "one", false, false)
				assertBackfillTimes(t, registry, "two", !deepFork, !deepFork)
				if !held || !out.Recovered {
					t.Fatalf("healthy commit=%t result=%+v: %v", held, out, err)
				}
				if deepFork {
					if err == nil || !errors.Is(err, store.ErrManagedLiveDeepReorg) || out.Ready || !slices.Equal(calls[1], []uint64{1, 2}) {
						t.Fatalf("returning finalized fork: result=%+v calls=%v err=%v", out, calls, err)
					}
				} else if err != nil || !out.Ready || !out.Completed || !slices.Equal(calls[1], []uint64{1, 2, 3, 4, 5}) || !slices.Equal(calls[2], []uint64{1, 2, 3, 4, 5}) {
					t.Fatalf("reopen: result=%+v calls=%v err=%v", out, calls, err)
				}
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT height::text AS height, hash FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".seen WHERE chain_id=1 ORDER BY height")
					expected := 5
					if deepFork {
						expected = 2
					}
					if len(rows) != expected {
						t.Fatal(rows)
					}
					if !deepFork {
						node.mu.Lock()
						defer node.mu.Unlock()
						for i, row := range rows {
							if row["hash"] != node.canonical[1][uint64(i+1)]["hash"] {
								t.Fatal("orphaned row", row)
							}
						}
					}
				})
			})
		}
	}
}

func TestIsolatedReturningFinality(t *testing.T) {
	node := newContinuousRPC(t, 2)
	requester := rpc.New(node.p.server.URL + "/1")
	requester.Retries = 0
	requester.RequestsPerSecond = 0
	head := func(n uint64) store.ManagedLiveHead {
		t.Helper()
		h, err := runtimeHead(1, node.canonical[1][n])
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	// An interrupted backfill acquired block 3 before the business safe
	// cursor at 1. A returning node must prove both positions.
	cursor := head(1)
	r := &RuntimeRunner{
		options:   RuntimeOptions{Live: &RuntimeLiveOptions{}},
		build:     &config.ExecutableSourceIndexingBuild{Chains: []indexing.Object{{"id": uint64(1), "reorgWindow": uint64(0)}}},
		finalized: map[uint64]store.ManagedLiveHead{1: head(3)},
	}
	if ok, err := r.verifyReturningChain(t.Context(), 0, requester, cursor); ok.Hash != "" || err != nil {
		t.Fatalf("lagging node: verified=%v err=%v", ok, err)
	}
	node.setLatest(4)
	if ok, err := r.verifyReturningChain(t.Context(), 0, requester, cursor); ok.Hash == "" || err != nil {
		t.Fatalf("canonical node: verified=%v err=%v", ok, err)
	}
	node.fork(1, 3)
	if ok, err := r.verifyReturningChain(t.Context(), 0, requester, cursor); ok.Hash != "" || !errors.Is(err, store.ErrManagedLiveDeepReorg) {
		t.Fatalf("replacement of recorded opening finality: verified=%v err=%v", ok, err)
	}
	if h, err := r.verifyReturningChain(t.Context(), 0, requester, store.ManagedLiveHead{}); h.Hash != "" || !errors.Is(err, store.ErrManagedLiveDeepReorg) {
		t.Fatalf("uninitialized chain changed its recorded opening: head=%v err=%v", h, err)
	}
	delete(r.finalized, 1)
	if h, err := r.verifyReturningChain(t.Context(), 0, requester, cursor); h.Hash != "" || !errors.Is(err, store.ErrMissingIsolatedBaseline) {
		t.Fatalf("stored cursor missing opening proof: head=%v err=%v", h, err)
	}
	r.build.SetupCallbacks = [][]indexing.Object{{{"block": uint64(5)}}}
	if h, err := r.verifyReturningChain(t.Context(), 0, requester, store.ManagedLiveHead{}); h.Hash != "" || err != nil {
		t.Fatalf("new chain below setup block: head=%v err=%v", h, err)
	}
	node.setLatest(5)
	if h, err := r.verifyReturningChain(t.Context(), 0, requester, store.ManagedLiveHead{}); h != head(5) || err != nil {
		t.Fatalf("first finalized head: got %v err=%v", h, err)
	}
}

// A partially initialized Isolated deployment can retry setup on the online
// chain while a peer whose setup already committed is offline.
func TestRuntimeIsolatedPartialSetupReopen(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
			defer cancel()
			node := newContinuousRPC(t, 3)
			var offline atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == fmt.Sprintf("/%d", offline.Load()) {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				node.p.server.Config.Handler.ServeHTTP(w, req)
			}))
			defer gateway.Close()
			raw := fmt.Sprintf(`{"ordering":"experimental_isolated","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":0},"two":{"id":2,"rpc":%q,"reorgWindow":0}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":3,"interval":1}},"contracts":{"Seed":{"chain":{"one":{},"two":{}},"abi":[],"startBlock":1,"endBlock":3}}}`, gateway.URL+"/1", gateway.URL+"/2")
			setupCalls := map[uint64]int{}
			heights := map[uint64][]uint64{}
			insert := func(ctx context.Context, a *indexing.HandlerArgs, height uint64) error {
				_, err := a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(finiteChain(a)), "height": int(height)}, store.Conflict{})
				return err
			}
			setup := func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				setupCalls[id]++
				if err := insert(ctx, a, 0); err != nil {
					return err
				}
				if id == 1 && setupCalls[id] == 1 {
					return errors.New("interrupted first setup")
				}
				return nil
			}
			block := func(ctx context.Context, a *indexing.HandlerArgs) error {
				n, err := continuousNumber(a)
				if err != nil {
					return err
				}
				id := finiteChain(a)
				heights[id] = append(heights[id], n)
				return insert(ctx, a, n)
			}
			build := runtimeSourceBuild(t, raw, config.SourceIndexingFunction{Name: "Seed:setup", Fn: indexing.Handler(setup)}, config.SourceIndexingFunction{Name: "Blocks:block", Fn: indexing.Handler(block)})
			f := newManagedFiniteFixture(t, backend, build)
			f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{
				Name: "seen", PrimaryKey: []string{"chain_id", "height"},
				Columns: []schema.Column{{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "height", Type: "integer", NotNull: true}},
			}}}
			var epochs []uint64
			o := RuntimeOptions{BuildID: "partial-setup", OpenID: "first", Historical: runtimeFixtureInput(f),
				Requesters: runtimeFactory(&finiteRPC{server: gateway}, &epochs),
				Live:       &RuntimeLiveOptions{PollingInterval: time.Millisecond, RetryInterval: 10 * time.Millisecond}}
			r := runtimeOpen(t, backend, o)
			if _, err := r.Run(ctx); err == nil || !strings.Contains(err.Error(), "interrupted first setup") {
				t.Fatalf("initial setup: %v", err)
			}
			prior, err := r.Control(ctx)
			if err != nil || len(prior.Cursors) != 1 || prior.Cursors[2].Hash == "" {
				t.Fatalf("partial setup: %+v %v", prior, err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			offline.Store(1)
			o.OpenID = "deferred-cursor"
			r = runtimeOpen(t, backend, o)
			deferred, err := r.Control(ctx)
			if err != nil || len(deferred.Cursors) != 1 || deferred.Cursors[2] != prior.Cursors[2] || deferred.Serving {
				t.Fatalf("deferred setup: %+v %v", deferred, err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			offline.Store(2)
			o.OpenID = "partial-reopen"
			advanced := false
			o.OnCommitted = func(ctx context.Context, commit RuntimeCommit) error {
				if commit.ChainID == 1 && commit.BlockRange[1] == 3 && !advanced {
					c, err := r.Control(ctx)
					if err != nil {
						return err
					}
					if c.Serving || c.Cursors[2] != prior.Cursors[2] || len(heights[2]) != 0 || offline.Load() != 2 {
						return fmt.Errorf("peer changed before reconnect: serving=%t cursors=%v events=%v", c.Serving, c.Cursors, heights)
					}
					advanced = true
					offline.Store(0)
				}
				return nil
			}
			r = runtimeOpen(t, backend, o)
			reopened, err := r.Control(ctx)
			if err != nil || reopened.Generation != deferred.Generation+1 || len(reopened.Cursors) != 1 || reopened.Cursors[2] != prior.Cursors[2] {
				t.Fatalf("reopen after deferred setup: %+v %v", reopened, err)
			}
			result, err := r.Run(ctx)
			if err != nil || !result.Recovered || !result.Ready || !result.Completed || !advanced {
				t.Fatalf("partial reopen: %+v advanced=%t error=%v", result, advanced, err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if setupCalls[1] != 2 || setupCalls[2] != 1 || !slices.Equal(heights[1], []uint64{1, 2, 3}) || !slices.Equal(heights[2], []uint64{1, 2, 3}) {
				t.Fatalf("setup attempts=%v event heights=%v", setupCalls, heights)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT chain_id::text AS id,height::text AS height FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".seen ORDER BY chain_id,height")
				if len(rows) != 8 {
					t.Fatalf("committed rows: %v", rows)
				}
				for i, row := range rows {
					if row["id"] != fmt.Sprint(1+i/4) || row["height"] != fmt.Sprint(i%4) {
						t.Fatalf("unexpected row %v", row)
					}
				}
			})
		})
	}
}

// A fresh deployment may have no RPC for an entire chain. Only a real
// canonical baseline may enroll it, even after a peer has unfinalized writes.
func TestRuntimeIsolatedDeferredBootstrap(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, setup := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/setup_%t", backend, setup), func(t *testing.T) {
				continuousRoot(t)
				ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
				defer cancel()
				node := newContinuousRPC(t, 2)
				allowBaseline := make(chan struct{})
				var awaitingPeer atomic.Bool
				node.mu.Lock()
				node.gate = func(ctx context.Context, id uint64, method string, args []any) error {
					if id != 1 || method != "eth_getBlockByNumber" || len(args) != 2 || args[0] != "0x0" || args[1] != true {
						return nil
					}
					if awaitingPeer.CompareAndSwap(false, true) {
						if !setup {
							node.fork(2, 2)
						}
						node.setLatest(3)
					}
					select {
					case <-allowBaseline:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				node.mu.Unlock()
				var oneOffline, twoOffline atomic.Bool
				oneOffline.Store(true)
				twoOffline.Store(!setup)
				gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Path == "/1" && oneOffline.Load() || req.URL.Path == "/2" && twoOffline.Load() {
						http.Error(w, "offline", http.StatusServiceUnavailable)
						return
					}
					node.p.server.Config.Handler.ServeHTTP(w, req)
				}))
				defer gateway.Close()
				raw := fmt.Sprintf(`{"ordering":"experimental_isolated","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":2},"two":{"id":2,"rpc":%q,"reorgWindow":2}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":0,"endBlock":4,"interval":1}},"contracts":{"Seed":{"chain":{"one":{},"two":{}},"abi":[],"startBlock":0,"endBlock":4}}}`, gateway.URL+"/1", gateway.URL+"/2")
				heights := map[uint64][]uint64{}
				setupCalls := map[uint64]int{}
				block := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					n, err := continuousNumber(a)
					if err != nil {
						return err
					}
					id := finiteChain(a)
					heights[id] = append(heights[id], n)
					h, err := omniLogsFields(a.Event.Get("block"), "hash")
					if err != nil {
						return err
					}
					_, err = a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "height": int(n), "hash": h["hash"]}, store.Conflict{})
					return err
				})
				callbacks := []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: block}}
				if setup {
					callbacks = append(callbacks, config.SourceIndexingFunction{Name: "Seed:setup", Fn: indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
						id := finiteChain(a)
						setupCalls[id]++
						_, err := a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "height": -1, "hash": "setup"}, store.Conflict{})
						return err
					})})
				}
				cfg, err := config.ParseSourceConfiguration([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				b, err := config.BuildRuntimeSourceIndexing(ctx, cfg, config.SourceBuildOptions{}, callbacks)
				if err != nil {
					t.Fatal(err)
				}
				f := newManagedFiniteFixture(t, backend, b)
				f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "seen", PrimaryKey: []string{"chain_id", "height"}, Columns: []schema.Column{
					{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "height", Type: "integer", NotNull: true}, {Name: "hash", Type: "text", NotNull: true},
				}}}}
				var r *RuntimeRunner
				var epochs []uint64
				var advanced, forked bool
				var setupCommitted int
				o := RuntimeOptions{BuildID: "deferred", OpenID: "first", Historical: runtimeFixtureInput(f), Requesters: runtimeFactory(&finiteRPC{server: gateway}, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, RetryInterval: 10 * time.Millisecond}}
				if setup {
					o.OnSetupCommitted = func(context.Context) error { setupCommitted++; return nil }
				}
				o.Live.OnProgress = func(ctx context.Context, p RuntimeLiveProgress) error {
					if p.Type == "fetch-retry" && p.ChainID == 2 {
						twoOffline.Store(false)
					}
					return nil
				}
				o.OnCommitted = func(ctx context.Context, g RuntimeCommit) error {
					if g.ChainID == 2 && g.BlockRange[1] == 2 {
						oneOffline.Store(false)
					}
					if g.ChainID == 2 && g.BlockRange[1] == 3 && !advanced {
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						ready, err := r.Ready(ctx)
						if err != nil {
							return err
						}
						_, initialized := c.Cursors[1]
						_, acquired := c.FinalizedHeads[1]
						if !awaitingPeer.Load() || ready || c.Serving || initialized || acquired || len(heights[1]) != 0 || setupCalls[1] != 0 || setupCommitted != 0 {
							return fmt.Errorf("absent chain acquired state: %+v", c)
						}
						advanced = true
						close(allowBaseline)
					}
					if g.ChainID == 1 && g.BlockRange[1] == 2 && !forked {
						node.fork(2, 3)
						node.setLatest(4)
						forked = true
					}
					return nil
				}
				r = runtimeOpen(t, backend, o)
				initial, err := r.Control(ctx)
				if err != nil || initial.FinalizedHeads[1].Hash != "" || len(initial.Cursors) != 0 || (!setup && len(initial.FinalizedHeads) != 0) {
					t.Fatalf("initial control %+v: %v", initial, err)
				}
				result, err := r.Run(ctx)
				if err != nil || !result.Ready || !result.Completed || !advanced || !forked {
					t.Fatalf("result=%+v advanced=%t forked=%t heights=%v: %v", result, advanced, forked, heights, err)
				}
				peerHeights := []uint64{0, 1, 2, 3, 3, 4}
				if !setup {
					peerHeights = []uint64{0, 1, 2, 2, 3, 3, 4}
				}
				if !slices.Equal(heights[1], []uint64{0, 1, 2, 3, 4}) || !slices.Equal(heights[2], peerHeights) {
					t.Fatalf("callback heights %v", heights)
				}
				if setup && (setupCommitted != 1 || setupCalls[1] != 1 || setupCalls[2] != 1) {
					t.Fatalf("setup committed=%d calls=%v", setupCommitted, setupCalls)
				}
				if err = r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT chain_id::text AS id,height::text AS n, hash FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".seen ORDER BY chain_id,height")
					perChain := 5
					if setup {
						perChain++
					}
					if len(rows) != 2*perChain {
						t.Fatal(rows)
					}
					node.mu.Lock()
					defer node.mu.Unlock()
					for i, row := range rows {
						id := uint64(1 + i/perChain)
						n := i % perChain
						if setup {
							n--
						}
						if row["id"] != fmt.Sprint(id) || row["n"] != fmt.Sprint(n) || n >= 0 && row["hash"] != node.canonical[id][uint64(n)]["hash"] || n < 0 && row["hash"] != "setup" {
							t.Fatalf("unexpected row %v", row)
						}
					}
				})
				o.OpenID = "reopen"
				o.OnCommitted = nil
				r = runtimeOpen(t, backend, o)
				result, err = r.Run(ctx)
				if err != nil || !result.Recovered || !result.Completed || setup && (setupCalls[1] != 1 || setupCalls[2] != 1) {
					t.Fatalf("reopen=%+v setup=%v err=%v", result, setupCalls, err)
				}
				if err = r.Close(ctx); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
