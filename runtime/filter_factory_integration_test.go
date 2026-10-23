package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Supplementary actual EVM factory/birth-block/reorg/cache boundary evidence.
// No replacement for the original missing generated ABI/deployment fixtures.
func TestFilterFactoryBirthAndReorg(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) {
				ctx := context.Background()
				client, endpoint := localChain(t)
				backend := stagedBackend(t, ctx, dialect, "memory://")
				defer backend.Close()
				root := "0x1000000000000000000000000000000000000001"
				a := "0x2000000000000000000000000000000000000001"
				b := "0x3000000000000000000000000000000000000001"
				created := abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{{Name: "child", Type: "address"}}}
				selector := strings.TrimPrefix(abi.EventSelector(created), "0x")
				word := func(a string) string { return strings.Repeat("0", 24) + a[2:] }
				transfer := "0x6000356000527f" + strings.Repeat("0", 64) + "7f" + strings.Repeat("0", 64) + "7fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef60206000a300"
				setCode := func(address, code string) {
					t.Helper()
					var result any
					if err := client.Call(ctx, "anvil_setCode", []any{address, code}, &result); err != nil {
						t.Fatal(err)
					}
				}
				setCode(a, transfer)
				setCode(b, transfer)
				setParent := func(child string) {
					// LOG1 Created(child), then CALL child with uint2565 in the same tx.
					setCode(root, "0x7f"+word(child)+"6000527f"+selector+"60206000a160056000526000600060206000600073"+child[2:]+"5af15000")
				}
				var accounts []string
				if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
					t.Fatal(err)
				}
				send := func(to string, amount int) {
					t.Helper()
					var hash string
					if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": to, "gas": "0x493e0", "data": fmt.Sprintf("0x%064x", amount)}}, &hash); err != nil {
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
							t.Fatal("unmined native fixture")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				send(a, 99) // Before factory discovery: must not dispatch.
				var snapshot string
				if err := client.Call(ctx, "evm_snapshot", []any{}, &snapshot); err != nil {
					t.Fatal(err)
				}
				setParent(a)
				send(root, 0)
				parameter := "child"
				cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{endpoint}}}, Contracts: []config.Contract{{Name: "Token", Chain: "local", ABI: []byte(transferABI), Factory: &factory.Config{Address: root, Event: created, Parameter: &parameter}}}}
				d, err := store.NewLifecycle(backend, stagedBlockModel(), fmt.Sprintf("ponder_go_birth_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close()
				count := 0
				handlers := map[string]Handler{"Token:Transfer": func(ctx context.Context, c *Context) error {
					count++
					return c.DB.Write(ctx, "account", store.Row{"id": c.Event.Log.Address, "balance": fmt.Sprint(c.Event.Args.(map[string]any)["value"])}, store.Insert)
				}}
				engine, err := NewStaged(cfg, d, handlers, StagedOptions{Options: Options{DisableCache: disabled}, BuildID: "native-filter-factory"})
				if err != nil {
					t.Fatal(err)
				}
				engine.Client.RequestsPerSecond = 0
				check := func(address string, wantCalls int) {
					t.Helper()
					rows, err := d.RuntimeStore().Read(ctx, "account")
					if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["id"] != address || fmt.Sprint(rows.Rows[0]["balance"]) != "5" || count != wantCalls {
						t.Fatal("birth/block/fork dispatch", rows, count, err)
					}
					f := engine.factories["Token"]
					entries, err := d.RuntimeStore().FactoryChildren(ctx, f.ID, 1, 2)
					if err != nil || len(entries) != 1 || entries[address] != 2 {
						t.Fatal("durable birth boundary", entries, err)
					}
					before, err := d.RuntimeStore().FactoryChildren(ctx, f.ID, 1, 1)
					if err != nil || len(before) != 0 {
						t.Fatal("future child visible", before, err)
					}
				}
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				check(a, 1)
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				check(a, 1)
				var reverted bool
				if err = client.Call(ctx, "evm_revert", []any{snapshot}, &reverted); err != nil || !reverted {
					t.Fatal(reverted, err)
				}
				setParent(b)
				send(root, 0)
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				check(b, 2)
				if !disabled {
					rows, err := engine.cache.Rows(ctx, "logs", 1, 2, 2)
					if err != nil || len(rows.Rows) != 2 {
						t.Fatal("cached actual factory and child logs", rows, err)
					}
					for _, row := range rows.Rows {
						if row["address"] == a {
							t.Fatal("orphan cached child survived", row)
						}
					}
				}
			})
		}
	}
}
