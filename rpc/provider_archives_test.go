package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The launcher serves mined log pages or an exact observed gateway response.
// Closing stdin releases its listeners before the temporary directory is removed.
func startProviderArchives(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("python3", "-B", filepath.Join("..", "tools", "provider_archive_nodes.py"), dir)
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("archive launcher: %v", err)
		}
	})
	line, err := bufio.NewReader(output).ReadBytes('\n')
	if err != nil {
		t.Fatalf("archive startup: %v", err)
	}
	var urls map[string]string
	if err := json.Unmarshal(line, &urls); err != nil {
		t.Fatal(err)
	}
	return urls
}

func TestUpstreamProviderArchives(t *testing.T) {
	urls := startProviderArchives(t)
	const uni = "0x1F98431c8aD98523631AE4a59f267346ea31F984"
	const weth = "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	const baseAddress = "0x2Ae3F1Ec7F1F5012CFEab0185bfc7aa3cf0DEc22"
	const definedOnly = -1
	cases := []struct {
		provider, title, chain, address string
		first, width                    uint64
		logs                            int
	}{
		{"1rpc", "1rpc success", "ethereum", uni, 18_000_000, 1_000, 9},
		{"alchemy", "alchemy success response size", "ethereum", weth, 18_000_000, 2_000, 140_192},
		{"alchemy", "alchemy success block range", "ethereum", uni, 18_000_000, 1_000_000, 3_773},
		{"altitude", "hyperliquid success", "hyper", "0xfD739d4e423301CE9385c1fb8850539D657C296D", 9_419_400, 29_999, 4_217},
		{"ankr-tac", "ankr success", "tac", "0xB63B9f0eb4A6E6f191529D71d4D88cc8900Df2C9", 3_105_972, 10_000, 185},
		{"ankr", "ankr success", "ethereum", uni, 18_000_000, 3_000, 13},
		{"avalanche", "avalanche success", "avax", "0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7", 53_164_500, 2_047, 3_135},
		{"base", "base success", "base", baseAddress, 10_000_000, 1_000, 77},
		{"blast", "blast success", "blast", "0xb9dfCd4CF589bB8090569cb52FaC1b88Dbe4981F", 1_000_000, 10_000, 109},
		{"blockpi", "blockpi success", "ethereum", uni, 18_000_000, 1_024, 9},
		{"celo", "celo success", "celo", "0x471EcE3750Da237f93B8E339c536989b8978a438", 10_000_000, 1_000, 3_649},
		{"chainstack", "chainstack success", "ethereum", uni, 18_000_000, 110, 1},
		{"coinbase", "coinbase success", "base", baseAddress, 10_000_000, 999, 77},
		{"infura", "infura success", "ethereum", uni, 18_000_000, 1_000, 9},
		{"llamarpc", "llamarpc success", "ethereum", uni, 18_000_000, 1_000, 9},
		{"merkle", "merkle success 10k", "ethereum", uni, 18_000_000, 10_000, 49},
		{"merkle", "merkle success 1k", "ethereum", uni, 18_000_000, 1_000, 9},
		{"moonriver", "moonriver success", "moon", "", 12_000_000, 1_024, definedOnly},
		{"publicnode", "publicnode success", "ethereum", uni, 18_000_000, 50_000, 203},
		{"quicknode", "quicknode success", "ethereum", uni, 18_000_000, 10_000, 49},
		{"zkevm", "zkevm success", "zkevm", "", 950_000, 1_000, 2_979},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.title, func(t *testing.T) {
			client := New(urls[tc.chain])
			client.Retries = 0
			// The original Alchemy request returns all 140,192 WETH logs in one response.
			if tc.address == weth {
				client.MaxHTTPResponseBytes = 128 << 20
			}
			client.HTTP = &http.Client{Timeout: time.Minute, Transport: &http.Transport{}}
			defer client.HTTP.CloseIdleConnections()
			query := map[string]any{"fromBlock": fmt.Sprintf("0x%x", tc.first), "toBlock": fmt.Sprintf("0x%x", tc.first+tc.width)}
			if tc.address != "" {
				query["address"] = tc.address
			}
			var logs []json.RawMessage
			if err := client.Call(t.Context(), "eth_getLogs", []any{query}, &logs); err != nil {
				t.Fatal(err)
			}
			if logs == nil {
				t.Fatal("expected a defined log result")
			}
			if tc.logs != definedOnly && len(logs) != tc.logs {
				t.Fatalf("got %d logs, want %d", len(logs), tc.logs)
			}
		})
	}
	t.Run("moonriver/moonriver block range", func(t *testing.T) {
		client := New(urls["moon"])
		client.Retries = 0
		client.HTTP = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{}}
		defer client.HTTP.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		const first = 12_000_000
		lo, limit, beyond := fmt.Sprintf("0x%x", first), fmt.Sprintf("0x%x", first+1024), fmt.Sprintf("0x%x", first+1025)
		query := map[string]any{"fromBlock": lo, "toBlock": beyond}
		var logs []json.RawMessage
		callErr := client.Call(ctx, "eth_getLogs", []any{query}, &logs)
		var rpcErr *Error
		if !errors.As(callErr, &rpcErr) || rpcErr.Code != -32603 {
			t.Fatalf("expected internal RPC error: %v", callErr)
		}
		if !strings.Contains(rpcErr.Message, "block range is too wide (maximum 1024)") {
			t.Fatal(rpcErr.Message)
		}
		plan, err := GetLogsRetryHelper(query, rpcErr)
		if err != nil {
			t.Fatal(err)
		}
		expected := LogsRetryPlan{ShouldRetry: true, IsSuggestedRange: true, Ranges: []LogRange{{lo, limit}, {beyond, beyond}}}
		if !reflect.DeepEqual(plan, expected) {
			t.Fatalf("got %+v, want %+v", plan, expected)
		}
	})
}
