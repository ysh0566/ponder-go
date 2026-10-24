package runtime

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Supplementary real typed source -> topics/bloom/RPC -> handler/backend proof.
// Prepare an old-code red on the undefined cases before applying the fix.
func TestIndexedTopicSourceRuntime(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, disabled := range []bool{false, true} {
			for _, mode := range []string{"missing", "null", "undefined", "matching"} {
				t.Run(fmt.Sprintf("%s/cache_disabled_%t/%s", dialect, disabled, mode), func(t *testing.T) {
					ctx := context.Background()
					client, endpoint := localChain(t)
					backend := stagedBackend(t, ctx, dialect, "memory://")
					defer backend.Close()
					emitter := "0x1000000000000000000000000000000000000001"
					event := abi.Event{Type: "event", Name: "Seen", Inputs: []abi.Parameter{{Name: "tag", Type: "bytes32", Indexed: true}, {Name: "amount", Type: "uint256"}}}
					var ignored any
					code := "0x6000356000526020357f" + abi.EventSelector(event)[2:] + "60206000a200"
					if err := client.Call(ctx, "anvil_setCode", []any{emitter, code}, &ignored); err != nil {
						t.Fatal(err)
					}
					var accounts []string
					if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
						t.Fatal(err)
					}
					for index, amount := range []int{42, 7} {
						var hash string
						data := fmt.Sprintf("0x%064x%064x", amount, index+1)
						if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": emitter, "data": data, "gas": "0x186a0"}}, &hash); err != nil {
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
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("indexed topic fixture was not mined")
							}
							time.Sleep(10 * time.Millisecond)
						}
					}
					base, err := config.ParseSourceConfiguration([]byte(fmt.Sprintf(`{"chains":{"local":{"id":1,"rpc":%q,"cacheRpcRequests":%t}}}`, endpoint, !disabled)))
					if err != nil {
						t.Fatal(err)
					}
					args := map[string]any{}
					switch mode {
					case "undefined":
						args["tag"] = filter.Undefined
					case "null":
						args["tag"] = nil
					case "matching":
						args["tag"] = fmt.Sprintf("0x%064x", 1)
					}
					rawABI := []byte(`[{"type":"event","name":"Seen","inputs":[{"name":"tag","type":"bytes32","indexed":true},{"name":"amount","type":"uint256","indexed":false}]}]`)
					typed := config.CreateTypedSourceConfiguration(base, "T", config.SourceTypedContract[int, config.SourceLatestBound]{Chain: config.SourceOnChain("local"), ABI: rawABI, Address: config.SourceSome[any](emitter), StartBlock: 1, EndBlock: config.SourceLatestBound{}, IncludeTransactionReceipts: config.SourceSome(true), Filter: config.SourceSome[any](map[string]any{"event": "Seen", "args": args})})
					built, err := config.BuildTypedSourceConfig(typed, config.SourceBuildOptions{})
					if err != nil {
						t.Fatal(err)
					}
					count, sum := 0, new(big.Int)
					handler := Handler(func(ctx context.Context, c *Context) error {
						values := c.Event.Args.(map[string]any)
						tag, amount := values["tag"].(string), values["amount"].(*big.Int)
						if tag != fmt.Sprintf("0x%064x", 1) && tag != fmt.Sprintf("0x%064x", 2) {
							return fmt.Errorf("decoded indexed tag mismatch: %v", values)
						}
						if c.Event.Receipt == nil {
							return fmt.Errorf("requested actual receipt missing")
						}
						count++
						sum.Add(sum, amount)
						return c.DB.Write(ctx, "audit", store.Row{"id": c.Event.Normalized.Checkpoint, "amount": amount}, store.Insert)
					})
					indexing, err := config.SafeBuildTypedSourceIndexing(ctx, typed, built, []config.SourceIndexingFunction{{Name: "T:Seen", Fn: handler}})
					if err != nil {
						t.Fatalf("source indexed argument %s: %v", mode, err)
					}
					actualFilter := indexing.EventCallbacks()[0][0].Filter.Value()
					if mode != "matching" && actualFilter["topic1"] != nil {
						t.Fatal("scalar nullish argument must compile to the source wildcard", actualFilter)
					}
					model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "amount", Type: "bigint", NotNull: true}}}}}
					db, err := store.NewLifecycle(backend, model, fmt.Sprintf("ponder_go_topic_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					engine, err := NewFromSource(indexing, "local", db.RuntimeStore(), Options{DisableCache: disabled})
					if err != nil {
						t.Fatal(err)
					}
					engine.staged = &stagedState{db: db, buildID: "indexed-topic-source-v1"}
					if engine.Client != built.RPCClients()[0] {
						t.Fatal("actual source client identity changed")
					}
					engine.Client.RequestsPerSecond = 0
					if err = engine.Sync(ctx); err != nil {
						t.Fatal(err)
					}
					wantCount, wantSum := 2, int64(49)
					if mode == "matching" {
						wantCount, wantSum = 1, 42
					}
					rows, err := engine.Store.Read(ctx, "audit")
					if err != nil || len(rows.Rows) != wantCount || count != wantCount || sum.Int64() != wantSum {
						t.Fatalf("source wildcard/filter dispatch rows=%+v count=%d sum=%v want=%d/%d error=%v", rows, count, sum, wantCount, wantSum, err)
					}
					if err = engine.Sync(ctx); err != nil || count != wantCount || sum.Int64() != wantSum {
						t.Fatalf("replay count=%d sum=%v error=%v", count, sum, err)
					}
				})
			}
		}
	}
}
