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
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Real EVM/backend factory-account boundaries. This supplementary integration
// test does not add any original source record.
func TestAccountFactoryRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, staged := range []bool{false, true} {
			for _, disabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/staged_%t/cache_disabled_%t", dialect, staged, disabled), func(t *testing.T) {
					ctx := context.Background()
					client, endpoint := localChain(t)
					directory := t.TempDir() + "/pglite"
					backend := stagedBackend(t, ctx, dialect, directory)
					defer func() { backend.Close() }()
					parent := "0x1000000000000000000000000000000000000001"
					childA := "0x2000000000000000000000000000000000000001"
					childB := "0x3000000000000000000000000000000000000001"
					created := abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{{Name: "child", Type: "address"}}}
					rpc := func(method string, args ...any) {
						t.Helper()
						var result any
						if err := client.Call(ctx, method, args, &result); err != nil {
							t.Fatal(err)
						}
					}
					rpc("anvil_setBalance", parent, "0x100")
					var accounts []string
					if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
						t.Fatal(err)
					}
					sendFrom := func(sender, to string, value, nonce uint64) string {
						t.Helper()
						var hash string
						if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": sender, "to": to, "value": Hex(value), "nonce": Hex(nonce), "gas": "0x493e0", "gasPrice": "0x3b9aca00"}}, &hash); err != nil {
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
								return hash
							}
							if time.Now().After(deadline) {
								t.Fatal("unmined fixture")
							}
							time.Sleep(10 * time.Millisecond)
						}
					}
					send := func(to string, value, nonce uint64) string { return sendFrom(accounts[0], to, value, nonce) }
					setParent := func(child string, value int) {
						t.Helper()
						word := strings.Repeat("0", 24) + child[2:]
						topic := abi.EventSelector(created)[2:]
						code := "0x7f" + word + "6000527f" + topic + "60206000a1" + fmt.Sprintf("600060006000600060%02x73%s5af15000", value, child[2:])
						rpc("anvil_setCode", parent, code)
					}
					send(childA, 99, 0) // block1, before discovery; must be excluded.
					var snapshot string
					if err := client.Call(ctx, "evm_snapshot", []any{}, &snapshot); err != nil {
						t.Fatal(err)
					}
					model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "kind", Type: "text", NotNull: true}, {Name: "destination", Type: "text", NotNull: true}, {Name: "amount", Type: "bigint", NotNull: true}}}}}
					namespace := fmt.Sprintf("ponder_go_account_factory_%x", time.Now().UnixNano())
					lifecycle, err := store.NewLifecycle(backend, model, namespace, schema.Multichain, store.LifecycleOptions{})
					if err != nil {
						t.Fatal(err)
					}
					defer func() { lifecycle.Close() }()
					database := lifecycle.RuntimeStore()
					if !staged {
						database, err = store.New(ctx, backend, model, database.Namespace, schema.Multichain)
						if err != nil {
							t.Fatal(err)
						}
					}
					parameter := "child"
					from := uint64(0)
					cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{endpoint}}}, Accounts: []config.AccountSource{{Name: "Wallet", Chain: "local", Factory: &factory.Config{Address: parent, Event: created, Parameter: &parameter, FromBlock: &from}}}}
					fail := true
					calls := 0
					handler := func(ctx context.Context, c *Context) error {
						calls++
						if c.Event.Normalized == nil || c.Event.Receipt["transactionHash"] != c.Event.Transaction["hash"] {
							return fmt.Errorf("missing raw/normalized receipt provenance")
						}
						var amount *big.Int
						var destination string
						if strings.Contains(c.Event.Name, ":transaction:") {
							amount = c.Event.Normalized.Transaction["value"].(*big.Int)
							destination = c.Event.Transaction["to"].(string)
							if c.Event.Normalized.TransactionReceipt["status"] != "success" {
								return fmt.Errorf("missing normalized transaction receipt")
							}
						} else {
							amount = c.Event.Transfer["value"].(*big.Int)
							destination = c.Event.Transfer["to"].(string)
						}
						if err := c.DB.Write(ctx, "audit", store.Row{"id": c.Event.Normalized.Checkpoint + ":" + c.Event.Name, "kind": c.Event.Name, "destination": destination, "amount": amount}, store.Insert); err != nil {
							return err
						}
						if fail && c.Event.Name == "Wallet:transfer:to" && c.Event.Normalized.Block["number"].(*big.Int).Uint64() == 3 {
							return fmt.Errorf("post-birth account handler fault")
						}
						return nil
					}
					handlers := map[string]Handler{"Wallet:transaction:to": handler, "Wallet:transfer:to": handler, "Wallet:transaction:from": handler, "Wallet:transfer:from": handler}
					var engine *Engine
					if staged {
						engine, err = NewStaged(cfg, lifecycle, handlers, StagedOptions{Options: Options{DisableCache: disabled}, BuildID: "native-account-factory"})
					} else {
						engine, err = NewWithOptions(cfg, database, handlers, Options{DisableCache: disabled})
					}
					if err != nil {
						t.Fatal(err)
					}
					engine.Client.RequestsPerSecond, engine.Client.Retries = 0, 0
					if err := engine.Sync(ctx); err != nil {
						t.Fatal(err)
					}
					rows, err := database.Read(ctx, "audit")
					if err != nil || len(rows.Rows) != 0 || calls != 0 {
						t.Fatal("pre-birth event leaked", rows, calls, err)
					}
					setParent(childA, 5)
					parentHash := send(parent, 0, 1)
					send(childA, 7, 2)
					if err := engine.Sync(ctx); err == nil || !strings.Contains(err.Error(), "post-birth account handler fault") {
						t.Fatal("expected original handler failure", err)
					}
					rows, err = database.Read(ctx, "audit")
					// The legacy constructor commits each event atomically; staged
					// commits the full block. Preserve both existing boundaries.
					wantRows := 2
					if staged {
						wantRows = 1
					}
					if err != nil || len(rows.Rows) != wantRows {
						t.Fatal("failed handler transaction boundary", rows, err)
					}
					birthRows, earlierTx := 0, 0
					for _, row := range rows.Rows {
						if fmt.Sprint(row["amount"]) == "5" && row["kind"] == "Wallet:transfer:to" {
							birthRows++
						} else if !staged && fmt.Sprint(row["amount"]) == "7" && row["kind"] == "Wallet:transaction:to" {
							earlierTx++
						} else {
							t.Fatal("failed transfer write escaped rollback", row)
						}
					}
					if birthRows != 1 || (!staged && earlierTx != 1) {
						t.Fatal("committed event provenance", rows)
					}
					fail = false
					if err := engine.Sync(ctx); err != nil {
						t.Fatal(err)
					}
					check := func(destination string, birth, post int) {
						t.Helper()
						rows, err := database.Read(ctx, "audit")
						if err != nil || len(rows.Rows) != 3 {
							t.Fatal(rows, err)
						}
						amounts := map[string]int{}
						for _, row := range rows.Rows {
							if row["destination"] != destination {
								t.Fatal("orphan/different child", row)
							}
							amounts[fmt.Sprint(row["amount"])]++
						}
						if amounts[fmt.Sprint(birth)] != 1 || amounts[fmt.Sprint(post)] != 2 {
							t.Fatal("birth-block and post-birth values", amounts)
						}
						f := engine.factories["Wallet"]
						children, err := database.FactoryChildren(ctx, f.ID, 1, 3)
						if err != nil || len(children) != 1 || children[destination] != 2 {
							t.Fatal("durable factory birth", children, err)
						}
						before, err := database.FactoryChildren(ctx, f.ID, 1, 1)
						if err != nil || len(before) != 0 {
							t.Fatal("future child leaked", before, err)
						}
					}
					check(childA, 5, 7)
					beforeCalls := calls
					if err := engine.Sync(ctx); err != nil || calls != beforeCalls {
						t.Fatal("completed blocks replayed", calls, err)
					}
					cacheNamespace := ""
					if engine.cache != nil {
						cacheNamespace = engine.cache.Namespace()
					}
					if err := lifecycle.Close(); err != nil {
						t.Fatal(err)
					}
					if err := backend.Close(); err != nil {
						t.Fatal(err)
					}
					backend = stagedBackend(t, ctx, dialect, directory)
					lifecycle, err = store.NewLifecycle(backend, model, namespace, schema.Multichain, store.LifecycleOptions{})
					if err != nil {
						t.Fatal(err)
					}
					database = lifecycle.RuntimeStore()
					finalDisabled := !disabled
					if staged {
						engine, err = NewStaged(cfg, lifecycle, handlers, StagedOptions{Options: Options{DisableCache: finalDisabled}, BuildID: "native-account-factory"})
					} else {
						database, err = store.New(ctx, backend, model, namespace, schema.Multichain)
						if err == nil {
							engine, err = NewWithOptions(cfg, database, handlers, Options{DisableCache: finalDisabled})
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					engine.Client.RequestsPerSecond, engine.Client.Retries = 0, 0
					wantRecoveryCalls := beforeCalls
					if staged {
						wantRecoveryCalls += 3
					} // Source recovery replays the live suffix from safe.
					if err := engine.Sync(ctx); err != nil || calls != wantRecoveryCalls {
						t.Fatal("cold recovery event replay", calls, wantRecoveryCalls, err)
					}
					check(childA, 5, 7)
					rpc("evm_revert", snapshot)
					setParent(childB, 6)
					if replacement := send(parent, 0, 1); replacement != parentHash {
						t.Fatal("same parent tx hash required", parentHash, replacement)
					}
					send(childB, 11, 2)
					if err := engine.Sync(ctx); err != nil {
						t.Fatal(err)
					}
					check(childB, 6, 11)
					// Discovery must run before the account's business start.
					lateConfig := cfg
					lateConfig.Accounts = append([]config.AccountSource(nil), cfg.Accounts...)
					lateConfig.Accounts[0].StartBlock = 3
					lateLifecycle, err := store.NewLifecycle(backend, model, fmt.Sprintf("ponder_go_af_late_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
					if err != nil {
						t.Fatal(err)
					}
					defer lateLifecycle.Close()
					lateStore := lateLifecycle.RuntimeStore()
					var lateEngine *Engine
					if staged {
						lateEngine, err = NewStaged(lateConfig, lateLifecycle, handlers, StagedOptions{Options: Options{DisableCache: disabled}, BuildID: "native-account-factory-late"})
					} else {
						lateStore, err = store.New(ctx, backend, model, lateStore.Namespace, schema.Multichain)
						if err == nil {
							lateEngine, err = NewWithOptions(lateConfig, lateStore, handlers, Options{DisableCache: disabled})
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					lateEngine.Client.RequestsPerSecond, lateEngine.Client.Retries = 0, 0
					if err := lateEngine.Sync(ctx); err != nil {
						t.Fatal(err)
					}
					lateRows, err := lateStore.Read(ctx, "audit")
					if err != nil || len(lateRows.Rows) != 2 {
						t.Fatal("factory history before account start was missed", lateRows, err)
					}
					for _, row := range lateRows.Rows {
						if row["destination"] != childB || fmt.Sprint(row["amount"]) != "11" {
							t.Fatal("pre-start business event leaked", row)
						}
					}
					// Exercise factory matching on the outgoing direction as well.
					rpc("anvil_impersonateAccount", childB)
					rpc("anvil_setBalance", childB, "0x1000000000000000000")
					sendFrom(childB, accounts[1], 2, 0)
					if err := engine.Sync(ctx); err != nil {
						t.Fatal(err)
					}
					outgoing, err := database.Read(ctx, "audit")
					if err != nil || len(outgoing.Rows) != 5 {
						t.Fatal("factory outgoing rows", outgoing, err)
					}
					kinds := map[string]int{}
					for _, row := range outgoing.Rows {
						if fmt.Sprint(row["amount"]) == "2" {
							if row["destination"] != strings.ToLower(accounts[1]) {
								t.Fatal("factory from direction destination", row)
							}
							kinds[fmt.Sprint(row["kind"])]++
						}
					}
					if kinds["Wallet:transaction:from"] != 1 || kinds["Wallet:transfer:from"] != 1 {
						t.Fatal("factory from transaction/transfer callbacks", kinds)
					}
					completedCalls := calls
					if err := engine.Sync(ctx); err != nil || calls != completedCalls {
						t.Fatal("outgoing event duplicated", calls, err)
					}
					if cacheNamespace != "" {
						rows, err := backend.Query(ctx, "SELECT count(*)::text AS n FROM "+schema.Quote(cacheNamespace)+".traces WHERE chain_id=1 AND block_number>=2 AND \"to\"=$1", childA)
						if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["n"] != "0" {
							t.Fatal("cold disabled orphan cache survived", rows, err)
						}
						if !staged {
							canonical, err := backend.Query(ctx, "SELECT count(*)::text AS n FROM "+schema.Quote(cacheNamespace)+".traces WHERE chain_id=1 AND block_number=1 AND \"to\"=$1 AND value=99", childA)
							if err != nil || len(canonical.Rows) != 1 || canonical.Rows[0]["n"] != "1" {
								t.Fatal("canonical prebirth protocol trace was deleted", canonical, err)
							}
						}
					}
					if !finalDisabled {
						cached, err := engine.cache.Rows(ctx, "traces", 1, 2, 4)
						if err != nil {
							t.Fatal(err)
						}
						for _, row := range cached.Rows {
							var trace map[string]any
							if err := json.Unmarshal([]byte(fmt.Sprint(row["row"])), &trace); err != nil {
								t.Fatal(err)
							}
							if trace["to"] == childA {
								t.Fatal("orphan cached factory account trace", trace)
							}
						}
					}
				})
			}
		}
	}
}
