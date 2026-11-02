package historical

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"sync"
	"testing"
	"time"
)

func sourceMatrixBuild(t *testing.T, p *finiteRPC, start, end, window uint64, setupMode string, empty bool, h, setup indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	blockChains := `"one":{},"two":{}`
	window2 := window
	if setupMode == "mixed" {
		blockChains = `"one":{"startBlock":1,"endBlock":3},"two":{"startBlock":3,"endBlock":5}`
		window2 = 2
	}
	src := fmt.Sprintf(`"blocks":{"Blocks":{"chain":{%s},"startBlock":%d,"endBlock":%d,"interval":1}}`, blockChains, start, end)
	funcs := []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: h}}
	if empty {
		src = fmt.Sprintf(`"contracts":{"Token":{"abi":[{"type":"event","name":"Ping","inputs":[]}],"address":"0x0000000000000000000000000000000000000001","chain":{"one":{},"two":{}},"startBlock":%d,"endBlock":%d}}`, start, end)
		funcs = []config.SourceIndexingFunction{{Name: "Token:Ping", Fn: h}}
	}
	if setupMode != "none" {
		if empty {
			src = strings.TrimSuffix(src, "}") + fmt.Sprintf(`,"Setup":{"abi":[],"chain":{"one":{},"two":{}},"startBlock":%d,"endBlock":%d}}`, start, end)
		} else {
			chains := `"one":{},"two":{}`
			if setupMode == "one" {
				chains = `"one":{}`
			}
			src += fmt.Sprintf(`,"contracts":{"Setup":{"abi":[],"chain":{%s},"startBlock":%d,"endBlock":%d}}`, chains, start, end)
		}
		funcs = append(funcs, config.SourceIndexingFunction{Name: "Setup:setup", Fn: setup})
	}
	raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":%d,"cacheRpcRequests":true},"two":{"id":2,"rpc":%q,"reorgWindow":%d,"cacheRpcRequests":true}},%s}`, p.server.URL+"/1", window, p.server.URL+"/2", window2, src)
	cfg, e := config.ParseSourceConfiguration([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	base, e := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 16})
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range base.RPCClients() {
		r.RequestsPerSecond = 0
		r.Retries = 0
	}
	b, e := config.BuildSourceIndexing(context.Background(), cfg, base, funcs)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestRuntimeSourceInitialMatrix(t *testing.T) {
	cases := []struct {
		name, oracle, setup        string
		start, end, latest, window uint64
		empty                      bool
	}{
		{"genesis", "start0_end0_final0", "both", 0, 0, 0, 0, false},
		{"future_start", "start3_final1", "both", 3, 5, 5, 4, false},
		{"equal_start_final", "start3_final3", "one", 3, 5, 5, 2, false},
		{"past_end", "start3_final6_past_end", "both", 3, 5, 6, 0, false},
		{"no_setup", "start1_final2", "none", 1, 3, 3, 1, false},
		{"empty_events", "start1_final2", "both", 1, 3, 3, 1, true},
		{"mixed_bounds", "start1_final2", "mixed", 1, 5, 3, 1, false},
	}
	for _, backend := range []string{"postgres", "pglite"} {
		for _, tc := range cases {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				continuousRoot(t)
				s := newContinuousRPC(t, tc.latest)
				if tc.setup == "mixed" {
					s.latest[2] = 5
				}
				calls := map[uint64]int{}
				setups := map[uint64]int{}
				observed := 0
				var observeMu sync.Mutex
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					calls[finiteChain(a)]++
					return finiteIncrement(ctx, a)
				})
				setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					setups[finiteChain(a)]++
					_, e := a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("setup-%d", finiteChain(a)), "balance": "99"}, store.Conflict{})
					return e
				})
				b := sourceMatrixBuild(t, s.p, tc.start, tc.end, tc.window, tc.setup, tc.empty, h, setup)
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				want := sourceInitialGoldens(t, tc.oracle)
				if tc.setup == "mixed" {
					want[2] = sourceInitialGoldens(t, "start3_final3")[2]
				}
				var r *RuntimeRunner
				check := func(ctx context.Context) error {
					got, e := r.PublicCheckpoints(ctx)
					if e != nil {
						return e
					}
					for id, cp := range want {
						if got[id] != cp {
							return fmt.Errorf("source oracle differs chain%d got=%s want=%s", id, got[id], cp)
						}
					}
					observed++
					return nil
				}
				o := RuntimeOptions{SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "source-matrix-" + tc.name, OpenID: "source-matrix-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				if tc.setup != "none" {
					o.OnSetupCommitted = check
				} else {
					// Read the committed bootstrap at the first subsequent source RPC.
					s.gate = func(ctx context.Context, _ uint64, method string, _ []any) error {
						observeMu.Lock()
						defer observeMu.Unlock()
						if r == nil || observed != 0 || method != "eth_getBlockByNumber" {
							return nil
						}
						c, e := r.domains.Control(ctx)
						if e != nil {
							return e
						}
						if c.Batches == 1 {
							return check(ctx)
						}
						return nil
					}
				}
				r = runtimeOpen(t, backend, o)
				out, e := r.Run(context.Background())
				if e != nil || !out.Completed || !out.Ready || observed != 1 {
					t.Fatalf("matrix result=%+v observed=%d err=%v", out, observed, e)
				}
				expected := int(tc.end - tc.start + 1)
				if tc.empty {
					expected = 0
				}
				if tc.setup == "mixed" {
					expected = 3
				}
				for _, id := range []uint64{1, 2} {
					if calls[id] != expected {
						t.Fatalf("executed chain%d count=%d want=%d", id, calls[id], expected)
					}
				}
				if tc.setup == "none" && len(setups) != 0 || tc.setup == "one" && (setups[1] != 1 || setups[2] != 0) || (tc.setup == "both" || tc.setup == "mixed") && (setups[1] != 1 || setups[2] != 1) {
					t.Fatalf("setup scope %v", setups)
				}
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				s.mu.Lock()
				s.gate = nil
				s.mu.Unlock()
				o.OnSetupCommitted = nil
				o.OpenID = "source-matrix-reopen"
				r = runtimeOpen(t, backend, o)
				out, e = r.Run(context.Background())
				if e != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("reopen=%+v err=%v", out, e)
				}
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				// Unsafe bounded suffixes really execute again after rollback on reopen.
				// The durable business projection must contain each canonical event once.
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
					balances := map[string]string{}
					for _, row := range rows {
						balances[fmt.Sprint(row["id"])] = fmt.Sprint(row["balance"])
					}
					for _, id := range []uint64{1, 2} {
						key := fmt.Sprintf("chain%d", id)
						if expected == 0 {
							if _, exists := balances[key]; exists {
								t.Fatalf("empty source wrote business row %v", balances)
							}
						} else if balances[key] != fmt.Sprint(expected) {
							t.Fatalf("recovery duplicated/lost canonical business writes %v", balances)
						}
					}
				})
			})
		}
	}
}
