package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestGoProtocolTemplates(t *testing.T) {
	setProjectBuildFlags(t)
	for _, key := range []string{"PONDER_LOCAL_RPC_URL", "PONDER_LOCAL_CONTRACT", "DATABASE_URL", "DATABASE_PRIVATE_URL", "DATABASE_VIEWS_SCHEMA", "PONDER_DATABASE_MODE"} {
		t.Setenv(key, "")
	}
	code, err := os.ReadFile("testdata/templates/protocol-examples.hex")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, network, reference, first, next string
		id, start                             uint64
	}{
		{"friendtech", "base", "0xcf205808ed36593aa40a44f10c7f7c2f67d4a4d4", "firstTrades()", "nextTrades()", 8453, 2430440},
		{"uniswap-v3-flash", "mainnet", "0x1F98431c8aD98523631AE4a59f267346ea31F984", "firstFlashes()", "nextFlash()", 1, 12369621},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := generatedGoTemplate(t, t.Context(), tc.name)
			networkFile := filepath.Join(root, "network.json")
			body, err := os.ReadFile(networkFile)
			if err != nil {
				t.Fatal(err)
			}
			var network map[string]any
			if err := json.Unmarshal(body, &network); err != nil {
				t.Fatal(err)
			}
			if network["id"] != float64(tc.id) || network["name"] != tc.network || network["address"] != tc.reference || network["startBlock"] != float64(tc.start) || network["endBlock"] != nil {
				t.Fatal(network)
			}
			network["startBlock"] = 2
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
					node := testanvil.StartChain(t, tc.id, "--no-mining", "--timestamp", "1700000000")
					endpoint, calls := observeTemplateRPC(t, node, "eth_call")
					t.Setenv(fmt.Sprintf("PONDER_RPC_URL_%d", tc.id), endpoint)
					c := contractTemplateChain{t: t, rpc: rpc.New(node)}
					c.call("eth_accounts", []any{}, &c.accounts)
					for _, addr := range []string{tc.reference, localRoute} {
						var ignored any
						c.call("anvil_setCode", []any{addr, "0x" + strings.TrimSpace(string(code))}, &ignored)
					}
					first := crypto.Keccak256([]byte(tc.first))[:4]
					c.send(tc.reference, first, true) // before startBlock
					c.send(localRoute, first, true)   // unrelated emitter
					hash := c.send(tc.reference, first, true)
					check := func(t *testing.T, resumed bool) {
						t.Helper()
						q, _ := startTemplate(t, ctx, root, ns)
						if tc.name == "friendtech" {
							checkFriendtech(t, ctx, q, resumed)
						} else {
							checkFlashPools(t, ctx, q, resumed)
							checkPoolReads(t, &c, hash, calls())
						}
					}
					if !t.Run("initial", func(t *testing.T) { check(t, false) }) {
						return
					}
					c.send(tc.reference, crypto.Keccak256([]byte(tc.next))[:4], true)
					t.Run("reopen", func(t *testing.T) { check(t, true) })
				})
			}
		})
	}
}

func checkFriendtech(t *testing.T, ctx context.Context, q query.Client, resumed bool) {
	t.Helper()
	const a, b, c = filterAPIAccountA, filterAPIAccountB, filterAPIAccountC
	check := func(sql string, want ...string) { t.Helper(); checkTemplateRows(t, ctx, q, sql, want...) }
	subjects := []string{b + " 1 4 6 3 25 2"}
	traders := []string{a + " 1 25 0 -25 3 2"}
	shares := []string{b + " " + a + " 7"}
	events := []string{a + " " + b + " BUY 3 100 7 4 111 3 1700000003", a + " " + b + " BUY 4 20 2 3 25 7 1700000003"}
	sells := 0
	if resumed {
		const huge = "1267650600228229401496703205376"
		subjects = []string{b + " 3 3 3 9 69 5", c + " 0 0 0 0 0 0"}
		traders = []string{a + " 3 32 33 1 8 5", b + " 0 0 0 0 0 0"}
		shares = []string{b + " " + a + " 5", b + " " + b + " 1", c + " " + a + " " + huge}
		events = append(events, a+" "+b+" SELL 2 41 3 5 33 5 1700000004", b+" "+b+" BUY 1 2 0 1 3 6 1700000004", a+" "+c+" BUY "+huge+" 7 0 0 7 "+huge+" 1700000004")
		sells = 1
	}
	check("SELECT address,total_trades,total_shares,last_price,earnings,trader_volume,protocol_fees_generated FROM subject ORDER BY address", subjects...)
	check("SELECT address,total_trades,spend,earnings,profit,subject_fees_paid,protocol_fees_paid FROM trader ORDER BY address", traders...)
	check("SELECT subject,trader,amount FROM share ORDER BY subject,trader", shares...)
	check("SELECT trader,subject,trade_type,share_amount,eth_amount,protocol_eth_amount,subject_eth_amount,trader_amount,supply,timestamp FROM trade_event WHERE length(id)>0 ORDER BY id", events...)
	for _, path := range []string{"/", "/graphql"} {
		body := templateRequest(t, ctx, strings.TrimSuffix(q.BaseURL, "/sql"), path, `{ tradeEvents(where:{tradeType:SELL}){totalCount} }`, 200)
		var result struct {
			Data struct{ TradeEvents struct{ TotalCount *int } }
		}
		if err := json.Unmarshal([]byte(body), &result); err != nil || result.Data.TradeEvents.TotalCount == nil || *result.Data.TradeEvents.TotalCount != sells {
			t.Fatal(body, err)
		}
	}
}

