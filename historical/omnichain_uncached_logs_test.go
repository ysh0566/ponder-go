package historical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestOmnichainUncachedLogs(t *testing.T) {
	fixtures := make(map[string]omniLogsGolden)
	for _, f := range abiSkippedLogOnlyCases(t) {
		fixtures[f.Name] = f.Data
	}
	for _, backend := range []string{"postgres", "pglite"} {
		for _, tc := range []struct {
			name, fixture string
			disabled      []uint64
			interrupt     bool
		}{
			{"middle_gap_recovery", "abi_skip_small", []uint64{1, 2}, true},
			{"mixed_trailing_logs", "abi_skip_trailing_empty", []uint64{2}, false},
			{"no_decodable_events", "abi_skip_all_empty", []uint64{1, 2}, false},
		} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				continuousRoot(t)
				ctx := context.Background()
				g := fixtures[tc.fixture]
				golden := g.Cases[0].Snapshots[0]
				expected := golden.Calls
				var calls []string
				failNext := tc.interrupt
				interrupted := errors.New("interrupt after last handler write")
				handlers := omniLogsHandlers(func() []omniLogsEvent { return expected }, &calls, func(key string) error {
					if failNext && key == golden.Calls[len(golden.Calls)-1].Key {
						failNext = false
						return interrupted
					}
					return nil
				})
				p, counts := omniLogsRPC(t, g)
				build := omniLogsBuildWithSetup(t, g, p, handlers, 1, 3, false, nil, tc.disabled...)
				f := newManagedFiniteFixture(t, backend, build)
				omnichainGoldenStorage(&f)
				var epochs []uint64
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: tc.name, Requesters: runtimeFactory(p, &epochs)}
				o.Historical.QuerySize = 6
				pageSize := uint64(1)
				o.Historical.Acquisition.EthGetLogsBlockRange = &pageSize
				passes := 2
				if tc.interrupt {
					passes++
				}
				for pass := 0; pass < passes; pass++ {
					calls = nil
					o.OpenID = fmt.Sprintf("uncached-log-open-%d", pass)
					shouldFail := failNext
					r := runtimeOpen(t, backend, o)
					result, err := r.Run(ctx)
					control, e := r.Control(ctx)
					if e != nil {
						t.Fatal(e)
					}
					if e = r.Close(ctx); e != nil {
						t.Fatal(e)
					}
					if shouldFail {
						if err == nil || !strings.Contains(err.Error(), interrupted.Error()) || result.Ready || result.Completed || control.Serving {
							t.Fatalf("failed callback: result=%+v error=%v", result, err)
						}
						managedInspect(t, f, func(db store.Backend) {
							rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
							var committed []string
							if len(rows) > 0 {
								committed = strings.Split(fmt.Sprint(rows[0]["value"]), "|")
							}
							if len(committed) >= len(calls) || !slices.Equal(committed, calls[:len(committed)]) {
								t.Fatalf("committed partial callback group %v from %v", committed, calls)
							}
							expected = golden.Calls[len(committed):]
						})
						continue
					}
					if err != nil || !result.Completed || !result.Ready || (pass > 0 && !result.Recovered) || !slices.Equal(calls, omniLogsKeys(expected)) {
						t.Fatalf("pass %d: result=%+v err=%v calls=%v", pass, result, err, calls)
					}
					assertOmniLogsRows(t, f, golden)
					for _, chain := range g.Chains {
						end, e := runtimeHead(chain.ID, chain.Blocks[3])
						if e != nil || control.LogOnlyScanHeads[chain.ID] != end {
							t.Fatalf("missing scan endpoint on %d: %+v, %v", chain.ID, control.LogOnlyScanHeads, e)
						}
						if len(golden.Calls) == 0 {
							start, e := runtimeHead(chain.ID, chain.Blocks[1])
							cp, _ := checkpoint.Decode(start.Checkpoint)
							down, _ := checkpoint.Encode(checkpoint.Block(cp.BlockTimestamp, chain.ID, 1, false))
							if e != nil || control.LogicalSafe[chain.ID] != down || control.LogicalCursors[chain.ID] != down || control.LogicalFinality[chain.ID] != down {
								t.Fatalf("empty scan advanced chain %d business progress: %+v", chain.ID, control)
							}
						}
					}
					expected = nil
				}
				syncStore := f
				syncStore.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
				managedInspect(t, syncStore, func(db store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.SyncNamespace + "_rpc_1")
					for _, chain := range g.Chains {
						logs := managedRows(t, db, "SELECT log_index FROM "+ns+".logs WHERE chain_id=$1", chain.ID)
						intervals := managedRows(t, db, "SELECT fragment_id FROM "+ns+".intervals WHERE chain_id=$1", chain.ID)
						disabled := slices.Contains(tc.disabled, chain.ID)
						if disabled && len(logs) != 0 || disabled != (len(intervals) == 0) {
							t.Fatalf("chain %d disabled=%v logs=%d intervals=%d", chain.ID, disabled, len(logs), len(intervals))
						}
						counts.mu.Lock()
						n := counts.Logs[chain.ID]
						counts.mu.Unlock()
						if n == 0 {
							t.Fatalf("no log RPC on chain %d", chain.ID)
						}
					}
				})
			})
		}
	}
}

