package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// This is supplementary EVM/backend coverage, separate from source test credit.
func TestContractLogAndTraceBatch(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, staged := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/staged_%t", dialect, staged), func(t *testing.T) {
				ctx := context.Background()
				client, endpoint := localChain(t)
				backend := stagedBackend(t, ctx, dialect, "memory://")
				defer backend.Close()
				model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "bigint", NotNull: true}}}}}
				lifecycle, err := store.NewLifecycle(backend, model, fmt.Sprintf("ponder_go_log_batch_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer lifecycle.Close()
				database := lifecycle.RuntimeStore()
				if !staged {
					database, err = store.New(ctx, backend, model, database.Namespace, schema.Multichain)
					if err != nil {
						t.Fatal(err)
					}
				}
				address := "0x1000000000000000000000000000000000000001"
				item := abi.Event{Type: "event", Name: "Value", Inputs: []abi.Parameter{{Name: "value", Type: "uint256"}}}
				fn := abi.Function{Type: "function", Name: "run", Outputs: []abi.Parameter{{Name: "value", Type: "uint256"}}, StateMutability: "nonpayable"}
				abiJSON, err := json.Marshal([]any{item, fn})
				if err != nil {
					t.Fatal(err)
				}
				rpc := func(method string, params ...any) {
					t.Helper()
					var result any
					if err := client.Call(ctx, method, params, &result); err != nil {
						t.Fatal(err)
					}
				}
				topic := abi.EventSelector(item)[2:]
				invalidLog := "7f" + topic + "60006000a1"
				validLog := func(value int) string { return fmt.Sprintf("60%02x6000527f%s60206000a1", value, topic) }
				rpc("anvil_setCode", address, "0x"+invalidLog+invalidLog+validLog(9)+"00")
				var accounts []string
				if err = client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
					t.Fatal(err)
				}
				var snapshot string
				if err = client.Call(ctx, "evm_snapshot", []any{}, &snapshot); err != nil {
					t.Fatal(err)
				}
				send := func() string {
					t.Helper()
					var hash string
					if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": address, "data": abi.FunctionSelector(fn), "gas": "0x493e0", "nonce": "0x0", "gasPrice": "0x3b9aca00"}}, &hash); err != nil {
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
								t.Fatal("fixture transaction reverted", receipt)
							}
							return hash
						}
						if time.Now().After(deadline) {
							t.Fatal("fixture transaction was not mined")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				hash := send()
				cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{endpoint}}}, Contracts: []config.Contract{{Name: "C", Chain: "local", Address: []string{address}, ABI: abiJSON, StartBlock: 0, IncludeCallTraces: true, IncludeTransactionReceipts: true}}}
				logger := &traceDebugLog{}
				fail := true
				order := []string{}
				handler := func(ctx context.Context, c *Context) error {
					order = append(order, c.Event.Name)
					if c.Event.Normalized == nil || c.Event.Transaction["hash"] != hash || c.Event.Receipt["transactionHash"] != hash || c.Event.Normalized.TransactionReceipt["status"] != "success" {
						return fmt.Errorf("missing normalized event or receipt provenance")
					}
					var value *big.Int
					if c.Event.Name == "C:Value" {
						value = c.Event.Args.(map[string]any)["value"].(*big.Int)
						if c.Event.Log == nil || c.Event.Normalized.Log["address"] != address {
							return fmt.Errorf("missing original and normalized log")
						}
					} else {
						value = c.Event.Result.(*big.Int)
					}
					if err := c.DB.Write(ctx, "audit", store.Row{"id": c.Event.Name, "value": value}, store.Insert); err != nil {
						return err
					}
					if fail {
						return fmt.Errorf("valid log handler fault")
					}
					return nil
				}
				handlers := map[string]Handler{"C:Value": handler, "C.run()": handler}
				var engine *Engine
				if staged {
					engine, err = NewStaged(cfg, lifecycle, handlers, StagedOptions{Options: Options{Logger: logger}, BuildID: "native-log-batch"})
				} else {
					engine, err = NewWithOptions(cfg, database, handlers, Options{Logger: logger})
				}
				if err != nil {
					t.Fatal(err)
				}
				engine.Client.RequestsPerSecond, engine.Client.Retries = 0, 0
				if err = engine.Sync(ctx); err == nil || !strings.Contains(err.Error(), "valid log handler fault") {
					t.Fatal("invalid matched logs must be omitted before business dispatch", err)
				}
				rows, err := database.Read(ctx, "audit")
				if err != nil || len(rows.Rows) != 0 {
					t.Fatal("handler failure leaked writes", rows, err)
				}
				if len(logger.entries) != 4 || logger.entries[0]["msg"] != "Failed to decode matched event log using provided ABI item" || logger.entries[1]["msg"] != "Failed to decode matched call trace using provided ABI item" || logger.entries[2]["failure_count"] != 2 || logger.entries[2]["success_count"] != 1 || logger.entries[3]["failure_count"] != 1 || logger.entries[3]["success_count"] != 0 {
					t.Fatal("shared batch selector dedup, order and summaries", logger.entries)
				}
				fail = false
				order = nil
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				rows, err = database.Read(ctx, "audit")
				if err != nil || len(rows.Rows) != 1 || fmt.Sprint(rows.Rows[0]["value"]) != "9" || fmt.Sprint(order) != "[C:Value]" || len(logger.entries) != 8 {
					t.Fatal("valid event after malformed logs or replay", rows, order, logger.entries, err)
				}
				if err = engine.Sync(ctx); err != nil || len(logger.entries) != 8 {
					t.Fatal("completed malformed batch replayed", err, logger.entries)
				}
				rpc("evm_revert", snapshot)
				rpc("anvil_setCode", address, "0x"+validLog(17)+"602b60005260206000f3")
				if replacement := send(); replacement != hash {
					t.Fatal("same transaction hash required for cache reorg regression", hash, replacement)
				}
				order = nil
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				rows, err = database.Read(ctx, "audit")
				if err != nil || len(rows.Rows) != 2 || fmt.Sprint(order) != "[C:Value C.run()]" || len(logger.entries) != 8 {
					t.Fatal("replacement log/trace dispatch", rows, order, logger.entries, err)
				}
				values := map[string]string{}
				for _, row := range rows.Rows {
					values[fmt.Sprint(row["id"])] = fmt.Sprint(row["value"])
				}
				if values["C:Value"] != "17" || values["C.run()"] != "43" {
					t.Fatal("orphan decoded data survived reorg", values)
				}
			})
		}
	}
}
