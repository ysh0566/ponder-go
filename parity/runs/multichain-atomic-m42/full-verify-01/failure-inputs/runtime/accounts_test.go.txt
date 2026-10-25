package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Actual EVM + task database boundaries; these are supplementary native tests.
func TestAccountTransferRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) {
				ctx := context.Background()
				client, url := localChain(t)
				directory := t.TempDir() + "/pglite"
				backend := stagedBackend(t, ctx, dialect, directory)
				defer func() { backend.Close() }()
				var accounts []string
				if e := client.Call(ctx, "eth_accounts", []any{}, &accounts); e != nil {
					t.Fatal(e)
				}
				alice, bob := strings.ToLower(accounts[0]), strings.ToLower(accounts[1])
				wrapper := "0x1000000000000000000000000000000000000001"
				leaf := "0x1000000000000000000000000000000000000002"
				revert := "0x1000000000000000000000000000000000000003"
				nested := "0x1000000000000000000000000000000000000004"
				rpc := func(method string, args []any) {
					t.Helper()
					var result any
					if e := client.Call(ctx, method, args, &result); e != nil {
						t.Fatal(e)
					}
				}
				call := func(address string, value int, opcode string) string {
					return "600060006000600060" + fmt.Sprintf("%02x", value) + "73" + address[2:] + "5a" + opcode + "50"
				}
				rpc("anvil_setCode", []any{leaf, "0x00"})
				rpc("anvil_setCode", []any{revert, "0x60006000fd"})
				rpc("anvil_setCode", []any{nested, "0x" + call(bob, 1, "f1") + "00"})
				rpc("anvil_setBalance", []any{nested, "0x10"})
				delegate := "600060006000600073" + leaf[2:] + "5af450"
				rpc("anvil_setCode", []any{wrapper, "0x" + call(bob, 3, "f1") + call(revert, 2, "f1") + delegate + call(leaf, 1, "f2") + call(nested, 0, "f1") + "00"})
				send := func(to string, amount int, status string) {
					t.Helper()
					var hash string
					if e := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": alice, "to": to, "value": Hex(uint64(amount)), "gas": "0x493e0"}}, &hash); e != nil {
						t.Fatal(e)
					}
					deadline := time.Now().Add(5 * time.Second)
					for {
						var receipt map[string]any
						if e := client.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); e != nil {
							t.Fatal(e)
						}
						if receipt != nil {
							if receipt["status"] != status {
								t.Fatal("fixture EVM status", receipt, status)
							}
							return
						}
						if time.Now().After(deadline) {
							t.Fatal("unmined fixture")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				send(bob, 0, "0x1")
				send(revert, 1, "0x0")
				send(wrapper, 7, "0x1")
				model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "label", Type: "text", NotNull: true}, {Name: "value", Type: "bigint", NotNull: true}}}}}
				ns := fmt.Sprintf("ponder_go_account_%x", time.Now().UnixNano())
				d, e := store.NewLifecycle(backend, model, ns, schema.Multichain, store.LifecycleOptions{})
				if e != nil {
					t.Fatal(e)
				}
				defer func() { d.Close() }()
				cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{url}}}, Accounts: []config.AccountSource{{Name: "Alice", Chain: "local", Address: []string{alice}, StartBlock: 1}, {Name: "Bob", Chain: "local", Address: []string{bob}, StartBlock: 1}}}
				fail := true
				order := []string{}
				handler := func(ctx context.Context, c *Context) error {
					r := c.Event.Normalized
					if r == nil {
						return fmt.Errorf("normalized source event absent")
					}
					cp, err := checkpoint.Decode(r.Checkpoint)
					if err != nil {
						return err
					}
					value := new(big.Int)
					if c.Event.Transfer != nil {
						value.Set(c.Event.Transfer["value"].(*big.Int))
						if value.Sign() == 0 {
							return fmt.Errorf("zero native transfer dispatched")
						}
						if kind := r.Trace["type"]; kind == "DELEGATECALL" || kind == "CALLCODE" {
							return fmt.Errorf("non-value-moving trace dispatched")
						}
						if r.Trace["value"] == nil {
							return fmt.Errorf("trace value missing")
						}
					}
					if c.Event.Receipt["status"] != "0x1" {
						return fmt.Errorf("reverted transaction dispatched")
					}
					label := fmt.Sprintf("%d:%s:%d", cp.BlockNumber, c.Event.Name, cp.EventIndex)
					if err = c.DB.Write(ctx, "audit", store.Row{"id": c.Event.Name + ":" + r.Checkpoint, "label": label, "value": value}, store.Insert); err != nil {
						return err
					}
					order = append(order, label)
					if fail {
						return fmt.Errorf("native account handler fault")
					}
					return nil
				}
				handlers := map[string]Handler{"Alice:transaction:from": handler, "Alice:transfer:from": handler, "Bob:transfer:to": handler}
				engine, e := NewStaged(cfg, d, handlers, StagedOptions{Options: Options{DisableCache: disabled}, BuildID: "native-account"})
				if e != nil {
					t.Fatal(e)
				}
				engine.Client.RequestsPerSecond = 0
				engine.Client.Retries = 0
				if e = engine.Sync(ctx); e == nil || !strings.Contains(e.Error(), "native account handler fault") {
					t.Fatal("missing injected handler failure", e)
				}
				rows, e := d.RuntimeStore().Read(ctx, "audit")
				if e != nil || len(rows.Rows) != 0 {
					t.Fatal("failed account block leaked business writes", rows, e)
				}
				fail = false
				order = nil
				if e = engine.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				rows, e = d.RuntimeStore().Read(ctx, "audit")
				if e != nil || len(rows.Rows) != 5 {
					t.Fatal("zero/revert/nested/delegate/callcode result", rows, e)
				}
				expected := []string{"1:Alice:transaction:from:0", "3:Alice:transaction:from:0", "3:Alice:transfer:from:0", "3:Bob:transfer:to:1", "3:Bob:transfer:to:2"}
				if fmt.Sprint(order) != fmt.Sprint(expected) {
					t.Fatal("source checkpoint/matched trace order", order, expected)
				}
				var snapshot string
				if e = client.Call(ctx, "evm_snapshot", []any{}, &snapshot); e != nil {
					t.Fatal(e)
				}
				send(bob, 9, "0x1")
				if e = engine.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				rows, e = d.RuntimeStore().Read(ctx, "audit")
				if e != nil || len(rows.Rows) != 8 {
					t.Fatal(rows, e)
				}

				cacheNamespace := ""
				if engine.cache != nil {
					cacheNamespace = engine.cache.Namespace()
				}
				if e = d.Close(); e != nil {
					t.Fatal(e)
				}
				if e = backend.Close(); e != nil {
					t.Fatal(e)
				}
				backend = stagedBackend(t, ctx, dialect, directory)
				d, e = store.NewLifecycle(backend, model, ns, schema.Multichain, store.LifecycleOptions{})
				if e != nil {
					t.Fatal(e)
				}
				finalDisabled := !disabled
				engine, e = NewStaged(cfg, d, handlers, StagedOptions{Options: Options{DisableCache: finalDisabled}, BuildID: "native-account"})
				if e != nil {
					t.Fatal(e)
				}
				engine.Client.RequestsPerSecond = 0
				engine.Client.Retries = 0
				rpc("evm_revert", []any{snapshot})
				send(bob, 11, "0x1")
				if e = engine.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				rows, e = d.RuntimeStore().Read(ctx, "audit")
				if e != nil || len(rows.Rows) != 8 {
					t.Fatal("reorg duplicate/missing rows", rows, e)
				}
				n11 := 0
				for _, row := range rows.Rows {
					if fmt.Sprint(row["value"]) == "9" {
						t.Fatal("orphan business transfer survived")
					}
					if fmt.Sprint(row["value"]) == "11" {
						n11++
					}
				}
				if n11 != 2 {
					t.Fatal("replacement from/to transfers", rows)
				}
				before := len(order)
				if e = engine.Sync(ctx); e != nil || len(order) != before {
					t.Fatal("duplicate account replay", e, order)
				}
				if cacheNamespace != "" {
					r, err := backend.Query(ctx, "SELECT count(*)::text AS n FROM "+schema.Quote(cacheNamespace)+".traces WHERE chain_id=1 AND value=9")
					if err != nil || len(r.Rows) != 1 || r.Rows[0]["n"] != "0" {
						t.Fatal("cold-disabled orphan trace cache survived", r, err)
					}
				}
				if !finalDisabled {
					traces, e := engine.cache.Rows(ctx, "traces", 1, 4, 4)
					if e != nil || len(traces.Rows) != 1 {
						t.Fatal("trace normalized cache/reorg", traces, e)
					}
					var cached map[string]any
					if e = json.Unmarshal([]byte(fmt.Sprint(traces.Rows[0]["row"])), &cached); e != nil || fmt.Sprint(cached["value"]) != "11" {
						t.Fatal("cached replacement trace", cached, e)
					}
					receipts, e := engine.cache.Rows(ctx, "transaction_receipts", 1, 1, 4)
					if e != nil || len(receipts.Rows) != 4 {
						t.Fatal("receipt cache", receipts, e)
					}
				}
			})
		}
	}
}
