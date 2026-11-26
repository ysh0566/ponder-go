package historical

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type omniFactoryNode struct {
	url, child string
	txs        map[int]string
}

func mineOmniFactory(t *testing.T, id uint64, birth int, child, decoy string, times []int, sparse bool) omniFactoryNode {
	t.Helper()
	node := omniFactoryNode{url: testanvil.StartChain(t, id, "--no-mining", "--timestamp", "1700000000"), child: child, txs: map[int]string{}}
	client := rpc.New(node.url)
	call := func(method string, args ...any) any {
		t.Helper()
		if args == nil {
			args = []any{}
		}
		var result any
		if err := client.Call(t.Context(), method, args, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	sender := call("eth_accounts").([]any)[0].(string)
	// The parent emits Created(indexed child). Both candidate children emit
	// Ping(value) and return the uint256 argument passed to record(value).
	call("anvil_setCode", factoryParent, "0x73"+child[2:]+"7f"+factoryCreated[2:]+"60006000a200")
	worker := "0x6004356000527f" + factoryPing[2:] + "60206000a160206000f3"
	call("anvil_setCode", child, worker)
	call("anvil_setCode", decoy, worker)
	selector := crypto.Keccak256Hash([]byte("record(uint256)")).Hex()[:10]
	send := func(to, data string) string {
		return call("eth_sendTransaction", map[string]any{"from": sender, "to": to, "gas": "0x186a0", "value": "0x1", "data": data}).(string)
	}
	for i, ts := range times {
		n := i + 1
		call("evm_setNextBlockTimestamp", 1700000000+ts)
		if n == birth {
			send(factoryParent, "0x")
		}
		if n <= 4 || (sparse && n == 5) {
			data := selector + fmt.Sprintf("%064x", n)
			send(decoy, data)
			if !sparse || (n != 3 && n != 5) {
				node.txs[n] = send(child, data)
			}
		}
		call("evm_mine")
	}
	return node
}

func omniFactoryBuild(t *testing.T, nodes map[uint64]omniFactoryNode, handler func(string) indexing.Handler, logChains map[uint64]bool) *config.SourceIndexingBuild {
	t.Helper()
	both := map[string]any{"one": map[string]any{}, "two": map[string]any{}}
	created := abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{
		{Name: "child", Type: "address", Indexed: true},
	}}
	children, err := config.FactoryByParameter(created, config.DynamicFactoryParameter("child"),
		config.FactoryOptions{Address: factoryParent, StartBlock: 1, EndBlock: 2})
	if err != nil {
		t.Fatal(err)
	}
	args := []abi.Parameter{{Name: "value", Type: "uint256"}}
	childABI := []any{
		abi.Event{Type: "event", Name: "Ping", Inputs: args},
		abi.Function{Type: "function", Name: "record", Inputs: args, Outputs: args, StateMutability: "payable"},
	}
	cfg := map[string]any{
		"ordering": "omnichain",
		"chains": map[string]any{
			"one": map[string]any{"id": 101, "rpc": nodes[101].url, "reorgWindow": 0},
			"two": map[string]any{"id": 202, "rpc": nodes[202].url, "reorgWindow": 0, "cacheRpcRequests": false},
		},
		"blocks":   map[string]any{"Head": map[string]any{"chain": both, "startBlock": 1, "endBlock": 4, "interval": 1}},
		"accounts": map[string]any{"Account": map[string]any{"chain": both, "address": children, "startBlock": 1, "endBlock": 4}},
		"contracts": map[string]any{"Child": map[string]any{
			"chain": both, "address": children, "startBlock": 1, "endBlock": 4,
			"abi": childABI, "includeCallTraces": true,
		}},
	}
	callName := "Child.record()"
	if len(logChains) > 0 {
		contracts := cfg["contracts"].(map[string]any)
		child := contracts["Child"].(map[string]any)
		delete(child, "includeCallTraces")
		if len(logChains) == 2 {
			delete(cfg, "blocks")
			delete(cfg, "accounts")
			child["endBlock"] = 5
			return compileOmniLogsConfig(t, cfg, []config.SourceIndexingFunction{{Name: "Child:Ping", Fn: handler("log")}})
		}
		closing, sparse := "one", "two"
		if logChains[101] {
			closing, sparse = sparse, closing
		}
		child["chain"] = map[string]any{closing: map[string]any{}, sparse: map[string]any{"endBlock": 5}}
		cfg["blocks"].(map[string]any)["Head"].(map[string]any)["chain"] = closing
		cfg["accounts"].(map[string]any)["Account"].(map[string]any)["chain"] = closing
		trace := maps.Clone(child)
		trace["chain"], trace["includeCallTraces"] = closing, true
		contracts["ChildCalls"] = trace
		callName = "ChildCalls.record()"
	}
	return compileOmniLogsConfig(t, cfg, []config.SourceIndexingFunction{
		{Name: "Head:block", Fn: handler("block")},
		{Name: "Child:Ping", Fn: handler("log")},
		{Name: callName, Fn: handler("call")},
		{Name: "Account:transaction:to", Fn: handler("tx")},
		{Name: "Account:transfer:to", Fn: handler("transfer")},
	})
}

func TestRuntimeOmnichainFactory(t *testing.T) { testOmnichainFactory(t, nil) }
func TestRuntimeOmnichainLogFactory(t *testing.T) {
	testOmnichainFactory(t, map[uint64]bool{101: true, 202: true})
}

func TestRuntimeOmnichainMixedCallbacks(t *testing.T) {
	for _, id := range []uint64{101, 202} {
		t.Run(fmt.Sprintf("logs_on_%d", id), func(t *testing.T) {
			testOmnichainFactory(t, map[uint64]bool{id: true})
		})
	}
}

func testOmnichainFactory(t *testing.T, logChains map[uint64]bool) {
	one, two := []int{10, 40, 55, 70, 95}, []int{20, 30, 60, 75, 96}
	if len(logChains) == 1 {
		// The last two event blocks tie on timestamp. Chain 101 must commit first.
		two[3] = one[3]
	}
	if len(logChains) != 0 {
		// Put the empty fifth block on both chains below the global finalized timestamp.
		one, two = append(one, 110), append(two, 115)
	}
	nodes := map[uint64]omniFactoryNode{
		101: mineOmniFactory(t, 101, 2, factoryChild, factoryOther, one, logChains[101]),
		202: mineOmniFactory(t, 202, 1, factoryOther, factoryChild, two, logChains[202]),
	}
	var expected []string
	for _, pos := range [][2]int{{101, 1}, {202, 1}, {202, 2}, {101, 2}, {101, 3}, {202, 3}, {101, 4}, {202, 4}} {
		names := []string{"block"}
		if pos != [2]int{101, 1} {
			names = []string{"tx", "log", "call", "transfer", "block"}
		}
		if logChains[uint64(pos[0])] {
			if pos == [2]int{101, 1} || pos[1] == 3 {
				continue
			}
			names = []string{"log"}
		}
		for _, kind := range names {
			expected = append(expected, fmt.Sprintf("%d/%d/%s", pos[0], pos[1], kind))
		}
	}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			var calls []string
			fail := true
			handler := func(kind string) indexing.Handler {
				return func(ctx context.Context, a *indexing.HandlerArgs) error {
					id := finiteChain(a)
					n, err := continuousNumber(a)
					if err != nil {
						return err
					}
					key := fmt.Sprintf("%d/%d/%s", id, n, kind)
					if kind != "block" {
						hash, err := factoryTransactionProperty(a, "transaction", "hash")
						if err != nil || hash != nodes[id].txs[int(n)] {
							return fmt.Errorf("%s selected transaction %v: %v", key, hash, err)
						}
					}
					if kind == "call" && fmt.Sprint(a.Event.Get("result")) != fmt.Sprint(n) {
						return fmt.Errorf("%s unexpected CALL result %v", key, a.Event.Get("result"))
					}
					entry := store.Row{"id": "shared", "value": key}
					_, err = a.Context.DB.InsertOne(ctx, "account", entry, store.Conflict{
						Action: store.ConflictUpdate, Update: func(row store.Row) (store.Row, error) {
							return store.Row{"value": fmt.Sprint(row["value"]) + "|" + key}, nil
						}})
					if err != nil {
						return err
					}
					calls = append(calls, key)
					if fail && key == "202/4/log" {
						return errors.New("interrupt child write")
					}
					return nil
				}
			}
			b := omniFactoryBuild(t, nodes, handler, logChains)
			fixture := newManagedFiniteFixture(t, backend, b)
			omnichainGoldenStorage(&fixture)
			opts := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(fixture), BuildID: "omni-factory"}
			opts.Requesters = func(context.Context, uint64) ([]rpc.Requester, error) {
				var reqs []rpc.Requester
				for _, c := range b.Executable().Chains {
					client := rpc.New(nodes[runtimeChainID(c)].url)
					client.Retries = 0
					client.RequestsPerSecond = 0
					reqs = append(reqs, client)
				}
				return reqs, nil
			}
			opts.Historical.QuerySize = 1
			committed := 0
			failureIndex := slices.Index(expected, "202/4/log")
			for pass := 0; pass < 3; pass++ {
				opts.OpenID = fmt.Sprintf("owner-%d", pass)
				r := runtimeOpen(t, backend, opts)
				calls = nil
				out, err := r.Run(t.Context())
				if pass == 0 {
					if err == nil || !strings.Contains(err.Error(), "interrupt child write") || out.Ready || out.Completed || !slices.Equal(calls, expected[:failureIndex+1]) {
						t.Fatalf("interrupted result %+v, calls %v, error %v", out, calls, err)
					}
				} else if err != nil || !out.Ready || !out.Recovered || !out.Completed || !slices.Equal(calls, expected[committed:]) {
					t.Fatalf("reopen %d result %+v, calls %v want %v, error %v", pass, out, calls, expected[committed:], err)
				}
				if len(logChains) == 1 && pass > 0 {
					control, err := r.Control(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if !control.Serving || len(control.LogOnlyScanHeads) != 1 {
						t.Fatalf("unexpected mixed completion %+v", control)
					}
					for id := range nodes {
						if logChains[id] {
							cut, err := checkpoint.Decode(control.LogOnlyScanHeads[id].Checkpoint)
							if err != nil || cut.ChainID != id || cut.BlockNumber != 5 {
								t.Fatalf("chain %d missing end-of-range proof: %+v, %v", id, cut, err)
							}
						} else if !store.ManagedLiveReachedBlock(control, id, 4) {
							t.Fatalf("chain %d did not run its last closing callback", id)
						}
					}
				}
				if err := r.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				if pass == 0 {
					managedInspect(t, fixture, func(db store.Backend) {
						rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(fixture.options.Owned.Storage.BusinessNamespace)+".account")
						if len(rows) != 1 {
							t.Fatalf("expected committed cross-chain prefix, rows %v", rows)
						}
						prefix := strings.Split(fmt.Sprint(rows[0]["value"]), "|")
						committed = len(prefix)
						lastOnTwo := "202/2/block"
						if logChains[202] {
							lastOnTwo = "202/2/log"
						}
						if committed == 0 || committed > failureIndex || !slices.Equal(prefix, expected[:committed]) || !slices.Contains(prefix, lastOnTwo) {
							t.Fatalf("invalid committed prefix %v", prefix)
						}
					})
					fail = false
				} else {
					assertOmniLogsRows(t, fixture, omniLogsSnapshot{Rows: []map[string]any{{"id": "shared", "value": strings.Join(expected, "|")}}})
					committed = len(expected)
				}
			}
			cache := fixture
			cache.options.Owned.Storage.BusinessDirectory = fixture.options.Owned.Storage.SyncDirectory
			managedInspect(t, cache, func(db store.Backend) {
				table := schema.Quote(fixture.options.Owned.Storage.SyncNamespace+"_rpc_1") + ".factory_addresses"
				rows := managedRows(t, db, "SELECT chain_id::text,block_number::text,address FROM "+table)
				if len(rows) != 1 || fmt.Sprint(rows[0]["chain_id"]) != "101" || fmt.Sprint(rows[0]["block_number"]) != "2" ||
					!strings.EqualFold(fmt.Sprint(rows[0]["address"]), nodes[101].child) {
					t.Fatalf("unexpected cached births %v", rows)
				}
			})
		})
	}
}

