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
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Source factory identifiers retain undefined bounds, unlike legacy native
// defaults. The actual shared factory discovers log/account children together.
func TestSourceFactoryRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) {
				ctx := context.Background()
				client, url := localChain(t)
				backend := stagedBackend(t, ctx, dialect, "memory://")
				defer backend.Close()
				parent := "0x1000000000000000000000000000000000000001"
				child := "0x2000000000000000000000000000000000000001"
				created := abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{{Name: "child", Type: "address", Indexed: true}}}
				ping := abi.Event{Type: "event", Name: "Ping", Inputs: []abi.Parameter{}}
				rpc := func(method string, args ...any) {
					t.Helper()
					var out any
					if err := client.Call(ctx, method, args, &out); err != nil {
						t.Fatal(err)
					}
				}
				rpc("anvil_setCode", child, "0x7f"+abi.EventSelector(ping)[2:]+"60006000a100")
				rpc("anvil_setCode", parent, "0x73"+child[2:]+"7f"+abi.EventSelector(created)[2:]+"60006000a26000600060006000600573"+child[2:]+"5af15000")
				rpc("anvil_setBalance", parent, "0x100")
				var accounts []string
				if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
					t.Fatal(err)
				}
				send := func(to string, value int) {
					t.Helper()
					var hash string
					if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": to, "value": Hex(uint64(value)), "gas": "0x493e0"}}, &hash); err != nil {
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
							t.Fatal("unmined source factory fixture")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				send(child, 99)
				send(parent, 0)
				send(child, 7)
				address := fmt.Sprintf(`{"address":%q,"event":{"type":"event","name":"Created","inputs":[{"name":"child","type":"address","indexed":true}]},"parameter":"child"}`, parent)
				input := fmt.Sprintf(`{"chains":{"local":{"id":1,"rpc":%q,"cacheRpcRequests":%t}},"contracts":{"C":{"chain":"local","abi":[{"type":"event","name":"Ping","inputs":[]}],"address":%s}},"accounts":{"W":{"chain":"local","address":%s,"includeTransactionReceipts":true}}}`, url, !disabled, address, address)
				cfg, err := config.ParseSourceConfiguration([]byte(input))
				if err != nil {
					t.Fatal(err)
				}
				built, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{})
				if err != nil {
					t.Fatal(err)
				}
				calls := map[string]int{}
				sum := new(big.Int)
				handler := Handler(func(ctx context.Context, c *Context) error {
					calls[c.Event.Name]++
					if c.Event.Name == "W:transfer:to" {
						sum.Add(sum, c.Event.Transfer["value"].(*big.Int))
					}
					return c.DB.Write(ctx, "audit", store.Row{"id": c.Event.Name + ":" + c.Event.Normalized.Checkpoint, "value": big.NewInt(1)}, store.Insert)
				})
				registrations := []config.SourceIndexingFunction{}
				for _, name := range []string{"C:Ping", "W:transaction:to", "W:transfer:to"} {
					registrations = append(registrations, config.SourceIndexingFunction{Name: name, Fn: handler})
				}
				indexing, err := config.BuildSourceIndexing(ctx, cfg, built, registrations)
				if err != nil {
					t.Fatal(err)
				}
				native, err := indexing.NativeConfig("local")
				if err != nil {
					t.Fatal(err)
				}
				parameter := "child"
				expected, err := factory.Build(factory.Config{ChainID: 1, SourceID: "C", Address: parent, Event: created, Parameter: &parameter})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(expected.ID, "_undefined_undefined") || native.SourcePlan.Factories["C"].ID != expected.ID || native.SourcePlan.Factories["W"].ID != expected.ID {
					t.Fatal("source factory identity/default bounds changed", native.SourcePlan.Factories, expected)
				}
				model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "bigint", NotNull: true}}}}}
				db, err := store.NewLifecycle(backend, model, fmt.Sprintf("ponder_go_source_f_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				engine, err := NewFromSource(indexing, "local", db.RuntimeStore(), Options{DisableCache: disabled})
				if err != nil {
					t.Fatal(err)
				}
				engine.staged = &stagedState{db: db, buildID: "source-factory-v1"}
				engine.Client.RequestsPerSecond = 0
				if engine.factories["C"].ID != expected.ID || engine.factories["C"].FromBlock != nil {
					t.Fatal("native constructor changed source identity", engine.factories)
				}
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				wanted := map[string]int{"C:Ping": 2, "W:transaction:to": 1, "W:transfer:to": 2}
				if !reflect.DeepEqual(calls, wanted) || sum.Int64() != 12 {
					t.Fatal("factory birth predicate or same-block child dispatch", calls, sum)
				}
				children, err := engine.Store.FactoryChildren(ctx, expected.ID, 1, 3)
				if err != nil || children[child] != 2 {
					t.Fatal("durable source factory birth", children, err)
				}
				rows, err := engine.Store.Read(ctx, "audit")
				if err != nil || len(rows.Rows) != 5 {
					t.Fatal("source factory transactional writes", rows, err)
				}
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(calls, wanted) || sum.Int64() != 12 {
					t.Fatal("source factory replay duplicated", calls, sum)
				}
			})
		}
	}
}
