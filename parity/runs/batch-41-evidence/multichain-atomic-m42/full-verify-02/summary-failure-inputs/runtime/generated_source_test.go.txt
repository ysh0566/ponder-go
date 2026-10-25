package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	bindings "ponder.local/ponder-go/config/testdata/sourcebindings"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// The generated, closed chain/event references feed actual native handlers,
// indexed-topic filtering and persistent writes, beyond compile-only probes.
func TestGeneratedSourceRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) {
				ctx := context.Background()
				client, url := localChain(t)
				backend := stagedBackend(t, ctx, dialect, "memory://")
				defer backend.Close()
				emitter := "0x1000000000000000000000000000000000000001"
				event := abi.Event{Type: "event", Name: "Event0", Inputs: []abi.Parameter{{Name: "arg", Type: "bytes32", Indexed: true}}}
				var value any
				if err := client.Call(ctx, "anvil_setCode", []any{emitter, "0x6000357f" + abi.EventSelector(event)[2:] + "60006000a200"}, &value); err != nil {
					t.Fatal(err)
				}
				var accounts []string
				if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
					t.Fatal(err)
				}
				for _, tag := range []int{1, 2} {
					var hash string
					if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": emitter, "data": fmt.Sprintf("0x%064x", tag), "gas": "0x186a0"}}, &hash); err != nil {
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
							t.Fatal("unmined generated source fixture")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				base, err := config.ParseSourceConfiguration([]byte(fmt.Sprintf(`{"chains":{"mainnet":{"id":1,"rpc":%q,"cacheRpcRequests":%t}}}`, url, !disabled)))
				if err != nil {
					t.Fatal(err)
				}
				selected := fmt.Sprintf("0x%064x", 1)
				typed, err := bindings.Create(base, "a", bindings.Contract[config.SourceUndefinedBound, config.SourceUndefinedBound]{Chain: bindings.Chains.Mainnet, Address: config.SourceSome[any](emitter), Filter: &bindings.Filter{Event: bindings.Events.Event0, Args: map[string]any{"arg": selected}}})
				if err != nil {
					t.Fatal(err)
				}
				built, err := config.BuildTypedSourceConfig(typed, config.SourceBuildOptions{})
				if err != nil {
					t.Fatal(err)
				}
				model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "tag", Type: "text", NotNull: true}}}}}
				db, err := store.NewLifecycle(backend, model, fmt.Sprintf("ponder_go_refs_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				count := 0
				handler := Handler(func(ctx context.Context, c *Context) error {
					count++
					actual := c.Event.Args.(map[string]any)["arg"]
					if actual != selected {
						return fmt.Errorf("generated event/topic reference mismatch: %v", actual)
					}
					return c.DB.Write(ctx, "audit", store.Row{"id": c.Event.Normalized.Checkpoint, "tag": actual}, store.Insert)
				})
				indexing, err := config.SafeBuildTypedSourceIndexing(ctx, typed, built, []config.SourceIndexingFunction{{Name: "a:Event0", Fn: handler}})
				if err != nil {
					t.Fatal(err)
				}
				engine, err := NewFromSource(indexing, "mainnet", db.RuntimeStore(), Options{DisableCache: disabled})
				if err != nil {
					t.Fatal(err)
				}
				engine.staged = &stagedState{db: db, buildID: "generated-source-v1"}
				engine.Client.RequestsPerSecond = 0
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				rows, err := engine.Store.Read(ctx, "audit")
				if err != nil || len(rows.Rows) != 1 || count != 1 {
					t.Fatal("actual generated source filtering/writes", rows, count, err)
				}
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatal("generated source replay duplicated", count)
				}
			})
		}
	}
}
