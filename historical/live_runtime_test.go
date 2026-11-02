package historical

import (
	"context"
	"fmt"
	"os"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"reflect"
	"strings"
	"testing"
)

// Supplemental native tests: no upstream mapping credit. Acquisition is HTTP
// JSON-RPC -> actual source synchronizer -> SQL -> ABI decoder -> Service.
func runtimeBuild(t *testing.T, p *finiteRPC, end, interval uint64, h indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true},"two":{"id":2,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":%d,"interval":%d}}}`, p.server.URL+"/1", p.server.URL+"/2", end, interval)
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
	b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: h}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func runtimeFactory(p *finiteRPC, epochs *[]uint64) func(context.Context, uint64) ([]rpc.Requester, error) {
	return func(ctx context.Context, g uint64) ([]rpc.Requester, error) {
		*epochs = append(*epochs, g)
		out := []rpc.Requester{}
		for _, id := range []uint64{1, 2} {
			r := rpc.New(fmt.Sprintf("%s/%d", p.server.URL, id))
			r.Retries = 0
			r.RequestsPerSecond = 0
			out = append(out, r)
		}
		return out, nil
	}
}
func runtimeOpen(t *testing.T, backend string, o RuntimeOptions) *RuntimeRunner {
	t.Helper()
	var r *RuntimeRunner
	var err error
	if backend == "postgres" {
		r, err = OpenRuntimePostgres(context.Background(), o)
	} else {
		r, err = OpenRuntimePGlite(context.Background(), o)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return r
}
func TestRuntimeBoundedLifecycle(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained task test root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			p := newFiniteRPC(t, 3)
			var calls []string
			var escapedDB *indexing.Database
			var escapedClient *indexing.Client
			var escapedCtx context.Context
			var activeHistory []bool
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				var n uint64
				var err error
				if v, ok := a.Event.Property("block"); ok {
					activeHistory = append(activeHistory, true)
					raw, e := v.Get("number")
					err = e
					if err == nil {
						n, err = uintQuantity(raw)
					}
				} else {
					activeHistory = append(activeHistory, false)
					n, err = uintQuantity(a.Event.Get("block").(map[string]any)["number"])
				}
				if err != nil {
					return err
				}
				p.handlerActive.Store(true)
				defer p.handlerActive.Store(false)
				if _, err = a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40)); err != nil {
					return err
				}
				escapedClient = a.Context.Client
				escapedDB, escapedCtx = a.Context.DB, context.WithoutCancel(ctx)
				calls = append(calls, fmt.Sprintf("%d:%d", finiteChain(a), n))
				return finiteIncrement(ctx, a)
			})
			b := runtimeBuild(t, p, 3, 1, h)
			f := newManagedFiniteFixture(t, backend, b)
			f.options.Owned.Storage.Schema.Tables[0].Indexes = []schema.Index{{Name: "account_balance_idx", Columns: []string{"balance"}}}
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "runtime-native-v1", OpenID: "runtime-open-1", ViewsSchema: f.options.Owned.Storage.BusinessNamespace + "_v", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, backend, o)
			if ready, err := r.Ready(context.Background()); err != nil || ready {
				t.Fatalf("initial ready=%v err=%v", ready, err)
			}
			activations := 0
			o.OnActivated = nil
			r.options.OnActivated = func(ctx context.Context) error {
				activations++
				ready, err := r.Ready(ctx)
				if err != nil || !ready {
					return fmt.Errorf("activation ready=%v err=%v", ready, err)
				}
				if len(calls) != 4 {
					return fmt.Errorf("activated before history finished: %v", calls)
				}
				return nil
			}
			out, err := r.Run(context.Background())
			if err != nil {
				c, e := r.domains.Control(context.Background())
				t.Logf("diagnostic calls=%v control=%+v readErr=%v", calls, c, e)
				t.Fatal(err)
			}
			if !out.Ready || !out.Completed || out.HistoricalBlocks != 4 || out.LiveBlocks != 2 || activations != 1 {
				t.Fatalf("result=%+v activations=%d", out, activations)
			}
			seen := map[string]int{}
			for i, call := range calls {
				parts := strings.Split(call, ":")
				seen[parts[0]]++
				if parts[1] != fmt.Sprint(seen[parts[0]]) || (i < 4 && seen[parts[0]] > 2) || (i >= 4 && seen[parts[0]] != 3) {
					t.Fatalf("per-chain or phase order differs: %v", calls)
				}
			}
			if seen["1"] != 3 || seen["2"] != 3 {
				t.Fatal(calls)
			}

			if !reflect.DeepEqual(activeHistory, []bool{true, true, true, true, false, false}) {
				t.Fatal(activeHistory)
			}
			if _, err = escapedDB.Find(escapedCtx, "account", store.Row{"id": "chain1"}); err == nil {
				t.Fatal("escaped binding retained access")
			}
			if _, err = escapedClient.GetBalance(escapedCtx, "0x"+strings.Repeat("1", 40)); err == nil {
				t.Fatal("sealed client retained RPC/cache access")
			}
			c, err := r.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if c.SafeOrdinal != 4 || c.Tail != 6 || len(c.ReplayTargets) != 0 || !c.Serving {
				t.Fatalf("control=%+v", c)
			}
			before := len(calls)
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedCatalog(t, f, o.ViewsSchema, true)
			o.OpenID = "runtime-open-2"
			o.OnActivated = func(ctx context.Context) error {
				if len(calls) != before+2 {
					return fmt.Errorf("recovery activated before real unsafe replay: %v", calls)
				}
				return nil
			}
			r2 := runtimeOpen(t, backend, o)
			c, err = r2.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if c.Serving || len(c.ReplayTargets) != 2 {
				t.Fatalf("reopen lost private replay contract %+v", c)
			}
			if ready, err := r2.Ready(context.Background()); err != nil || ready {
				t.Fatalf("recovery ready=%v err=%v", ready, err)
			}
			out, err = r2.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !out.Recovered || !out.Completed || !out.Ready || out.HistoricalBlocks != 0 || out.LiveBlocks != 2 {
				t.Fatalf("recovery result=%+v", out)
			}
			if err = r2.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(b store.Backend) {
				rows := managedRows(t, b, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || rows[0]["balance"] != "3" || rows[1]["balance"] != "3" {
					t.Fatalf("actual writes duplicated/lost: %v", rows)
				}
			})
			if len(epochs) != 6 {
				t.Fatalf("fresh acquisition/activation/reopen factories=%v", epochs)
			}
		})
	}
}
