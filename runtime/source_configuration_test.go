package runtime

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Additional real EVM/backend proof: the source builder's actual clients,
// handlers, overloaded names, topics, receipts and callbacks drive the runtime.
func TestSourceConfigurationRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) {
				ctx := context.Background()
				client, url := localChain(t)
				backend := stagedBackend(t, ctx, dialect, "memory://")
				defer backend.Close()
				var accounts []string
				if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
					t.Fatal(err)
				}
				contract := "0x1000000000000000000000000000000000000001"
				overloaded := "0x1000000000000000000000000000000000000002"
				chosen := abi.Event{Type: "event", Name: "Chosen", Inputs: []abi.Parameter{{Name: "tag", Type: "bytes32", Indexed: true}, {Name: "amount", Type: "uint256"}}}
				empty := abi.Event{Type: "event", Name: "Value", Inputs: []abi.Parameter{}}
				tagged := abi.Event{Type: "event", Name: "Value", Inputs: []abi.Parameter{{Name: "tag", Type: "bytes32", Indexed: true}}}
				function := abi.Function{Type: "function", Name: "run", Inputs: []abi.Parameter{{Name: "amount", Type: "uint256"}, {Name: "tag", Type: "bytes32"}}, Outputs: []abi.Parameter{{Type: "uint256"}}, StateMutability: "nonpayable"}
				rpc := func(method string, args []any) {
					t.Helper()
					var value any
					if err := client.Call(ctx, method, args, &value); err != nil {
						t.Fatal(err)
					}
				}
				rpc("anvil_setCode", []any{contract, "0x6004356000526024357f" + abi.EventSelector(chosen)[2:] + "60206000a260043560010160005260206000f3"})
				rpc("anvil_setCode", []any{overloaded, "0x7f" + abi.EventSelector(empty)[2:] + "60006000a160017f" + abi.EventSelector(tagged)[2:] + "60006000a200"})
				send := func(address, data string) {
					t.Helper()
					var hash string
					if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": address, "value": "0x1", "gas": "0x493e0", "data": data}}, &hash); err != nil {
						t.Fatal(err)
					}
					deadline := time.Now().Add(5 * time.Second)
					for {
						var receipt map[string]any
						if err := client.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
							t.Fatal(err)
						}
						if receipt != nil {
							if receipt["status"] != "0x1" {
								t.Fatal(receipt)
							}
							return
						}
						if time.Now().After(deadline) {
							t.Fatal("unmined actual source fixture")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				send(contract, abi.FunctionSelector(function)+fmt.Sprintf("%064x%064x", 42, 1))
				send(contract, abi.FunctionSelector(function)+fmt.Sprintf("%064x%064x", 42, 2))
				send(overloaded, "0x")
				chosenJSON := `{"type":"event","name":"Chosen","anonymous":false,"inputs":[{"name":"tag","type":"bytes32","internalType":"bytes32","indexed":true},{"name":"amount","type":"uint256","indexed":false}]}`
				functionJSON := `{"type":"function","name":"run","stateMutability":"nonpayable","inputs":[{"name":"amount","type":"uint256"},{"name":"tag","type":"bytes32"}],"outputs":[{"type":"uint256"}]}`
				input := fmt.Sprintf(`{"chains":{"local":{"id":1,"rpc":%q,"cacheRpcRequests":%t}},"contracts":{"C":{"chain":"local","abi":[%s,%s],"address":%q,"startBlock":1,"includeCallTraces":true,"includeTransactionReceipts":true,"filter":{"event":"Chosen","args":{"tag":"0x%064x"}}},"O":{"chain":"local","abi":[{"type":"event","name":"Value","inputs":[]},{"type":"event","name":"Value","inputs":[{"type":"bytes32","name":"tag","indexed":true}]}],"address":%q,"startBlock":1}},"accounts":{"A":{"chain":"local","address":%q,"startBlock":1,"includeTransactionReceipts":true}},"blocks":{"B":{"chain":"local","interval":2,"startBlock":1}}}`, url, !disabled, chosenJSON, functionJSON, contract, 1, overloaded, strings.ToLower(accounts[0]))
				cfg, err := config.ParseSourceConfiguration([]byte(input))
				if err != nil {
					t.Fatal(err)
				}
				built, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 4})
				if err != nil {
					t.Fatal(err)
				}
				counts := map[string]int{}
				handler := Handler(func(ctx context.Context, c *Context) error {
					counts[c.Event.Name]++
					switch c.Event.Name {
					case "C:Chosen":
						args := c.Event.Args.(map[string]any)
						if args["amount"].(*big.Int).Int64() != 42 || args["tag"] != fmt.Sprintf("0x%064x", 1) {
							return fmt.Errorf("indexed filter or decoded ABI mismatch: %v", args)
						}
					case "C.run()":
						args := c.Event.Args.([]any)
						if len(args) != 2 || args[0].(*big.Int).Int64() != 42 || c.Event.Result.(*big.Int).Int64() != 43 {
							return fmt.Errorf("source trace ABI mismatch")
						}
					case "O:Value(bytes32 indexed tag)":
						if c.Event.Args.(map[string]any)["tag"] != fmt.Sprintf("0x%064x", 1) {
							return fmt.Errorf("overloaded value mismatch")
						}
					}
					if c.Event.Name == "C:Chosen" || c.Event.Name == "C.run()" || strings.HasPrefix(c.Event.Name, "A:") {
						if c.Event.Receipt == nil || c.Event.Receipt["status"] != "0x1" {
							return fmt.Errorf("missing requested source receipt for %s", c.Event.Name)
						}
					}
					id := c.Event.Name + ":" + c.Event.Block.Number
					if c.Event.Normalized != nil {
						id = c.Event.Name + ":" + c.Event.Normalized.Checkpoint
					}
					return c.DB.Write(ctx, "audit", store.Row{"id": id, "value": big.NewInt(1)}, store.Insert)
				})
				names := []string{"C:Chosen", "C.run()", "C:setup", "O:Value(bytes32 indexed tag)", "O:Value()", "A:transaction:from", "A:transfer:from", "B:block"}
				registrations := []config.SourceIndexingFunction{}
				for _, name := range names {
					registrations = append(registrations, config.SourceIndexingFunction{Name: name, Fn: handler})
				}
				indexing, err := config.BuildSourceIndexing(ctx, cfg, built, registrations)
				if err != nil {
					t.Fatal(err)
				}
				callbacks := indexing.EventCallbacks()[0]
				callbackNames := []string{}
				for _, callback := range callbacks {
					callbackNames = append(callbackNames, callback.Name)
				}
				expectedOrder := []string{"C:Chosen", "C.run()", "O:Value(bytes32 indexed tag)", "O:Value()", "A:transaction:from", "A:transfer:from", "B:block"}
				if !reflect.DeepEqual(callbackNames, expectedOrder) {
					t.Fatal("source callback registration order", callbackNames)
				}
				if !strings.Contains(string(callbacks[0].ABIItem), `"internalType":"bytes32"`) || !strings.Contains(string(callbacks[0].ABIItem), `"anonymous":false`) {
					t.Fatal("full source ABI metadata missing", string(callbacks[0].ABIItem))
				}
				narrowed, ok := callbacks[len(callbacks)-1].Filter.(config.SourceBlockFilter)
				if !ok || narrowed.Interval != 2 || narrowed.Offset != 1 {
					t.Fatal("actual filter union narrowing", narrowed)
				}
				model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "bigint", NotNull: true}}}}}
				db, err := store.NewLifecycle(backend, model, fmt.Sprintf("ponder_go_source_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				engine, err := NewFromSource(indexing, "local", db.RuntimeStore(), Options{DisableCache: disabled})
				if err != nil {
					t.Fatal(err)
				}
				engine.staged = &stagedState{db: db, buildID: "source-config-v1"}
				if engine.Client != built.RPCClients()[0] {
					t.Fatal("source RPC client identity was replaced")
				}
				engine.Client.RequestsPerSecond = 0
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				expectedCounts := map[string]int{"C:Chosen": 1, "C.run()": 2, "C:setup": 1, "O:Value(bytes32 indexed tag)": 1, "O:Value()": 1, "A:transaction:from": 3, "A:transfer:from": 3, "B:block": 2}
				if !reflect.DeepEqual(counts, expectedCounts) {
					t.Fatal("source dispatch/filter counts", counts, expectedCounts)
				}
				rows, err := db.RuntimeStore().Read(ctx, "audit")
				if err != nil || len(rows.Rows) != 14 {
					t.Fatal("source actual transactional writes", len(rows.Rows), err)
				}
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(counts, expectedCounts) {
					t.Fatal("source replay duplicated dispatch", counts)
				}
				state, err := db.RuntimeStore().Progress(ctx)
				if err != nil {
					t.Fatal(err)
				}
				progress, decodeErr := checkpoint.Decode(state["1"])
				if decodeErr != nil || progress.BlockNumber != 3 {
					t.Fatal("source complete block progress", state)
				}
			})
		}
	}
}
