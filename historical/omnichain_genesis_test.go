package historical

import (
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
)

// Add one transaction with a matching log to each fixture's block zero.
// All transactions and logs keep the block's hash and number.
func addOmnichainGenesisLogs(g *omniLogsGolden) {
	for i := range g.Chains {
		c := &g.Chains[i]
		genesis, next := c.Blocks[0], c.Blocks[1]
		tx := maps.Clone(next["transactions"].([]any)[0].(map[string]any))
		log := maps.Clone(c.Logs[0])
		hash := fmt.Sprintf("0x%064x", 1000+c.ID)
		tx["hash"], tx["blockHash"], tx["blockNumber"] = hash, genesis["hash"], "0x0"
		log["transactionHash"], log["blockHash"], log["blockNumber"] = hash, genesis["hash"], "0x0"
		genesis["transactions"], genesis["logsBloom"] = []any{tx}, next["logsBloom"]
		c.Logs = append([]map[string]any{log}, c.Logs...)
	}
}

func omniGenesisEvents(t *testing.T, g omniLogsGolden, closing, genesisLogs bool, end uint64) []omniLogsEvent {
	t.Helper()
	var genesis, later []omniLogsEvent
	for _, e := range g.Cases[0].Snapshots[0].Calls {
		cp, err := checkpoint.Decode(e.Checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		block := strings.Contains(e.Key, ":block:")
		if block && !closing {
			continue
		}
		if cp.BlockNumber <= end {
			later = append(later, e)
		}
		if cp.BlockNumber != 1 || (!block && (!genesisLogs || cp.EventIndex != 0)) {
			continue
		}
		for _, c := range g.Chains {
			if c.ID != cp.ChainID {
				continue
			}
			raw := c.Blocks[0]
			ts, err := uintQuantity(raw["timestamp"])
			if err != nil {
				t.Fatal(err)
			}
			cp.BlockTimestamp, cp.BlockNumber = ts, 0
			e.Key = strings.Replace(e.Key, ":1:", ":0:", 1)
			e.Checkpoint, e.Hash, e.Timestamp = checkpoint.MustEncode(cp), fmt.Sprint(raw["hash"]), fmt.Sprint(ts)
			genesis = append(genesis, e)
		}
	}
	sort.SliceStable(genesis, func(i, j int) bool { return genesis[i].Checkpoint < genesis[j].Checkpoint })
	return append(genesis, later...)
}

func TestOmnichainGenesis(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, tc := range []struct {
			name          string
			closing, logs bool
			end           uint64
		}{
			{"closing_genesis", true, false, 0},
			{"closing_range", true, false, 3},
			{"log_range", false, false, 3},
			{"empty_genesis", false, false, 0},
			{"genesis_logs", false, true, 0},
		} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				continuousRoot(t)
				g := loadOmniLogsGolden(t)
				if tc.logs {
					addOmnichainGenesisLogs(&g)
				}
				want := omniGenesisEvents(t, g, tc.closing, tc.logs, tc.end)
				var keys []string
				for _, e := range want {
					keys = append(keys, e.Key)
				}
				p, _ := omniLogsRPC(t, g)
				var calls []string
				handlers := omniLogsHandlers(func() []omniLogsEvent { return want }, &calls, nil)
				b := omniLogsBuildRange(t, g, p, handlers, 0, tc.end, tc.closing)
				f := newManagedFiniteFixture(t, backend, b)
				omnichainGoldenStorage(&f)
				var epochs []uint64
				opts := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omni-genesis-" + tc.name, Requesters: runtimeFactory(p, &epochs)}
				opts.Historical.QuerySize = 1
				for _, owner := range []string{"first", "reopen"} {
					opts.OpenID = owner
					r := runtimeOpen(t, backend, opts)
					result, err := r.Run(t.Context())
					if err != nil || !result.Completed || !result.Ready || result.Recovered != (owner == "reopen") || !reflect.DeepEqual(calls, keys) {
						t.Fatalf("%s result=%+v calls=%v want=%v err=%v", owner, result, calls, keys, err)
					}
					control, err := r.Control(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					for _, c := range g.Chains {
						block := c.Blocks[tc.end]
						ts, err := uintQuantity(block["timestamp"])
						if err != nil {
							t.Fatal(err)
						}
						pos := checkpoint.MustEncode(checkpoint.Block(ts, c.ID, tc.end, len(keys) != 0))
						if control.LogicalCursors[c.ID] != pos || control.LogicalFinality[c.ID] != pos || control.LogicalSafe[c.ID] != pos || control.Cursors[c.ID].Hash != block["hash"] {
							t.Fatalf("%s chain %d: cursor=%s safe=%s finalized=%s hash=%s, want %s at %v", owner, c.ID, control.LogicalCursors[c.ID], control.LogicalSafe[c.ID], control.LogicalFinality[c.ID], control.Cursors[c.ID].Hash, pos, block["hash"])
						}
					}
					if err := r.Close(t.Context()); err != nil {
						t.Fatal(err)
					}
					var snapshot omniLogsSnapshot
					if len(keys) != 0 {
						snapshot.Rows = []map[string]any{{"id": "shared", "value": strings.Join(keys, "|")}}
					}
					assertOmniLogsSnapshot(t, f, control, snapshot)
				}
			})
		}
	}
}
