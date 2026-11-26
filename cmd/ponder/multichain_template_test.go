package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
)

type wrappedNetwork struct {
	Name       string
	ID         uint64
	Address    string
	StartBlock string
	EndBlock   *uint64
}

type wrappedChain struct {
	net     wrappedNetwork
	client  *rpc.Client
	senders []string
	height  uint64
}

func (c *wrappedChain) rpcCall(t *testing.T, method string, params []any, result any) {
	t.Helper()
	if err := c.client.Call(t.Context(), method, params, result); err != nil {
		t.Fatalf("chain %d %s: %v", c.net.ID, method, err)
	}
}

func (c *wrappedChain) mine(t *testing.T) {
	t.Helper()
	c.height++
	var ignored any
	c.rpcCall(t, "evm_setNextBlockTimestamp", []any{1700000000 + c.height}, &ignored)
	c.rpcCall(t, "evm_mine", []any{}, &ignored)
}

func (c *wrappedChain) deposit(t *testing.T, who int, to string, amount *big.Int) {
	t.Helper()
	var hash string
	c.rpcCall(t, "eth_sendTransaction", []any{map[string]string{
		"from": c.senders[who], "to": to, "value": hexutil.EncodeBig(amount), "gas": "0x18000", "data": "0xd0e30db0",
	}}, &hash)
	c.mine(t)
	var receipt struct {
		Status      string
		BlockNumber hexutil.Uint64
		Logs        []struct{ Address string }
	}
	c.rpcCall(t, "eth_getTransactionReceipt", []any{hash}, &receipt)
	if receipt.Status != "0x1" || uint64(receipt.BlockNumber) != c.height || len(receipt.Logs) != 1 || !strings.EqualFold(receipt.Logs[0].Address, to) {
		t.Fatal(receipt)
	}
}

func newWrappedChains(t *testing.T, networks []wrappedNetwork, code string) []wrappedChain {
	t.Helper()
	chains := make([]wrappedChain, len(networks))
	for i, n := range networks {
		node := testanvil.StartChain(t, n.ID, "--no-mining", "--timestamp", "1700000000")
		t.Setenv(fmt.Sprintf("PONDER_RPC_URL_%d", n.ID), node)
		t.Setenv(fmt.Sprintf("PONDER_WS_URL_%d", n.ID), strings.Replace(node, "http:", "ws:", 1))
		c := &chains[i]
		c.net, c.client = n, rpc.New(node)
		c.rpcCall(t, "eth_accounts", []any{}, &c.senders)
		for _, address := range []string{n.Address, otherWrappedAddress} {
			var ignored any
			c.rpcCall(t, "anvil_setCode", []any{address, code}, &ignored)
		}
		c.deposit(t, 0, n.Address, big.NewInt(999)) // before the first observed head
		for range i {
			c.mine(t)
		} // each chain starts indexing at a different height
		c.deposit(t, 0, n.Address, firstWrappedAmount(n.ID))
	}
	return chains
}

const otherWrappedAddress = "0x1111111111111111111111111111111111111111"

func firstWrappedAmount(id uint64) *big.Int {
	return new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), new(big.Int).SetUint64(id))
}

