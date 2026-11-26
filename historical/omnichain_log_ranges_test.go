package historical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func rangeLogBuild(t *testing.T, g omniLogsGolden, rpc *finiteRPC, handlers map[string]indexing.Handler, lateAddress string) *config.SourceIndexingBuild {
	t.Helper()
	chains := map[string]any{}
	for id, name := range []string{"one", "two"} {
		chains[name] = map[string]any{"id": id + 1, "rpc": fmt.Sprintf("%s/%d", rpc.server.URL, id+1), "reorgWindow": 0}
	}
	contracts := map[string]any{
		"TokenA": map[string]any{"abi": []any{g.ABIItem}, "address": g.Address, "chain": map[string]any{
			"one": map[string]any{"startBlock": 1, "endBlock": 1},
			"two": map[string]any{"startBlock": 1, "endBlock": 3},
		}},
		"TokenB": map[string]any{"abi": []any{g.ABIItem}, "address": g.Address, "chain": map[string]any{
			"one": map[string]any{"startBlock": 3, "endBlock": 3, "address": lateAddress},
			"two": map[string]any{"startBlock": 2, "endBlock": 3},
		}},
	}
	return compileOmniLogsConfig(t, map[string]any{"ordering": "omnichain", "chains": chains, "contracts": contracts}, []config.SourceIndexingFunction{{Name: "TokenA:Value", Fn: handlers["A"]}, {Name: "TokenB:Value", Fn: handlers["B"]}})
}

func TestRuntimeOmnichainLogRanges(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, name := range []string{"fresh", "cached-outside-range"} {
			t.Run(backend+"/"+name, func(t *testing.T) {
				continuousRoot(t)
				g := loadOmniLogOnlyGolden(t)[0].Data
				cached := name == "cached-outside-range"
				lateAddress := g.Address
				if cached {
					lateAddress = "0x" + strings.Repeat("7", 40)
					// Each cached log on chain one would decode under a source
					// whose address matches but whose height range excludes it.
					for _, log := range g.Chains[0].Logs {
						if log["blockNumber"] == "0x1" {
							log["address"] = lateAddress
						}
					}
				}
				var golden []omniLogsEvent
				for _, ev := range g.Cases[0].Snapshots[0].Calls {
					key := strings.Split(ev.Key, ":")
					if key[0] == "1" && (cached || (key[1] == "1" && key[2] == "B") || (key[1] == "3" && key[2] == "A")) {
						continue
					}
					if key[0] == "2" && key[1] == "1" && key[2] == "B" {
						continue
					}
					golden = append(golden, ev)
				}
				expected := golden
				var calls []string
				fail := true
				handlers := omniLogsHandlers(func() []omniLogsEvent { return expected }, &calls, func(key string) error {
					if fail && key == "2:3:B:1" {
						fail = false
						return errors.New("range callback interrupted")
					}
					return nil
				})
				p, counts := omniLogsRPC(t, g)
				build := rangeLogBuild(t, g, p, handlers, lateAddress)
				f := newManagedFiniteFixture(t, backend, build)
				omnichainGoldenStorage(&f)
				var epochs []uint64
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "log-ranges-" + name, Requesters: runtimeFactory(p, &epochs)}
				o.Historical.QuerySize = 6
				ctx := context.Background()
				for pass := range 3 {
					calls = nil
					o.OpenID = fmt.Sprintf("open-%d", pass)
					r := runtimeOpen(t, backend, o)
					if cached && pass == 0 {
						seedSparseSource(t, r, g)
					}
					out, err := r.Run(ctx)
					if pass == 0 {
						if err == nil || !strings.Contains(err.Error(), "range callback interrupted") || out.Ready || out.Completed || len(calls) == 0 || calls[len(calls)-1] != "2:3:B:1" {
							t.Fatalf("callback rollback: result=%+v calls=%v err=%v", out, calls, err)
						}
					} else if err != nil || !out.Completed || !out.Ready || !out.Recovered || !slices.Equal(calls, omniLogsKeys(expected)) {
						t.Fatalf("pass %d: result=%+v calls=%v err=%v", pass, out, calls, err)
					}
					control, err := r.Control(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if err = r.Close(ctx); err != nil {
						t.Fatal(err)
					}
					managedInspect(t, f, func(db store.Backend) {
						rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
						var keys []string
						if len(rows) > 0 {
							keys = strings.Split(fmt.Sprint(rows[0]["value"]), "|")
						}
						if pass == 0 {
							if len(keys) >= len(calls) || !slices.Equal(keys, calls[:len(keys)]) {
								t.Fatalf("rollback persisted part of failing batch: %v from %v", keys, calls)
							}
							expected = golden[len(keys):]
						} else {
							if !slices.Equal(keys, omniLogsKeys(golden)) {
								t.Fatalf("durable calls %v", keys)
							}
							expected = nil
						}
					})
					if pass != 0 {
						for _, c := range g.Chains {
							end, err := runtimeHead(c.ID, c.Blocks[3])
							if err != nil {
								t.Fatal(err)
							}
							want := end.Checkpoint
							if cached && c.ID == 1 {
								ts, err := uintQuantity(c.Blocks[1]["timestamp"])
								if err != nil {
									t.Fatal(err)
								}
								want, err = checkpoint.Encode(checkpoint.Block(ts, c.ID, 1, false))
								if err != nil {
									t.Fatal(err)
								}
							}
							if control.LogOnlyScanHeads[c.ID] != end || control.LogicalCursors[c.ID] != want || control.LogicalSafe[c.ID] != want || control.LogicalFinality[c.ID] != want {
								t.Fatalf("chain %d scan=%+v position=%s safe=%s final=%s want=%s", c.ID, control.LogOnlyScanHeads[c.ID], control.LogicalCursors[c.ID], control.LogicalSafe[c.ID], control.LogicalFinality[c.ID], want)
							}
						}
					}
				}
				counts.mu.Lock()
				defer counts.mu.Unlock()
				for _, id := range []uint64{1, 2} {
					if (counts.Logs[id] == 0) != cached {
						t.Fatalf("unexpected log RPC reads: %v", counts.Logs)
					}
				}
			})
		}
	}
}
