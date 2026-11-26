package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestGoEmptyAndBlocksTemplates(t *testing.T) {
	setProjectBuildFlags(t)
	for _, key := range []string{"PONDER_LOCAL_RPC_URL", "PONDER_LOCAL_CONTRACT", "DATABASE_URL", "DATABASE_PRIVATE_URL", "DATABASE_VIEWS_SCHEMA", "PONDER_DATABASE_MODE"} {
		t.Setenv(key, "")
	}
	const oracle = "0xD10aBbC76679a20055E167BB80A24ac851b37056"
	const localOracle = "0x1111111111111111111111111111111111111111"
	node := testanvil.StartChain(t, 1, "--no-mining", "--timestamp", "1700000000")
	t.Setenv("PONDER_RPC_URL_1", node)
	client := rpc.New(node)
	call := func(method string, params ...any) {
		t.Helper()
		if params == nil {
			params = []any{}
		}
		var result any
		if err := client.Call(t.Context(), method, params, &result); err != nil {
			t.Fatal(err)
		}
	}
	// Accept only latestAnswer() and return (block.number - 4) * 12500000
	// as int256. A latest-state read would return the same price for every row.
	const oracleCode = "0x60003560e01c6350d25bcd14601357600080fd5b6004430362bebc200260005260206000f3"
	call("anvil_setCode", localOracle, oracleCode)
	for n := 1; n <= 17; n++ {
		call("evm_setNextBlockTimestamp", 1700000000+n)
		call("evm_mine")
	}
	for _, tc := range []struct {
		template, address string
		start             uint64
	}{
		{"empty", "0x0000000000000000000000000000000000000000", 1234567},
		{"blocks", oracle, 19750000},
	} {
		t.Run(tc.template, func(t *testing.T) {
			root := generatedGoTemplate(t, t.Context(), tc.template)
			file := filepath.Join(root, "network.json")
			body, err := os.ReadFile(file)
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
			if err := json.Unmarshal(body, &network); err != nil {
				t.Fatal(err)
			}
			if network.Name != "mainnet" || network.ID != 1 || network.Address != tc.address || network.Start != tc.start || network.End != nil {
				t.Fatalf("unexpected reference configuration %+v", network)
			}
			if tc.template == "blocks" {
				t.Setenv("PONDER_LOCAL_CONTRACT", localOracle)
			} else {
				encoded, err := os.ReadFile(filepath.Join(root, "abi.json"))
				var entries []any
				if err != nil || json.Unmarshal(encoded, &entries) != nil || entries == nil || len(entries) != 0 {
					t.Fatalf("expected editable empty ABI: %s, %v", encoded, err)
				}
			}
			network.Start = 2
			end := uint64(12)
			network.End = &end
			body, err = json.Marshal(network)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, body, 0600); err != nil {
				t.Fatal(err)
			}
			for _, backend := range []string{"postgres", "pglite"} {
				t.Run(backend, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
					t.Cleanup(cancel)
					ns := templateDatabase(t, ctx, backend)
					q, _ := startTemplate(t, ctx, root, ns)
					if tc.template == "empty" {
						checkTemplateRows(t, ctx, q, "SELECT id,name FROM example")
						return
					}
					checkTemplateRows(t, ctx, q, "SELECT timestamp,price FROM chainlink_price ORDER BY timestamp",
						"1700000002 -0.25", "1700000007 0.375", "1700000012 1")
				})
			}
		})
	}
}
