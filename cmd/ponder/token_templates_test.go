package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// These contracts emit the reference events in their constructors. The factory
// creates two tracked children and one unrelated emitter in the same transaction.
func TestGoProjectTemplates(t *testing.T) {
	setProjectBuildFlags(t)
	raw, err := os.ReadFile("testdata/templates/creation.json")
	if err != nil {
		t.Fatal(err)
	}
	var creation map[string]string
	if err := json.Unmarshal(raw, &creation); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PONDER_LOCAL_RPC_URL", "")
	t.Setenv("PONDER_LOCAL_CONTRACT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_PRIVATE_URL", "")
	t.Setenv("DATABASE_VIEWS_SCHEMA", "")
	t.Setenv("PONDER_DATABASE_MODE", "")
	for _, template := range []string{"erc721", "erc1155", "erc4626", "factory", "proxy"} {
		t.Run(template, func(t *testing.T) {
			root := generatedGoTemplate(t, t.Context(), template)
			networkFile := filepath.Join(root, "network.json")
			body, err := os.ReadFile(networkFile)
			if err != nil {
				t.Fatal(err)
			}
			var network struct {
				Name    string  `json:"name"`
				ID      uint64  `json:"id"`
				Address string  `json:"address"`
				Start   uint64  `json:"startBlock"`
				End     *uint64 `json:"endBlock,omitempty"`
			}
			if err := json.Unmarshal(body, &network); err != nil || network.Name == "" || network.ID == 0 || network.Address == "" || network.Start == 0 {
				t.Fatal(network, err)
			}
			node := testanvil.StartChain(t, network.ID, "--no-mining")
			client := rpc.New(node)
			t.Setenv(fmt.Sprintf("PONDER_RPC_URL_%d", network.ID), node)
			var accounts []string
			if err := client.Call(t.Context(), "eth_accounts", []any{}, &accounts); err != nil || len(accounts) == 0 {
				t.Fatal(accounts, err)
			}
			var hash string
			if err := client.Call(t.Context(), "eth_sendTransaction", []any{map[string]string{"from": accounts[0], "data": creation[template], "gas": "0x200000"}}, &hash); err != nil {
				t.Fatal(err)
			}
			var mined string
			if err := client.Call(t.Context(), "evm_mine", []any{}, &mined); err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				ContractAddress, Status string
				Logs                    []struct{ Address string }
			}
			if err := client.Call(t.Context(), "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil || receipt.Status != "0x1" || receipt.ContractAddress == "" {
				t.Fatal(receipt, err)
			}
			// Keep the reference chain and range mode, substituting the local deployment.
			network.Address, network.Start = receipt.ContractAddress, 1
			if network.End != nil {
				*network.End = 5
			}
			body, err = json.Marshal(network)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(networkFile, body, 0600); err != nil {
				t.Fatal(err)
			}
			for _, backend := range []string{"postgres", "pglite"} {
				t.Run(backend, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
					t.Cleanup(cancel)
					ns := templateDatabase(t, ctx, backend)
					q, _ := startTemplate(t, ctx, root, ns)
					check := func(sql string, want ...string) {
						t.Helper()
						checkTemplateRows(t, ctx, q, sql, want...)
					}
					const bigID = "340282366920938463463374607431768211456"
					zero, one, two, three := fmt.Sprintf("0x%040x", 0), fmt.Sprintf("0x%040x", 1), fmt.Sprintf("0x%040x", 2), fmt.Sprintf("0x%040x", 3)
					switch template {
					case "erc721":
						check("SELECT id,owner FROM token", bigID+" "+two)
						check("SELECT address FROM account ORDER BY address", zero, one, two)
						check(`SELECT "from","to",token FROM transfer_event WHERE timestamp > 0 ORDER BY id`, zero+" "+one+" "+bigID, one+" "+two+" "+bigID)
					case "erc1155":
						check("SELECT owner,token_id,balance FROM token_balance ORDER BY owner,token_id",
							zero+" "+bigID+" -100", one+" 3 -8", one+" "+bigID+" 91", two+" 3 8", two+" "+bigID+" 9")
						check("SELECT address FROM account ORDER BY address", zero, one, two)
						for i, token := range []string{bigID, bigID, "3"} {
							check(fmt.Sprintf("SELECT token FROM transfer_event WHERE id LIKE '%%-%d'", i), token)
						}
						check("SELECT count(*) FROM transfer_event WHERE timestamp > 0", "4")
					case "erc4626":
						check("SELECT address,balance FROM account ORDER BY address", zero+" 0", one+" 340282366920938463463374607431768211431", two+" 25")
						check("SELECT owner,spender,amount FROM allowance", one+" "+two+" 13")
						check("SELECT amount FROM approval_event WHERE timestamp > 0 ORDER BY id", "70", "13")
						check("SELECT amount FROM transfer_event WHERE timestamp > 0 ORDER BY id", bigID, "25")
						check("SELECT sender,receiver,assets,shares FROM deposit_event", three+" "+one+" 150 100")
						check("SELECT sender,receiver,owner,assets,shares FROM withdrawal_event", three+" "+two+" "+one+" 12 8")
					case "proxy":
						if len(receipt.Logs) != 4 || receipt.Logs[0].Address == receipt.ContractAddress || receipt.Logs[2].Address != receipt.ContractAddress {
							t.Fatalf("expected direct and delegated router logs: %+v", receipt.Logs)
						}
						check("SELECT lower(liquidator) FROM liquidation_event", fmt.Sprintf("0x%040x", 42))
						check("SELECT lower(new_owner) FROM ownership_transfer_event", fmt.Sprintf("0x%040x", 43))
					case "factory":
						if len(receipt.Logs) != 7 {
							t.Fatalf("expected seven factory and child logs, got %d", len(receipt.Logs))
						}
						check("SELECT event,lower(contract) AS address FROM llama_event ORDER BY event",
							"LlamaCore:ActionCreated "+receipt.Logs[1].Address, "LlamaPolicy:Initialized "+receipt.Logs[4].Address)
					}
				})
			}
		})
	}
}

