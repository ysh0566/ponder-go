package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
)

type omniLogOnlyCase struct {
	Name string         `json:"name"`
	Data omniLogsGolden `json:"data"`
}

func loadOmniLogOnlyGolden(t *testing.T) []omniLogOnlyCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/runtime_omnichain_logonly_source_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		SourceCommit string            `json:"source_commit"`
		Cases        []omniLogOnlyCase `json:"cases"`
	}
	if err = json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.SourceCommit != "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334" || len(g.Cases) != 9 {
		t.Fatal("unexpected pinned log-only source cases")
	}
	return g.Cases
}
func omniLogOnlyBuild(t *testing.T, g omniLogsGolden, p *finiteRPC, handlers map[string]indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	chains := map[string]any{"one": map[string]any{"id": 1, "rpc": p.server.URL + "/1", "reorgWindow": 0, "cacheRpcRequests": true}, "two": map[string]any{"id": 2, "rpc": p.server.URL + "/2", "reorgWindow": 0, "cacheRpcRequests": true}}
	selected := map[string]any{"one": map[string]any{}, "two": map[string]any{}}
	contracts := map[string]any{}
	for _, kind := range []string{"A", "B"} {
		contracts["Token"+kind] = map[string]any{"abi": []any{g.ABIItem}, "address": g.Address, "chain": selected, "startBlock": 1, "endBlock": 3}
	}
	raw, err := json.Marshal(map[string]any{"ordering": "omnichain", "chains": chains, "contracts": contracts})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseSourceConfiguration(raw)
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
	build, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "TokenA:Value", Fn: handlers["A"]}, {Name: "TokenB:Value", Fn: handlers["B"]}})
	if err != nil {
		t.Fatal(err)
	}
	return build
}
func TestRuntimeOmnichainLogOnlySource(t *testing.T) {
	fixtures := loadOmniLogOnlyGolden(t)
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			for _, fixture := range fixtures {
				t.Run(fixture.Name, func(t *testing.T) {
					continuousRoot(t)
					g := fixture.Data
					if len(g.Cases) != 1 {
						t.Fatal("one exact source scenario required")
					}
					tc := g.Cases[0]
					p, counts := omniLogsRPC(t, g)
					pass := 0
					calls := []string{}
					handlers := omniLogsHandlers(func() []omniLogsEvent { return tc.Snapshots[pass-1].Calls }, &calls, func(key string) error {
						if pass == 1 && key == tc.FailKey {
							return errors.New("controlled static log handler failure after shared write")
						}
						return nil
					})
					build := omniLogOnlyBuild(t, g, p, handlers)
					f := newManagedFiniteFixture(t, backend, build)
					omnichainGoldenStorage(&f)
					epochs := []uint64{}
					o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omnichain-logonly-v16-" + fixture.Name, Requesters: runtimeFactory(p, &epochs)}
					if tc.QuerySize != 12000 {
						o.Historical.QuerySize = tc.QuerySize
					}
					for _, want := range tc.Snapshots {
						pass = want.Pass
						calls = []string{}
						o.OpenID = fmt.Sprintf("log-only-%d", pass)
						r := runtimeOpen(t, backend, o)
						out, err := r.Run(context.Background())
						if want.Error != nil {
							if err == nil || !strings.Contains(err.Error(), "controlled static log") || out.Ready || out.Completed {
								t.Fatalf("source log-only failure result%+v err%v", out, err)
							}
						} else if err != nil || !out.Ready || !out.Completed {
							t.Fatalf("source log-only result%+v err%v", out, err)
						}
						if pass > 1 && !out.Recovered {
							t.Fatalf("log-only cold open not recovered %+v", out)
						}
						expected := []string{}
						for _, event := range want.Calls {
							expected = append(expected, event.Key)
						}
						if !reflect.DeepEqual(calls, expected) {
							t.Fatalf("source log-only sequence got%v want%v", calls, expected)
						}
						ready, e := r.Ready(context.Background())
						if e != nil || ready != want.Ready {
							t.Fatalf("source log-only Ready got%v err%v want%v", ready, e, want.Ready)
						}
						control, e := r.Control(context.Background())
						if e != nil {
							t.Fatal(e)
						}
						if control.Version != "managed-live-omnichain-logonly-v16" {
							t.Fatalf("log-only position format %s", control.Version)
						}
						if want.Ready {
							lineage := map[uint64][]store.ManagedLiveHead{}
							for _, chain := range g.Chains {
								cursor, _ := checkpoint.Decode(control.Cursors[chain.ID].Checkpoint)
								for _, raw := range chain.Blocks {
									n, _ := uintQuantity(raw["number"])
									if n < cursor.BlockNumber {
										continue
									}
									head, err := runtimeHead(chain.ID, raw)
									if err != nil {
										t.Fatal(err)
									}
									lineage[chain.ID] = append(lineage[chain.ID], head)
								}
							}
							if e = r.domains.ActivateLogOnlyRuntime(context.Background(), control.LastOperation, control.Cursors, lineage); e != nil {
								t.Fatalf("exact activation retry: %v", e)
							}
							if e = r.domains.ActivateRuntime(context.Background(), control.LastOperation, control.Cursors); e == nil {
								t.Fatal("proof-free retry accepted")
							}
							lineage[1][0].Hash = "0x" + strings.Repeat("a", 64)
							if e = r.domains.ActivateLogOnlyRuntime(context.Background(), control.LastOperation, control.Cursors, lineage); e == nil {
								t.Fatal("changed lineage retry accepted")
							}
						}
						if fixture.Name == "all_empty" && pass == tc.Snapshots[len(tc.Snapshots)-1].Pass {
							if len(control.LogOnlyScanHeads) != 2 {
								t.Fatal("missing durable scan evidence")
							}
							faultEpoch, err := r.newEpoch(context.Background(), nil)
							if err != nil {
								t.Fatal(err)
							}
							_, err = r.runOmnichainFinalized(context.Background(), faultEpoch, control, map[uint64]map[uint64]store.ManagedLiveHead{}, RuntimeResult{})
							if err == nil || !strings.Contains(err.Error(), "scanned endpoint") {
								_ = faultEpoch.Abort(context.Background())
								t.Fatalf("endpoint mismatch accepted: %v", err)
							}
							// A missing deferred Abort leaves this scope open and Close returns Busy.
							defer faultEpoch.Abort(context.Background())
						}
						if e = r.Close(context.Background()); e != nil {
							t.Fatal(e)
						}
						assertOmniLogsSnapshot(t, f, control, want)
					}
					counts.mu.Lock()
					defer counts.mu.Unlock()
					for _, id := range []uint64{1, 2} {
						if counts.Logs[id] == 0 {
							t.Fatalf("chain%d skipped actual source RPC %+v", id, counts)
						}
					}
				})
			}
		})
	}
}
