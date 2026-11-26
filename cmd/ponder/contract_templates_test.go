package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
)

const (
	localFiles   = "0x1111111111111111111111111111111111111111"
	localRoute   = "0x2222222222222222222222222222222222222222"
	localBatch   = "0x3333333333333333333333333333333333333333"
	fileFrontend = "0xBc66C61BCF49Cc3fe4E321aeCEa307F61EC57C0b"
)

type contractTemplateChain struct {
	t        *testing.T
	rpc      *rpc.Client
	methods  map[string]ethabi.ABI
	accounts []string
	height   uint64
}

func newContractTemplateChain(t *testing.T) *contractTemplateChain {
	t.Helper()
	node := testanvil.StartChain(t, 1, "--no-mining", "--timestamp", "1700000000")
	t.Setenv("PONDER_RPC_URL_1", node)
	chain := &contractTemplateChain{t: t, rpc: rpc.New(node), methods: map[string]ethabi.ABI{}}
	body, err := os.ReadFile("testdata/templates/contract-sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]struct {
		Code string
		ABI  json.RawMessage
	}
	if err := json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	for addr, name := range map[string]string{localFiles: "FileSource", localRoute: "FileSource", localBatch: "BatchCalls", fileFrontend: "FileFrontend"} {
		artifact := fixtures[name]
		var unused any
		chain.call("anvil_setCode", []any{addr, artifact.Code}, &unused)
		if artifact.ABI != nil {
			methods, err := ethabi.JSON(strings.NewReader(string(artifact.ABI)))
			if err != nil {
				t.Fatal(err)
			}
			chain.methods[addr] = methods
		}
	}
	chain.call("eth_accounts", []any{}, &chain.accounts)
	return chain
}

func (c *contractTemplateChain) call(method string, params []any, out any) {
	c.t.Helper()
	if err := c.rpc.Call(c.t.Context(), method, params, out); err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
}

func (c *contractTemplateChain) input(to, method string, args ...any) []byte {
	c.t.Helper()
	data, err := c.methods[to].Pack(method, args...)
	if err != nil {
		c.t.Fatal(err)
	}
	return data
}

func (c *contractTemplateChain) send(to string, data []byte, success bool) string {
	c.t.Helper()
	var hash string
	c.call("eth_sendTransaction", []any{map[string]any{"from": c.accounts[0], "to": to, "data": hexutil.Encode(data), "gas": "0x120000"}}, &hash)
	c.height++
	var unused any
	c.call("evm_setNextBlockTimestamp", []any{1700000000 + c.height}, &unused)
	c.call("evm_mine", []any{}, &unused)
	var receipt struct {
		Status      hexutil.Uint64
		BlockNumber hexutil.Uint64
	}
	c.call("eth_getTransactionReceipt", []any{hash}, &receipt)
	if uint64(receipt.BlockNumber) != c.height || (receipt.Status == 1) != success {
		c.t.Fatalf("transaction %s: %+v", hash, receipt)
	}
	return hash
}

func (c *contractTemplateChain) prepareFiles() func(*testing.T, context.Context, query.Client) {
	create := func(name, body, metadata string) {
		c.send(localFiles, c.input(localFiles, "create", name, body, []byte(metadata)), true)
	}
	create("ignored", "", "{}") // below the configured start
	create("é.txt", "雪!", `{"type":"text/plain","encoding":"utf-8","compression":"gzip"}`)
	create("gone", "old", "{}")
	c.send(localFiles, c.input(localFiles, "remove", "gone"), true)
	c.send(localRoute, c.input(localRoute, "createVia", common.HexToAddress(localFiles), "route", "abc", []byte("broken JSON")), true)
	// Clear the transaction recipient's own state after creation. This event
	// comes from the route, so only the historical lookup can return its body.
	c.send(localRoute, c.input(localRoute, "remove", "route"), true)
	return func(t *testing.T, ctx context.Context, q query.Client) {
		checkTemplateGraphPage(t, ctx, q, "files", "name createdAt", 2)
		checkTemplateRows(t, ctx, q, `SELECT name,size,contents,created_at,type,encoding,compression FROM file ORDER BY created_at`,
			"é.txt 4 雪! 1700000002 text/plain utf-8 gzip", "route 3 via route: abc 1700000005 <nil> <nil> <nil>")
	}
}

