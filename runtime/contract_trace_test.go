package runtime

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type traceDebugLog struct {
	sync.Mutex
	entries []protocol.Object
}

func (l *traceDebugLog) Debug(entry protocol.Object) {
	l.Lock()
	defer l.Unlock()
	l.entries = append(l.entries, entry)
}

// Supplementary actual EVM/backend validation, not original generated-ABI credit.
func TestContractTraceRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) {
				ctx := context.Background()
				client, url := localChain(t)
				backend := stagedBackend(t, ctx, dialect, "memory://")
				defer backend.Close()
				var accounts []string
				if e := client.Call(ctx, "eth_accounts", []any{}, &accounts); e != nil {
					t.Fatal(e)
				}
				alice, bob := strings.ToLower(accounts[0]), strings.ToLower(accounts[1])
				wrapper := "0x1000000000000000000000000000000000000001"
				nested := "0x1000000000000000000000000000000000000002"
				revert := "0x1000000000000000000000000000000000000003"
				leaf := "0x1000000000000000000000000000000000000004"
				rpc := func(method string, args []any) {
					t.Helper()
					var result any
					if e := client.Call(ctx, method, args, &result); e != nil {
						t.Fatal(e)
					}
				}
				call := func(address string, value int) string {
					return "600060006000600060" + fmt.Sprintf("%02x", value) + "73" + address[2:] + "5af150"
				}
				rpc("anvil_setCode", []any{nested, "0x" + call(bob, 2) + "00"})
				rpc("anvil_setBalance", []any{nested, "0x10"})
				rpc("anvil_setCode", []any{revert, "0x60006000fd"})
				rpc("anvil_setCode", []any{leaf, "0x00"})
				rpc("anvil_setBalance", []any{wrapper, "0x10"})
				rpc("anvil_setCode", []any{wrapper, "0x" + call(leaf, 0) + call(bob, 1) + call(revert, 1) + call(nested, 0) + "60043560010160005260206000f3"})
				fn := abi.Function{Type: "function", Name: "run", Inputs: []abi.Parameter{{Name: "amount", Type: "uint256"}}, Outputs: []abi.Parameter{{Name: "value", Type: "uint256"}}, StateMutability: "nonpayable"}
				data := abi.FunctionSelector(fn) + fmt.Sprintf("%064x", 42)
				var snapshot string
				if e := client.Call(ctx, "evm_snapshot", []any{}, &snapshot); e != nil {
					t.Fatal(e)
				}
				send := func() string {
					t.Helper()
					var hash string
					if e := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": alice, "to": wrapper, "value": "0x0", "gas": "0x493e0", "data": data, "nonce": "0x0", "gasPrice": "0x3b9aca00"}}, &hash); e != nil {
						t.Fatal(e)
					}
					deadline := time.Now().Add(5 * time.Second)
					for {
						var receipt map[string]any
						if e := client.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); e != nil {
							t.Fatal(e)
						}
						if receipt != nil {
							if receipt["status"] != "0x1" {
								t.Fatal(receipt)
							}
							return hash
						}
						if time.Now().After(deadline) {
							t.Fatal("unmined native trace fixture")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				oldHash := send()
				model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "bigint", NotNull: true}}}}}
				d, e := store.NewLifecycle(backend, model, fmt.Sprintf("ponder_go_call_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
				if e != nil {
					t.Fatal(e)
				}
				defer d.Close()
				abiJSON := `[{"type":"function","name":"run","stateMutability":"nonpayable","inputs":[{"name":"amount","type":"uint256"}],"outputs":[{"name":"value","type":"uint256"}]}]`
				cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{url}}}, Contracts: []config.Contract{{Name: "C", Chain: "local", ABI: []byte(abiJSON), Address: []string{wrapper}, StartBlock: 1, IncludeCallTraces: true, IncludeTransactionReceipts: true}}, Accounts: []config.AccountSource{{Name: "Bob", Chain: "local", Address: []string{bob}, StartBlock: 1}}}
				logs := &traceDebugLog{}
				fail := true
				order := []string{}
				handler := func(ctx context.Context, c *Context) error {
					raw := c.Event.Normalized
					if raw == nil {
						return fmt.Errorf("missing normalized call event")
					}
					cp, e := checkpoint.Decode(raw.Checkpoint)
					if e != nil {
						return e
					}
					value := new(big.Int)
					if c.Event.Name == "C.run()" {
						args, ok := c.Event.Args.([]any)
						if !ok || len(args) != 1 || args[0].(*big.Int).Int64() != 42 || c.Event.Result.(*big.Int).Int64() != 43 {
							return fmt.Errorf("contract arguments/results mismatch")
						}
						value.Set(c.Event.Result.(*big.Int))
						if c.Event.Receipt["transactionHash"] != oldHash || raw.TransactionReceipt["status"] != "success" {
							return fmt.Errorf("contract receipt mismatch")
						}
					} else {
						value.Set(c.Event.Transfer["value"].(*big.Int))
					}
					label := fmt.Sprintf("%s:%d", c.Event.Name, cp.EventIndex)
					order = append(order, label)
					if e = c.DB.Write(ctx, "audit", store.Row{"id": label, "value": value}, store.Insert); e != nil {
						return e
					}
					if fail {
						return fmt.Errorf("trace handler fault")
					}
					return nil
				}
				engine, e := NewStaged(cfg, d, map[string]Handler{"C.run()": handler, "Bob:transfer:to": handler}, StagedOptions{Options: Options{DisableCache: disabled, Logger: logs}, BuildID: "native-call"})
				if e != nil {
					t.Fatal(e)
				}
				engine.Client.RequestsPerSecond = 0
				engine.Client.Retries = 0
				if e = engine.Sync(ctx); e == nil || !strings.Contains(e.Error(), "trace handler fault") {
					t.Fatal(e)
				}
				rows, e := d.RuntimeStore().Read(ctx, "audit")
				if e != nil || len(rows.Rows) != 0 {
					t.Fatal("failed whole trace block leaked", rows, e)
				}
				fail = false
				order = nil
				if e = engine.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				rows, e = d.RuntimeStore().Read(ctx, "audit")
				if e != nil || len(rows.Rows) != 3 {
					t.Fatal(rows, e)
				}
				if fmt.Sprint(order) != "[C.run():0 Bob:transfer:to:1 Bob:transfer:to:2]" {
					t.Fatal("shared contract/account matched trace index", order)
				}
				rpc("evm_revert", []any{snapshot})
				rpc("anvil_setCode", []any{wrapper, "0x00"})
				if newHash := send(); newHash != oldHash {
					t.Fatal("fixture must reuse transaction hash across fork", oldHash, newHash)
				}
				if e = engine.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				rows, e = d.RuntimeStore().Read(ctx, "audit")
				if e != nil || len(rows.Rows) != 0 {
					t.Fatal("orphan trace business data survived", rows, e)
				}
				if len(logs.entries) != 2 || logs.entries[0]["msg"] != "Failed to decode matched call trace using provided ABI item" || logs.entries[1]["failure_count"] != 1 {
					t.Fatal("invalid output source failure/dedup summary", logs.entries)
				}
				if !disabled {
					r, e := backend.Query(ctx, "SELECT count(*)::text AS n FROM "+schema.Quote(engine.cache.Namespace())+".traces WHERE chain_id=1 AND block_number=1")
					if e != nil || r.Rows[0]["n"] != "1" {
						t.Fatal("same-hash reorg trace cache", r, e)
					}
				}
				if e = engine.Sync(ctx); e != nil || len(logs.entries) != 2 {
					t.Fatal("duplicate invalid-batch replay", e, logs.entries)
				}
			})
		}
	}
}