func checkFlashPools(t *testing.T, ctx context.Context, q query.Client, resumed bool) {
	t.Helper()
	const a, b, c = filterAPIAccountA, filterAPIAccountB, filterAPIAccountC
	borrowed, paid := []string{a + " 7", b + " 18", c + " 8"}, []string{a + " 5", b + " 15", c + " 19"}
	if resumed {
		borrowed = []string{a + " 7", b + " 20", c + " 11"}
		paid = []string{a + " 5", b + " 17", c + " 22"}
	}
	checkTemplateRows(t, ctx, q, "SELECT address,amount FROM token_borrowed ORDER BY address", borrowed...)
	checkTemplateRows(t, ctx, q, "SELECT address,amount FROM token_paid ORDER BY address", paid...)
	for _, path := range []string{"/", "/graphql"} {
		body := templateRequest(t, ctx, strings.TrimSuffix(q.BaseURL, "/sql"), path, `{ tokenPaids(orderBy:"address"){items{address amount}} }`, 200)
		var result struct {
			Data struct {
				TokenPaids struct {
					Items []struct{ Address, Amount string }
				}
			}
		}
		if err := json.Unmarshal([]byte(body), &result); err != nil {
			t.Fatal(err)
		}
		var rows []string
		for _, r := range result.Data.TokenPaids.Items {
			rows = append(rows, r.Address+" "+r.Amount)
		}
		if !slices.Equal(rows, paid) {
			t.Fatal(body)
		}
	}
}

// The factory receipt identifies the pools independently of the indexer. Each
// immutable getter should hit the node once per pool, including after reopening.
func checkPoolReads(t *testing.T, c *contractTemplateChain, hash string, calls [][]json.RawMessage) {
	t.Helper()
	var receipt struct {
		Logs []struct {
			Address string
			Topics  []string
			Data    string
		}
	}
	c.call("eth_getTransactionReceipt", []any{hash}, &receipt)
	created := crypto.Keccak256Hash([]byte("PoolCreated(address,address,uint24,int24,address)")).Hex()
	expected := map[string]bool{}
	for _, log := range receipt.Logs {
		if len(log.Topics) > 0 && log.Topics[0] == created {
			if len(log.Data) != 130 {
				t.Fatal(log)
			}
			pool := "0x" + log.Data[len(log.Data)-40:]
			for _, method := range []string{"token0()", "token1()"} {
				expected[pool+fmt.Sprintf("/0x%x", crypto.Keccak256([]byte(method))[:4])] = true
			}
		}
	}
	if len(expected) != 4 || len(calls) != len(expected) {
		t.Fatalf("expected four pool reads, got %d calls for %v", len(calls), expected)
	}
	for _, params := range calls {
		var call struct{ To, Data string }
		if len(params) != 2 || string(params[1]) != `"latest"` || json.Unmarshal(params[0], &call) != nil {
			t.Fatalf("unexpected eth_call: %s", params)
		}
		key := strings.ToLower(call.To) + "/" + call.Data
		if !expected[key] {
			t.Fatalf("unexpected or duplicate eth_call: %s", params)
		}
		delete(expected, key)
	}
}