func (c *contractTemplateChain) prepareTraces() func(*testing.T, context.Context, query.Client) {
	type subcall struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}
	good := func(data ...byte) subcall { return subcall{Target: common.HexToAddress(c.accounts[1]), CallData: data} }
	bad := subcall{Target: common.HexToAddress(localFiles), AllowFailure: true, CallData: []byte{0xff, 0xff, 0xff, 0xff}}
	aggregate := func(calls ...subcall) []byte { return c.input(localBatch, "aggregate3", calls) }
	c.send(localBatch, aggregate(good(0)), true) // before the configured start
	first := c.send(localBatch, aggregate(good(1), good(1, 2, 3, 4, 5), bad), true)
	next := c.send(localBatch, aggregate(good()), true)
	nested := c.send(localRoute, c.input(localRoute, "forward", common.HexToAddress(localBatch), aggregate(bad, good(1, 2, 3, 4, 5, 6, 7, 8))), true)
	empty := c.send(localBatch, aggregate(), true)
	bad.AllowFailure = false
	c.send(localBatch, aggregate(good(1), bad), false)
	type frame struct {
		GasUsed hexutil.Uint64
		Calls   []struct {
			To      string
			GasUsed hexutil.Uint64
		}
	}
	trace := func(hash string) frame {
		var out frame
		c.call("debug_traceTransaction", []any{hash, map[string]any{"tracer": "callTracer"}}, &out)
		return out
	}
	directGas := trace(first).GasUsed + trace(next).GasUsed + trace(empty).GasUsed
	children := trace(nested).Calls
	if directGas == 0 || len(children) != 1 || !strings.EqualFold(children[0].To, localBatch) || children[0].GasUsed == 0 {
		c.t.Fatalf("unexpected call tracer output %+v", children)
	}
	return func(t *testing.T, ctx context.Context, q query.Client) {
		checkTemplateGraphPage(t, ctx, q, "multicalls", "gasUsed successfulCalls failedCalls", 2)
		checkTemplateRows(t, ctx, q, `SELECT lower("from") AS caller,gas_used,bytes,successful_calls,failed_calls FROM multicall ORDER BY successful_calls DESC`,
			fmt.Sprintf("%s %d 4 3 1", strings.ToLower(c.accounts[0]), directGas), fmt.Sprintf("%s %d 3 1 1", localRoute, children[0].GasUsed))
	}
}

func TestGoContractTemplates(t *testing.T) {
	setProjectBuildFlags(t)
	for _, key := range []string{"PONDER_LOCAL_RPC_URL", "PONDER_LOCAL_CONTRACT", "DATABASE_URL", "DATABASE_PRIVATE_URL", "DATABASE_VIEWS_SCHEMA", "PONDER_DATABASE_MODE"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		template, reference, local string
		start                      float64
		prepare                    func(*contractTemplateChain) func(*testing.T, context.Context, query.Client)
	}{
		{"read-contract", "0x9746fD0A77829E12F8A9DBe70D7a322412325B91", localFiles, 15963553, (*contractTemplateChain).prepareFiles},
		{"call-traces", "0xca11bde05977b3631167028862be2a173976ca11", localBatch, 19800000, (*contractTemplateChain).prepareTraces},
	} {
		t.Run(tc.template, func(t *testing.T) {
			root := generatedGoTemplate(t, t.Context(), tc.template)
			networkFile := filepath.Join(root, "network.json")
			body, err := os.ReadFile(networkFile)
			if err != nil {
				t.Fatal(err)
			}
			var network map[string]any
			if err := json.Unmarshal(body, &network); err != nil {
				t.Fatal(err)
			}
			if network["name"] != "mainnet" || network["id"] != float64(1) || network["address"] != tc.reference || network["startBlock"] != tc.start || network["endBlock"] != nil {
				t.Fatal(network)
			}
			chain := newContractTemplateChain(t)
			check := tc.prepare(chain)
			network["startBlock"], network["endBlock"] = 2, chain.height
			body, err = json.Marshal(network)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(networkFile, body, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PONDER_LOCAL_CONTRACT", tc.local)
			for _, db := range []string{"postgres", "pglite"} {
				t.Run(db, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
					t.Cleanup(cancel)
					ns := templateDatabase(t, ctx, db)
					q, _ := startTemplate(t, ctx, root, ns)
					check(t, ctx, q)
				})
			}
		})
	}
}