func TestRuntimeOmnichainFactoryPreflight(t *testing.T) {
	for _, mode := range []string{"log-only-missing-opt-in", "missing-opt-in", "discovery-outside-closing-range"} {
		t.Run(mode, func(t *testing.T) {
			p := newContinuousRPC(t, 5)
			nodes := map[uint64]omniFactoryNode{101: {url: p.p.server.URL + "/1"}, 202: {url: p.p.server.URL + "/2"}}
			handler := func(string) indexing.Handler {
				return func(context.Context, *indexing.HandlerArgs) error { t.Fatal("preflight invoked handler"); return nil }
			}
			build := omniFactoryBuild(t, nodes, handler, nil)
			expected := "require opt-in"
			for i, group := range build.Executable().EventCallbacks {
				switch mode {
				case "log-only-missing-opt-in":
					build.Executable().EventCallbacks[i] = slices.DeleteFunc(slices.Clone(group), func(cb indexing.Object) bool {
						return cb["filter"].(map[string]any)["type"] != "log"
					})
				case "discovery-outside-closing-range":
					for _, cb := range group {
						f := cb["filter"].(map[string]any)
						if f["type"] == "log" {
							f["address"].(map[string]any)["toBlock"] = uint64(5)
						}
					}
					expected = "must cover every event callback range"
				}
			}
			opts := RuntimeOptions{Historical: Options{Build: build}, FactorySources: mode == "discovery-outside-closing-range", SourceCheckpoints: true, OpenID: "guard", BuildID: "guard"}
			opts.Requesters = func(context.Context, uint64) ([]rpc.Requester, error) {
				t.Fatal("preflight opened RPC")
				return nil, nil
			}
			r, err := openRuntime(t.Context(), opts, func(context.Context, store.IndependentStorageOptions, store.ManagedLiveRuntimeOptions) (*store.ManagedLiveDomains, error) {
				t.Fatal("preflight opened storage")
				return nil, nil
			})
			if r != nil || err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("unexpected preflight result %v, %v", r, err)
			}
		})
	}
}
