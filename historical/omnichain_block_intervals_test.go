package historical

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeOmnichainBlockIntervals(t *testing.T) {
	chains := map[uint64]omniAccountChain{
		101: mineOmniAccountChain(t, 101, []uint64{10, 20, 30, 40, 50, 60, 70, 80, 90}),
		202: mineOmniAccountChain(t, 202, []uint64{12, 22, 32, 42, 52, 62, 72, 82, 92}),
	}
	want := []string{
		"202/0/genesis", "101/1/tx", "101/1/A", "202/1/A",
		"101/2/B", "202/3/C", "101/4/B", "202/4/C", "202/5/C",
		"101/6/A", "101/6/B", "202/6/A",
	}
	const failingCallback = "101/6/B"
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cached := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/cache-one-%t", backend, cached), func(t *testing.T) {
				continuousRoot(t)
				ctx := t.Context()
				raw := fmt.Sprintf(`{
					"ordering": "omnichain",
					"chains": {
						"one": {"id":101,"rpc":%q,"cacheRpcRequests":%t,"reorgWindow":0},
						"two": {"id":202,"rpc":%q,"cacheRpcRequests":%t,"reorgWindow":0}
					},
					"blocks": {
						"Genesis": {"chain":"two","startBlock":0,"endBlock":0,"interval":5},
						"A": {"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":8,"interval":5},
						"B": {"chain":"one","startBlock":2,"endBlock":7,"interval":2},
						"C": {"chain":"two","startBlock":3,"endBlock":5,"interval":1}
					},
					"accounts": {"Wallet":{"chain":"one","address":%q,"startBlock":1,"endBlock":8}}
				}`, chains[101].url, cached, chains[202].url, !cached, chains[101].wallet)
				cfg, err := config.ParseSourceConfiguration([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
				if err != nil {
					t.Fatal(err)
				}
				fail := true
				var calls []string
				handler := func(name string) indexing.Handler {
					return func(ctx context.Context, a *indexing.HandlerArgs) error {
						id := finiteChain(a)
						n, err := continuousNumber(a)
						if err != nil {
							return err
						}
						key := fmt.Sprintf("%d/%d/%s", id, n, name)
						calls = append(calls, key)
						row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
						if err != nil {
							return err
						}
						if row == nil {
							_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "value": key}, store.Conflict{})
						} else {
							_, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"value": fmt.Sprint(row["value"]) + "|" + key})
						}
						if err == nil && fail && key == failingCallback {
							return fmt.Errorf("stop on second simultaneous block handler")
						}
						return err
					}
				}
				build, err := config.BuildSourceIndexing(ctx, cfg, base, []config.SourceIndexingFunction{
					{Name: "Genesis:block", Fn: handler("genesis")},
					{Name: "A:block", Fn: handler("A")},
					{Name: "B:block", Fn: handler("B")},
					{Name: "C:block", Fn: handler("C")},
					{Name: "Wallet:transaction:to", Fn: handler("tx")},
				})
				if err != nil {
					t.Fatal(err)
				}
				f := newManagedFiniteFixture(t, backend, build)
				omnichainGoldenStorage(&f)
				opts := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "block-intervals", Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
					var out []rpc.Requester
					for _, chain := range build.Executable().Chains {
						out = append(out, rpc.New(chains[runtimeChainID(chain)].url))
					}
					return out, nil
				}}
				opts.Historical.QuerySize = 1
				var cursors map[uint64]string
				committed := 0
				for _, owner := range []string{"interrupted", "resumed", "completed"} {
					opts.OpenID = owner
					r := runtimeOpen(t, backend, opts)
					calls = nil
					result, err := r.Run(ctx)
					if fail {
						if err == nil || !strings.Contains(err.Error(), "stop on second") || !slices.Equal(calls, want[:slices.Index(want, failingCallback)+1]) || result.Ready {
							t.Fatalf("interrupted: %+v %v %v", result, calls, err)
						}
					} else {
						if err != nil || !result.Completed || !result.Ready || !slices.Equal(calls, want[committed:]) {
							t.Fatalf("reopen: %+v %v %v", result, calls, err)
						}
						control, err := r.Control(ctx)
						if err != nil {
							t.Fatal(err)
						}
						last, err := checkpoint.Decode(control.LogOnlyLastEvent)
						if err != nil || last.ChainID != 202 || last.BlockNumber != 6 || !last.MatchesEvent("block") {
							t.Fatalf("last event %+v: %v", last, err)
						}
						for id := range chains {
							scanned, err := checkpoint.Decode(control.LogOnlyScanHeads[id].Checkpoint)
							if err != nil || scanned.ChainID != id || scanned.BlockNumber != 8 {
								t.Fatalf("chain %d scan %+v: %v", id, scanned, err)
							}
						}
						if cursors != nil && !maps.Equal(cursors, control.LogicalCursors) {
							t.Fatalf("cursors changed after completed reopen")
						}
						cursors = maps.Clone(control.LogicalCursors)
					}
					if err := r.Close(ctx); err != nil {
						t.Fatal(err)
					}
					managedInspect(t, f, func(db store.Backend) {
						rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
						var got []string
						if len(rows) == 1 {
							got = strings.Split(fmt.Sprint(rows[0]["value"]), "|")
						}
						if len(rows) > 1 || len(got) > len(want) || !slices.Equal(got, want[:len(got)]) || (fail && slices.Contains(got, "101/6/A")) || (!fail && len(got) != len(want)) {
							t.Fatalf("durable callbacks %v", got)
						}
						committed = len(got)
					})
					fail = false
				}
			})
		}
	}
}
