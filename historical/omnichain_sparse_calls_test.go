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

func TestRuntimeOmnichainSparseContractCalls(t *testing.T) {
	nodes := map[uint64]omniFactoryNode{
		101: mineOmniFactory(t, 101, 1, factoryChild, factoryOther, []int{10, 40, 50, 70, 90, 110}, true),
		202: mineOmniFactory(t, 202, 2, factoryOther, factoryChild, []int{20, 30, 60, 75, 95, 115}, true),
	}
	created := abi.Event{Name: "Created", Type: "event", Inputs: []abi.Parameter{{Name: "child", Type: "address", Indexed: true}}}
	children, err := config.FactoryByParameter(created, config.DynamicFactoryParameter("child"), config.FactoryOptions{Address: factoryParent, StartBlock: 1, EndBlock: 2})
	if err != nil {
		t.Fatal(err)
	}
	params := []abi.Parameter{{Name: "value", Type: "uint256"}}
	fn := abi.Function{Type: "function", Name: "record", Inputs: params, Outputs: params, StateMutability: "payable"}
	cfg := map[string]any{
		"ordering": "omnichain",
		"chains": map[string]any{
			"one": map[string]any{"id": 101, "rpc": nodes[101].url, "reorgWindow": 0},
			"two": map[string]any{"id": 202, "rpc": nodes[202].url, "reorgWindow": 0, "cacheRpcRequests": false},
		},
		"contracts": map[string]any{
			"Static": map[string]any{"chain": "one", "abi": []any{fn}, "address": factoryChild, "includeCallTraces": true, "startBlock": 1, "endBlock": 5},
			"Child":  map[string]any{"chain": "two", "abi": []any{fn}, "address": children, "includeCallTraces": true, "startBlock": 1, "endBlock": 5},
		},
	}
	want := []string{"101/1", "202/2", "101/2", "101/4", "202/4"}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			var seen []string
			fail := true
			record := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				n, err := continuousNumber(a)
				if err != nil {
					return err
				}
				hash, err := factoryTransactionProperty(a, "transaction", "hash")
				if err != nil || hash != nodes[id].txs[int(n)] || fmt.Sprint(a.Event.Get("result")) != fmt.Sprint(n) {
					return fmt.Errorf("chain %d block %d: hash %v, result %v, error %v", id, n, hash, a.Event.Get("result"), err)
				}
				key := fmt.Sprintf("%d/%d", id, n)
				_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "value": key}, store.Conflict{Action: store.ConflictUpdate, Update: func(row store.Row) (store.Row, error) {
					return store.Row{"value": fmt.Sprint(row["value"]) + "|" + key}, nil
				}})
				if err != nil {
					return err
				}
				seen = append(seen, key)
				if fail && key == "202/4" {
					return errors.New("interrupt sparse CALL")
				}
				return nil
			})
			b := compileOmniLogsConfig(t, cfg, []config.SourceIndexingFunction{{Name: "Static.record()", Fn: record}, {Name: "Child.record()", Fn: record}})
			f := newManagedFiniteFixture(t, backend, b)
			omnichainGoldenStorage(&f)
			opts := RuntimeOptions{Historical: runtimeFixtureInput(f), FactorySources: true, SourceCheckpoints: true, BuildID: "sparse-calls"}
			opts.Historical.QuerySize = 1
			opts.Requesters = func(context.Context, uint64) ([]rpc.Requester, error) {
				var reqs []rpc.Requester
				for _, chain := range b.Executable().Chains {
					reqs = append(reqs, rpc.New(nodes[runtimeChainID(chain)].url))
				}
				return reqs, nil
			}
			committed := 0
			for _, owner := range []string{"failed", "recovered", "completed"} {
				opts.OpenID = owner
				seen = nil
				r := runtimeOpen(t, backend, opts)
				result, err := r.Run(t.Context())
				if fail {
					if err == nil || !strings.Contains(err.Error(), "interrupt sparse CALL") || result.Ready || result.Completed || !slices.Equal(seen, want) {
						t.Fatalf("failure: %+v calls %v error %v", result, seen, err)
					}
				} else {
					if err != nil || !result.Recovered || !result.Ready || !result.Completed || !slices.Equal(seen, want[committed:]) {
						t.Fatalf("reopen: %+v calls %v error %v", result, seen, err)
					}
					c, err := r.Control(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					for id := range nodes {
						end, err := checkpoint.Decode(c.LogOnlyScanHeads[id].Checkpoint)
						if err != nil || end.ChainID != id || end.BlockNumber != 5 {
							t.Fatalf("scan head %d: %+v %v", id, end, err)
						}
					}
					last, err := checkpoint.Decode(c.LogOnlyLastEvent)
					if err != nil || last.ChainID != 202 || last.BlockNumber != 4 || !last.MatchesEvent("trace") {
						t.Fatalf("last CALL %+v, %v", last, err)
					}
				}
				if err := r.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
					if len(rows) != 1 {
						t.Fatal(rows)
					}
					got := strings.Split(fmt.Sprint(rows[0]["value"]), "|")
					if len(got) > len(want) || !slices.Equal(got, want[:len(got)]) || (fail && len(got) == len(want)) || (!fail && len(got) != len(want)) {
						t.Fatalf("committed %v, expected prefix of %v", got, want)
					}
					committed = len(got)
				})
				fail = false
			}
		})
	}
}
