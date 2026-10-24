package runtime

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Supplementary actual EVM proof for the production typed constructor and both
// typed consumers. Numeric/tag/undefined/NaN values reach the native scheduler.
func TestTypedSourceRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			for _, bounds := range []string{"numeric_latest", "nan_undefined"} {
				t.Run(fmt.Sprintf("%s/cache_disabled_%t/%s", dialect, disabled, bounds), func(t *testing.T) {
					ctx := context.Background()
					client, url := localChain(t)
					backend := stagedBackend(t, ctx, dialect, "memory://")
					defer backend.Close()
					emitter := "0x1000000000000000000000000000000000000001"
					event := abi.Event{Type: "event", Name: "Seen", Inputs: []abi.Parameter{{Name: "amount", Type: "uint256"}}}
					var value any
					if err := client.Call(ctx, "anvil_setCode", []any{emitter, "0x6000356000527f" + abi.EventSelector(event)[2:] + "60206000a100"}, &value); err != nil {
						t.Fatal(err)
					}
					var accounts []string
					if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
						t.Fatal(err)
					}
					for _, amount := range []int{42, 7} {
						var hash string
						if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": emitter, "data": fmt.Sprintf("0x%064x", amount), "gas": "0x186a0"}}, &hash); err != nil {
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
								t.Fatal("typed actual fixture was not mined")
							}
							time.Sleep(10 * time.Millisecond)
						}
					}
					base, err := config.ParseSourceConfiguration([]byte(fmt.Sprintf(`{"chains":{"local":{"id":1,"rpc":%q,"cacheRpcRequests":%t}}}`, url, !disabled)))
					if err != nil {
						t.Fatal(err)
					}
					rawABI := []byte(`[{"type":"event","name":"Seen","inputs":[{"name":"amount","type":"uint256","internalType":"uint256","indexed":false}]}]`)
					model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "amount", Type: "bigint", NotNull: true}}}}}
					db, err := store.NewLifecycle(backend, model, fmt.Sprintf("ponder_go_typed_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					count := 0
					sum := new(big.Int)
					handler := Handler(func(ctx context.Context, c *Context) error {
						count++
						amount := c.Event.Args.(map[string]any)["amount"].(*big.Int)
						sum.Add(sum, amount)
						if c.Event.Receipt == nil || !strings.EqualFold(c.Event.Log.Address, emitter) {
							return fmt.Errorf("typed handler receipt/address missing")
						}
						return c.DB.Write(ctx, "audit", store.Row{"id": c.Event.Normalized.Checkpoint, "amount": amount}, store.Insert)
					})
					functions := []config.SourceIndexingFunction{{Name: "T:Seen", Fn: handler}}
					var built *config.SourceConfigBuild
					var indexing *config.SourceIndexingBuild
					if bounds == "numeric_latest" {
						typed := config.CreateTypedSourceConfiguration(base, "T", config.SourceTypedContract[int, config.SourceLatestBound]{Chain: config.SourceOnChain("local"), ABI: rawABI, Address: config.SourceSome[any]([]string{emitter}), StartBlock: 1, EndBlock: config.SourceLatestBound{}, IncludeTransactionReceipts: config.SourceSome(true)})
						built, err = config.BuildTypedSourceConfig(typed, config.SourceBuildOptions{})
						if err != nil {
							t.Fatal(err)
						}
						indexing, err = config.SafeBuildTypedSourceIndexing(ctx, typed, built, functions)
						if err != nil {
							t.Fatal(err)
						}
						f := indexing.EventCallbacks()[0][0].Filter.Value()
						if f["fromBlock"] != float64(1) || f["toBlock"] != float64(2) {
							t.Fatal("typed numeric/latest boundaries", f)
						}
					} else {
						typed := config.CreateTypedSourceConfiguration(base, "T", config.SourceTypedContract[float64, config.SourceUndefinedBound]{Chain: config.SourceOnChains(config.SourceChainOverride{Name: "local"}), ABI: rawABI, Address: config.SourceSome[any]([]string{emitter}), StartBlock: math.NaN(), IncludeTransactionReceipts: config.SourceSome(true)})
						built, err = config.BuildTypedSourceConfig(typed, config.SourceBuildOptions{})
						if err != nil {
							t.Fatal(err)
						}
						indexing, err = config.SafeBuildTypedSourceIndexing(ctx, typed, built, functions)
						if err != nil {
							t.Fatal(err)
						}
						f := indexing.EventCallbacks()[0][0].Filter.Value()
						if !filter.IsUndefined(f["fromBlock"]) || !filter.IsUndefined(f["toBlock"]) {
							t.Fatal("typed NaN/undefined lost by conversion", f)
						}
					}
					engine, err := NewFromSource(indexing, "local", db.RuntimeStore(), Options{DisableCache: disabled})
					if err != nil {
						t.Fatal(err)
					}
					engine.staged = &stagedState{db: db, buildID: "typed-source-v1"}
					if engine.Client != built.RPCClients()[0] {
						t.Fatal("typed pipeline native client replaced")
					}
					engine.Client.RequestsPerSecond = 0
					if err = engine.Sync(ctx); err != nil {
						t.Fatal(err)
					}
					rows, err := engine.Store.Read(ctx, "audit")
					if err != nil || len(rows.Rows) != 2 || count != 2 || sum.Int64() != 49 {
						t.Fatal("actual typed source dispatch/data", rows, count, sum, err)
					}
					if err = engine.Sync(ctx); err != nil {
						t.Fatal(err)
					}
					if count != 2 || sum.Int64() != 49 {
						t.Fatal("typed source replay duplicated", count, sum)
					}
				})
			}
		}
	}
}