func templateDatabase(t *testing.T, ctx context.Context, backend string) string {
	t.Helper()
	ns := fmt.Sprintf("ponder_go_tpl_%x", time.Now().UnixNano())
	t.Setenv("PONDER_LOCAL_DATABASE_URL", "")
	t.Setenv("PONDER_PGLITE_DIR", filepath.Join(t.TempDir(), "db"))
	if backend == "postgres" {
		address := os.Getenv("PONDER_TEST_POSTGRES")
		if address == "" {
			t.Skip("requires a task PostgreSQL database")
		}
		t.Setenv("PONDER_LOCAL_DATABASE_URL", address)
		db, err := store.OpenPostgres(ctx, address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			defer db.Close()
			if err := db.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(ns)+","+schema.Quote(ns+"_sync")+" CASCADE"); err != nil {
				t.Error(err)
			}
		})
	}
	return ns
}

func checkTemplateRows(t *testing.T, ctx context.Context, q query.Client, sql string, want ...string) {
	t.Helper()
	result, err := q.Query(ctx, sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	var got []string
	for _, row := range result.Rows {
		var fields []string
		for _, f := range result.Fields {
			fields = append(fields, fmt.Sprint(row[fmt.Sprint(f["name"])]))
		}
		got = append(got, strings.Join(fields, " "))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s: got %v, want %v", sql, got, want)
	}
}

func startTemplate(t *testing.T, ctx context.Context, root, namespace string) (query.Client, string) {
	t.Helper()
	log, err := os.Create(filepath.Join(t.TempDir(), "indexer.log"))
	if err != nil {
		t.Fatal(err)
	}
	child, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	var childErr error
	go func() {
		defer close(done)
		childErr = runCLI(child, []string{"start", "--root", root, "--schema", namespace, "--hostname", "127.0.0.1", "--port", "0", "--shutdown-timeout", "30s"}, log, log)
	}()
	t.Cleanup(func() {
		stop()
		<-done
		log.Close()
		if childErr != nil {
			t.Errorf("start: %v", childErr)
		}
	})
	for {
		body, _ := os.ReadFile(log.Name())
		if _, tail, ok := strings.Cut(string(body), "local example at "); ok {
			if base, _, ok := strings.Cut(tail, "; indexing schema"); ok {
				req, err := http.NewRequestWithContext(ctx, "GET", base+"/ready", nil)
				if err != nil {
					t.Fatal(err)
				}
				res, err := http.DefaultClient.Do(req)
				if err == nil {
					res.Body.Close()
					if res.StatusCode == 200 {
						return query.Client{BaseURL: base + "/sql"}, log.Name()
					}
				}
			}
		}
		select {
		case <-done:
			t.Fatalf("indexer exited: %v\n%s", childErr, body)
		case <-ctx.Done():
			t.Fatalf("indexer startup: %v\n%s", ctx.Err(), body)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestGoAccountsTemplate(t *testing.T) {
	setProjectBuildFlags(t)
	root := generatedGoTemplate(t, t.Context(), "accounts")
	for _, key := range []string{"PONDER_LOCAL_RPC_URL", "DATABASE_URL", "DATABASE_PRIVATE_URL", "DATABASE_VIEWS_SCHEMA", "PONDER_DATABASE_MODE"} {
		t.Setenv(key, "")
	}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			t.Cleanup(cancel)
			ns := templateDatabase(t, ctx, backend)
			node := testanvil.StartChain(t, 1, "--no-mining")
			t.Setenv("PONDER_RPC_URL_1", node)
			client := rpc.New(node)
			var accounts []string
			if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil || len(accounts) < 3 {
				t.Fatal(accounts, err)
			}
			watched, recipient, sender := accounts[0], accounts[1], accounts[2]
			t.Setenv("PONDER_LOCAL_CONTRACT", watched)
			send := func(from, to, value, data string) {
				t.Helper()
				tx := map[string]string{"from": from, "value": value, "data": data, "gas": "0x20000"}
				if to != "" {
					tx["to"] = to
				}
				var hash string
				if err := client.Call(ctx, "eth_sendTransaction", []any{tx}, &hash); err != nil {
					t.Fatal(err)
				}
			}
			mine := func() {
				t.Helper()
				var result string
				if err := client.Call(ctx, "evm_mine", []any{}, &result); err != nil {
					t.Fatal(err)
				}
			}
			send(watched, recipient, "0x20", "0x10")
			mine()
			send(watched, recipient, "0x10000000000000000", "0xab")
			send(watched, recipient, "0x2", "0xcd")
			send(watched, "", "0x0", "0x60006000f3")
			send(sender, watched, "0x7", "0x")
			mine()
			const sql = `SELECT lower("to"), value, data FROM transaction_events`
			if !t.Run("first_head", func(t *testing.T) {
				q, log := startTemplate(t, ctx, root, ns)
				checkTemplateRows(t, ctx, q, sql, recipient+" 18446744073709551618 0xcd")
				body, err := os.ReadFile(log)
				if err != nil || !strings.Contains(string(body), "Received transfer") {
					t.Fatalf("incoming transfer: %v\n%s", err, body)
				}
			}) {
				return
			}
			send(watched, recipient, "0x3", "0xef")
			mine()
			send(watched, recipient, "0x5", "0x42")
			mine()
			t.Run("resume", func(t *testing.T) {
				q, _ := startTemplate(t, ctx, root, ns)
				checkTemplateRows(t, ctx, q, sql, recipient+" 18446744073709551626 0x42")
			})
		})
	}
}
