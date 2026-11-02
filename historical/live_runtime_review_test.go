package historical

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
	"time"
)

func runtimeFixtureInput(f finiteFixture) Options {
	return Options{Build: f.options.Build, Owned: f.options.Owned, Acquisition: f.options.Acquisition}
}
func TestRuntimeTimestampFinality(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"same_second_burst", "slow_blocks", "window_larger_than_height"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				root := os.Getenv("PONDER_M45_TEST_ROOT")
				if root == "" {
					t.Fatal("retained root required")
				}
				t.Setenv("PONDER_M44_TEST_ROOT", root)
				start, end, window, want := uint64(8), uint64(8), uint64(1), uint64(7)
				if kind == "slow_blocks" {
					start, end, window, want = 10, 10, 5, 9
				}
				if kind == "window_larger_than_height" {
					start, end, window, want = 1, 1, 2000, 0
				}
				s := &runtimeRPCState{latest: map[uint64]uint64{1: 10, 2: 10}}
				s.mutate = func(_, n uint64, _ int, _, _ bool, b map[string]any) {
					timestamp := uint64(1000) + 12*n
					if kind == "same_second_burst" {
						timestamp = 90 + n
						if n >= 8 {
							timestamp = 100
						}
					}
					b["timestamp"] = fmt.Sprintf("0x%x", timestamp)
				}
				p := controlledRuntimeRPC(t, s)
				calls := 0
				b := runtimeBuild(t, p, end, 1, indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error { calls++; return finiteIncrement(ctx, a) }))
				for i, c := range b.Executable().Chains {
					c["reorgWindow"] = window
					for _, cb := range b.Executable().EventCallbacks[i] {
						cb["filter"].(map[string]any)["fromBlock"] = float64(start)
					}
				}
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				r := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "timestamp-finality-v1", OpenID: "seconds-1", Requesters: runtimeFactory(p, &epochs)})
				c, err := r.Control(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				for id, h := range c.FinalizedHeads {
					cp, err := checkpoint.Decode(h.Checkpoint)
					if err != nil || cp.BlockNumber != want {
						t.Fatalf("window in seconds: chain%d finalized=%d want=%d err=%v", id, cp.BlockNumber, want, err)
					}
				}
				out, err := r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || out.HistoricalBlocks != 0 || out.LiveBlocks != 2 || calls != 2 {
					t.Fatalf("unfinalized bounded end %+v calls=%d err=%v", out, calls, err)
				}
				c, err = r.Control(context.Background())
				if err != nil || c.Finalizations != 0 || c.SafeOrdinal != 0 || c.Tail != 2 {
					t.Fatalf("unsafe end was pruned: %+v err=%v", c, err)
				}
			})
		}
	}
}
func TestRuntimeRejectsHistoricalOptions(t *testing.T) {
	for _, kind := range []string{"query_size", "query_parameters", "clock", "retry_wait", "on_committed"} {
		t.Run(kind, func(t *testing.T) {
			p := newFiniteRPC(t, 3)
			b := runtimeBuild(t, p, 3, 1, indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil }))
			input := Options{Build: b}
			switch kind {
			case "query_size":
				input.QuerySize = 1
			case "query_parameters":
				input.MaxQueryParameters = 100
			case "clock":
				input.Clock = time.Now
			case "retry_wait":
				input.RetryWait = func(context.Context, time.Duration) error { return nil }
			case "on_committed":
				input.OnCommitted = func(context.Context, GroupResult) error { return fmt.Errorf("must not be ignored") }
			}
			epochs := []uint64{}
			_, err := OpenRuntimePostgres(context.Background(), RuntimeOptions{Historical: input, BuildID: "reject-options", OpenID: "reject-options", Requesters: runtimeFactory(p, &epochs)})
			if err == nil || !strings.Contains(err.Error(), "does not support historical") {
				t.Fatalf("accepted ignored %s: %v", kind, err)
			}
		})
	}
}
func TestRuntimeCompletedMetricsRollback(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			p := newFiniteRPC(t, 3)
			raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true},"two":{"id":2,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true}},"blocks":{"A":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":3,"interval":1},"B":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":3,"interval":1}}}`, p.server.URL+"/1", p.server.URL+"/2")
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
			a := indexing.Handler(finiteIncrement)
			bHandler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				if v, ok := a.Event.Property("block"); ok {
					if _, err := v.Get("gasUsed"); err != nil {
						return err
					}
				}
				return finiteIncrement(ctx, a)
			})
			b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "A:block", Fn: a}, {Name: "B:block", Fn: bHandler}})
			if err != nil {
				t.Fatal(err)
			}
			for _, group := range b.Executable().EventCallbacks {
				for _, cb := range group {
					cb["filter"].(map[string]any)["include"] = []string{"block.number", "block.timestamp", "block.hash"}
				}
			}
			f := newManagedFiniteFixture(t, backend, b)
			metrics := &indexing.Metrics{}
			f.options.Owned.Service.Metrics = metrics
			epochs := []uint64{}
			r := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "metrics-rollback-v1", OpenID: "metrics-1", Requesters: runtimeFactory(p, &epochs)})
			out, err := r.Run(context.Background())
			if err != nil || !out.Completed || (out.Refetches < 1 || out.Refetches > 4) {
				t.Fatalf("real same-block refetch %+v err=%v", out, err)
			}
			if metrics.Completed["A:block"] != 6 || metrics.Completed["B:block"] != 6 {
				t.Fatalf("rollback counted uncommitted events: %v", metrics.Completed)
			}
		})
	}
}

func TestRuntimeCompletedMetricsFlushRollback(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			p := newFiniteRPC(t, 3)
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				_, err := a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "bad", "balance": new(big.Int).Lsh(big.NewInt(1), 300)}, store.Conflict{})
				return err
			})
			b := runtimeBuild(t, p, 3, 1, h)
			f := newManagedFiniteFixture(t, backend, b)
			metrics := &indexing.Metrics{}
			f.options.Owned.Service.Metrics = metrics
			epochs := []uint64{}
			r := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "metrics-flush-v1", OpenID: "metrics-flush-1", Requesters: runtimeFactory(p, &epochs)})
			out, err := r.Run(context.Background())
			if err == nil || out.Completed || out.Ready {
				t.Fatalf("real SQL numeric overflow did not roll back: %+v %v", out, err)
			}
			if metrics.Completed["Blocks:block"] != 0 {
				t.Fatalf("flush rollback counted completed handler: %v", metrics.Completed)
			}
			if len(metrics.Durations) != 1 {
				t.Fatalf("attempt observations were erased: %v", metrics.Durations)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(b store.Backend) {
				rows := managedRows(t, b, "SELECT id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
				if len(rows) != 0 {
					t.Fatalf("numeric overflow committed rows: %v", rows)
				}
			})
		})
	}
}

func TestRuntimeCompletelyEmptyLifecycle(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			p := newFiniteRPC(t, 3)
			raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true},"two":{"id":2,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true}},"contracts":{"C":{"abi":[{"type":"event","name":"Ping","inputs":[{"name":"value","type":"uint256","indexed":false}]}],"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":3}}}`, p.server.URL+"/1", p.server.URL+"/2")
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
			b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "C:Ping", Fn: indexing.Handler(func(context.Context, *indexing.HandlerArgs) error {
				t.Fatal("empty RPC logs dispatched a handler")
				return nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "empty-runtime-v1", OpenID: "empty-1", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || out.HistoricalBlocks != 4 || out.LiveBlocks != 2 {
				t.Fatalf("empty runtime %+v %v", out, err)
			}
			c, err := r.Control(context.Background())
			if err != nil || c.Tail != 6 || c.SafeJournalBoundary != 0 {
				t.Fatalf("empty segments lost %+v %v", c, err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			o.OpenID = "empty-2"
			r = runtimeOpen(t, backend, o)
			out, err = r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || !out.Recovered || out.LiveBlocks != 2 {
				t.Fatalf("empty nonempty-storage replay %+v %v", out, err)
			}
		})
	}
}