type unavailableLogArchive struct{ *rpc.Client }

func (c unavailableLogArchive) Call(ctx context.Context, method string, params, out any) error {
	if method == "eth_getLogs" {
		return errors.New("archive log range unavailable")
	}
	return c.Client.Call(ctx, method, params, out)
}

func TestOmnichainUncachedArchiveRetry(t *testing.T) {
	nodes := map[uint64]omniFactoryNode{
		101: mineOmniFactory(t, 101, 2, factoryChild, factoryOther, []int{10, 40, 55, 70, 95, 115}),
		202: mineOmniFactory(t, 202, 1, factoryOther, factoryChild, []int{20, 30, 60, 75, 96, 116}),
	}
	order := []string{"101/1", "202/1", "202/2", "101/2", "101/3", "202/3", "101/4", "202/4"}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			var calls []string
			handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				n, err := continuousNumber(a)
				if err != nil {
					return err
				}
				key := fmt.Sprintf("%d/%d", id, n)
				hash, err := factoryTransactionProperty(a, "transaction", "hash")
				if err != nil || hash != nodes[id].txs[int(n)] {
					return fmt.Errorf("%s transaction %v: %v", key, hash, err)
				}
				_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": key, "value": hash}, store.Conflict{})
				if err == nil {
					calls = append(calls, key)
				}
				return err
			})
			build := compileOmniLogsConfig(t, map[string]any{
				"ordering": "omnichain",
				"chains": map[string]any{
					"one": map[string]any{"id": 101, "rpc": nodes[101].url, "reorgWindow": 0, "cacheRpcRequests": false},
					"two": map[string]any{"id": 202, "rpc": nodes[202].url, "reorgWindow": 0, "cacheRpcRequests": false},
				},
				"contracts": map[string]any{"Token": map[string]any{
					"chain":      map[string]any{"one": map[string]any{"address": nodes[101].child}, "two": map[string]any{"address": nodes[202].child}},
					"startBlock": 1, "endBlock": 5,
					"abi": []abi.Event{{Type: "event", Name: "Ping", Inputs: []abi.Parameter{{Name: "value", Type: "uint256"}}}},
				}},
			}, []config.SourceIndexingFunction{{Name: "Token:Ping", Fn: handler}})
			f := newManagedFiniteFixture(t, backend, build)
			omnichainGoldenStorage(&f)
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "uncached-archive"}
			o.Historical.QuerySize = 1
			for pass := 0; pass < 3; pass++ {
				calls = nil
				o.OpenID = fmt.Sprintf("owner-%d", pass)
				o.Requesters = func(context.Context, uint64) ([]rpc.Requester, error) {
					var reqs []rpc.Requester
					for _, chain := range build.Executable().Chains {
						id := runtimeChainID(chain)
						client := rpc.New(nodes[id].url)
						client.Retries = 0
						client.RequestsPerSecond = 0
						var req rpc.Requester = client
						if pass == 0 && id == 202 && len(calls) == len(order) {
							req = unavailableLogArchive{client}
						}
						reqs = append(reqs, req)
					}
					return reqs, nil
				}
				r := runtimeOpen(t, backend, o)
				result, err := r.Run(t.Context())
				control, e := r.Control(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				if pass == 0 {
					if err == nil || !strings.Contains(err.Error(), "archive log range unavailable") || control.Serving || result.Completed || result.Ready || !slices.Equal(calls, order) {
						t.Fatalf("failed archive scan: calls=%v result=%+v err=%v", calls, result, err)
					}
				} else if err != nil || !control.Serving || !result.Completed || !result.Recovered || len(calls) != 0 {
					t.Fatalf("reopen %d: calls=%v result=%+v err=%v", pass, calls, result, err)
				}
				if e := r.Close(t.Context()); e != nil {
					t.Fatal(e)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT id,value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
					if len(rows) != len(order) {
						t.Fatalf("retained rows: %v", rows)
					}
					for _, row := range rows {
						var id uint64
						var n int
						if _, err := fmt.Sscanf(fmt.Sprint(row["id"]), "%d/%d", &id, &n); err != nil || row["value"] != nodes[id].txs[n] {
							t.Fatalf("unexpected row %v", row)
						}
					}
				})
			}
		})
	}
}