func TestGoMultichainTemplate(t *testing.T) {
	setProjectBuildFlags(t)
	for _, key := range []string{"DATABASE_URL", "DATABASE_PRIVATE_URL", "DATABASE_VIEWS_SCHEMA", "PONDER_DATABASE_MODE"} {
		t.Setenv(key, "")
	}
	// A global single-chain development override must never redirect all four chains.
	t.Setenv("PONDER_LOCAL_RPC_URL", "http://127.0.0.1:1")
	t.Setenv("PONDER_LOCAL_CONTRACT", otherWrappedAddress)
	root := generatedGoTemplate(t, t.Context(), "multichain")
	body, err := os.ReadFile(filepath.Join(root, "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	var networks []wrappedNetwork
	if err := json.Unmarshal(body, &networks); err != nil {
		t.Fatal(err)
	}
	want := []wrappedNetwork{
		{"mainnet", 1, "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", "latest", nil},
		{"base", 8453, "0x4200000000000000000000000000000000000006", "latest", nil},
		{"optimism", 10, "0x4200000000000000000000000000000000000006", "latest", nil},
		{"polygon", 137, "0x7ceB23fD6bC0adD59E62ac25578270cFf1b9f619", "latest", nil},
	}
	if !slices.Equal(networks, want) {
		t.Fatal(networks)
	}
	raw, err := os.ReadFile("testdata/templates/wrapped-deposits.hex")
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
			t.Cleanup(cancel)
			ns := templateDatabase(t, ctx, backend)
			chains := newWrappedChains(t, networks, "0x"+strings.TrimSpace(string(raw)))
			for i := range chains {
				if chains[i].senders[0] != chains[0].senders[0] {
					t.Fatal("expected the same depositor across chains")
				}
			}
			check := func(t *testing.T, command string, extra map[uint64]int64, second bool) query.Client {
				t.Helper()
				q, _ := launchTemplate(t, ctx, root, ns, command)
				checkWrappedBalances(t, ctx, q, chains, extra, second)
				return q

			}
			var snap string
			base := &chains[1]
			base.rpcCall(t, "evm_snapshot", []any{}, &snap)
			firstHead := base.height
			if !t.Run("first_heads", func(t *testing.T) {
				q := check(t, "start", nil, false)
				optimism := &chains[2]
				optimism.deposit(t, 0, optimism.net.Address, big.NewInt(13))
				want := new(big.Int).Add(firstWrappedAmount(10), big.NewInt(13)).String()
				for {
					r, err := q.Query(ctx, "SELECT balance FROM account WHERE chain_id=10")
					if err != nil || len(r.Rows) != 1 {
						t.Fatal(r, err)
					}
					if fmt.Sprint(r.Rows[0]["balance"]) == want {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatalf("waiting for the live Optimism deposit: %v", r.Rows)
					case <-time.After(50 * time.Millisecond):
					}
				}
			}) {
				return
			}
			extra := map[uint64]int64{1: 3, 8453: 5, 10: 7, 137: 9}
			for i := range chains {
				c := &chains[i]
				c.deposit(t, 0, c.net.Address, big.NewInt(extra[c.net.ID]))
				c.deposit(t, 0, otherWrappedAddress, big.NewInt(19))
			}
			extra[10] += 13 // The live Optimism deposit is already committed.
			chains[3].deposit(t, 1, chains[3].net.Address, big.NewInt(8))
			if !t.Run("catch_up", func(t *testing.T) { check(t, "start", extra, true) }) {
				return
			}
			var reverted bool
			base.rpcCall(t, "evm_revert", []any{snap}, &reverted)
			if !reverted {
				t.Fatal("could not replace Base branch")
			}
			base.height = firstHead
			extra[8453] = 23
			base.deposit(t, 0, base.net.Address, big.NewInt(23))
			base.mine(t)
			if !t.Run("base_reorg", func(t *testing.T) { check(t, "start", extra, true) }) {
				return
			}
			if backend == "postgres" {
				t.Run("standalone_reader", func(t *testing.T) {
					for _, c := range chains {
						t.Setenv(fmt.Sprintf("PONDER_RPC_URL_%d", c.net.ID), "")
						t.Setenv(fmt.Sprintf("PONDER_WS_URL_%d", c.net.ID), "")
					}
					check(t, "serve", extra, true)
				})
			}
		})
	}
}

func checkWrappedBalances(t *testing.T, ctx context.Context, q query.Client, chains []wrappedChain, extra map[uint64]int64, second bool) {
	t.Helper()
	base := strings.TrimSuffix(q.BaseURL, "/sql")
	states := map[string]query.ChainStatus{}
	if err := json.Unmarshal([]byte(templateRequest(t, ctx, base, "/status", "", 200)), &states); err != nil {
		t.Fatal(err)
	}
	if len(states) != len(chains) {
		t.Fatal(states)
	}
	var rows []string
	for _, c := range chains {
		st := states[c.net.Name]
		if st.ID != c.net.ID || st.Block.Number != c.height {
			t.Fatalf("%s: %+v, want height %d", c.net.Name, st, c.height)
		}
		balance := firstWrappedAmount(c.net.ID)
		balance.Add(balance, big.NewInt(extra[c.net.ID]))
		rows = append(rows, fmt.Sprintf("%d %s %s", c.net.ID, c.senders[0], balance))
		if second && c.net.Name == "polygon" {
			rows = append(rows, fmt.Sprintf("%d %s 8", c.net.ID, c.senders[1]))
		}
	}
	slices.Sort(rows)
	checkTemplateRows(t, ctx, q, "SELECT chain_id,address,balance FROM account ORDER BY chain_id::text COLLATE \"C\",address", rows...)
	for _, path := range []string{"/", "/graphql"} {
		payload := templateRequest(t, ctx, base, path, `{accounts(where:{chainId:8453}){items{chainId address balance}}}`, 200)
		var result struct {
			Data struct {
				Accounts struct {
					Items []struct {
						ChainID          uint64
						Address, Balance string
					}
				}
			}
		}
		if err := json.Unmarshal([]byte(payload), &result); err != nil || len(result.Data.Accounts.Items) != 1 {
			t.Fatal(payload, err)
		}
		row := result.Data.Accounts.Items[0]
		if row.ChainID != 8453 || row.Address != chains[1].senders[0] || row.Balance != new(big.Int).Add(firstWrappedAmount(8453), big.NewInt(extra[8453])).String() {
			t.Fatal(row)
		}
	}
}
