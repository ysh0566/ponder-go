package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/store"
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
	return omniLogOnlyRangeBuild(t, g, p, handlers, 3)
}
func omniLogOnlyRangeBuild(t *testing.T, g omniLogsGolden, p *finiteRPC, handlers map[string]indexing.Handler, end uint64) *config.SourceIndexingBuild {
	t.Helper()
	return omniLogsBuildRange(t, g, p, handlers, 1, end, false)
}

// Add matching, malformed Value(uint256) logs to ranges whose pinned
// snapshots contain no callbacks. The ABI decoder must omit only these logs.
func abiSkippedLogOnlyCases(t *testing.T) []omniLogOnlyCase {
	t.Helper()
	cases := loadOmniLogOnlyGolden(t)
	var result []omniLogOnlyCase
	full := loadOmniLogsGolden(t)
	for _, fixture := range cases {
		if fixture.Name != "all_empty" && fixture.Name != "trailing_empty" && fixture.Name != "empty_chain_failure" && fixture.Name != "small" {
			continue
		}
		for i := range fixture.Data.Chains {
			chain := &fixture.Data.Chains[i]
			included := make(map[any]bool)
			for _, log := range chain.Logs {
				included[log["blockNumber"]] = true
			}
			for _, original := range full.Chains[i].Logs {
				if included[original["blockNumber"]] {
					continue
				}
				log := maps.Clone(original)
				height, err := uintQuantity(log["blockNumber"])
				if err != nil {
					t.Fatal(err)
				}
				block := chain.Blocks[height]
				tx := block["transactions"].([]any)[0].(map[string]any)
				log["blockHash"], log["transactionHash"], log["data"] = block["hash"], tx["hash"], "0x01"
				chain.Logs = append(chain.Logs, log)
			}
			if fixture.Name == "small" {
				// All valid callbacks remain at heights 1 and 3.
				block := chain.Blocks[2]
				log := maps.Clone(chain.Logs[0])
				tx := block["transactions"].([]any)[0].(map[string]any)
				log["blockNumber"], log["blockHash"], log["transactionHash"], log["data"] = block["number"], block["hash"], tx["hash"], "0x01"
				chain.Logs = slices.Insert(chain.Logs, 4, log)
			}
		}
		if fixture.Name == "trailing_empty" {
			// With query size 6, the pinned source confirms block 1 when its
			// final SQL page contains four matching, undecodable logs.
			for _, snap := range fixture.Data.Cases[0].Snapshots {
				for i := range snap.Checkpoints {
					position := &snap.Checkpoints[i]
					head, err := runtimeHead(position.ID, fixture.Data.Chains[i].Blocks[1])
					if err != nil {
						t.Fatal(err)
					}
					position.Safe, position.Latest, position.Finalized = head.Checkpoint, head.Checkpoint, head.Checkpoint
				}
			}
		}
		fixture.Name = "abi_skip_" + fixture.Name
		result = append(result, fixture)
	}
	return result
}

func TestRuntimeOmnichainLogOnlySource(t *testing.T) {
	fixtures := append(loadOmniLogOnlyGolden(t), abiSkippedLogOnlyCases(t)...)
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
					o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omnichain-logonly-" + fixture.Name, Requesters: runtimeFactory(p, &epochs)}
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
						if control.Version != "managed-live-omnichain-logonly-v17" {
							t.Fatalf("log-only position format %s", control.Version)
						}
						if want.Ready {
							headers := map[uint64]map[uint64]store.ManagedLiveHead{}
							for _, chain := range g.Chains {
								headers[chain.ID] = map[uint64]store.ManagedLiveHead{}
								for _, raw := range chain.Blocks {
									n, _ := uintQuantity(raw["number"])
									head, err := runtimeHead(chain.ID, raw)
									if err != nil {
										t.Fatal(err)
									}
									headers[chain.ID][n] = head
								}
							}
							read := func(chain, height uint64) (store.ManagedLiveHead, error) {
								head, ok := headers[chain][height]
								if !ok {
									return head, fmt.Errorf("missing header %d/%d", chain, height)
								}
								return head, nil
							}
							if e = r.domains.ActivatePagedLogOnlyRuntime(context.Background(), control.LastOperation, control.Cursors, read); e != nil {
								t.Fatalf("exact activation retry: %v", e)
							}
							if e = r.domains.ActivateRuntime(context.Background(), control.LastOperation, control.Cursors); e == nil {
								t.Fatal("proof-free retry accepted")
							}
							cursor, _ := checkpoint.Decode(control.Cursors[1].Checkpoint)
							stale := headers[1][cursor.BlockNumber]
							stale.Hash = "0x" + strings.Repeat("a", 64)
							headers[1][cursor.BlockNumber] = stale
							if e = r.domains.ActivatePagedLogOnlyRuntime(context.Background(), control.LastOperation, control.Cursors, read); e == nil {
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
							_, err = r.runOmnichainFinalized(context.Background(), faultEpoch, control, map[uint64]*runtimeLineage{}, RuntimeResult{})
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
					if fixture.Name == "small" && counts.FullAt["0x2"] != 0 {
						t.Fatalf("transaction bodies requested for empty middle block %d times", counts.FullAt["0x2"])
					}
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
