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
		// The pinned source checks canonical UNI history. The current provider limit is covered below.
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
		{"tron", "tron success", "tron", "0x543208eB34ecad8f91a4D83e597d3c39D67ca47B", 71_583_814, 5_000, 4},
		{"zkevm", "zkevm success", "zkevm", "", 950_000, 1_000, 2_979},
		{"arbitrum", "arbitrum success", "arbitrum", "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1", 1_000_000, 1_000, 714},
		{"aurora", "aurora success", "aurora", "", 57_600_000, 2_000, definedOnly},
		{"harmony", "harmony success", "harmony", "", 70_000_000, 1_024, 703},
		{"optimism", "optimism success", "optimism", "0x871f2F2ff935FD1eD867842FF2a7bfD051A5E527", 100_000_000, 1_000, 0},
		{"somnia", "somnia success", "somnia", "", 53_580_000, 1_000, 5_616},
		{"thirdweb", "thirdweb success", "thirdweb", uni, 18_000_000, 1_000, 9},
		{"zksync", "zksync success", "zksync", "0xfc00dac251711508d4dd7b0c310e913575988838", 18_406_545, 10_000, 726},
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
	for _, tc := range []struct {
		network, title, address, message string
		code                             int
		first, delta, suggestedDelta     uint64
		suggested                        bool
	}{
		{"arbitrum", "arbitrum response size", "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1", "logs matched by query exceeds limit of 10000", -32000, 1_000_000, 20_000, 10_000, false},
		{"aurora", "aurora block range", "", "up to a 2000 block range", -32005, 57_600_000, 2_001, 2_000, true},
		{"cloudflare", "cloudflare block range", "", "Max range: 800", -32047, 18_000_000, 800, 799, true},
		{"harmony", "harmony block range", "", "query must be smaller than size 1024", -32000, 70_000_000, 1_025, 1_024, true},
		{"somnia", "somnia block range", "", "block range exceeds 1000", -1, 53_580_000, 1_001, 1_000, true},
		{"thirdweb", "thirdweb response size", weth, "Maximum allowed number of requested blocks is 1000", -32005, 18_000_000, 1_001, 1_000, true},
		{"zksync", "zksync block range", "0xfc00dac251711508d4dd7b0c310e913575988838", "Try with this block range ", -32602, 18_406_545, 50_000, 45_106, true},
		{"1rpc", "current 50 block limit", uni, "eth_getLogs is limited to 0 - 50 blocks range", -32602, 18_000_000, 51, 50, true},
	} {
		t.Run(tc.network+"/"+tc.title, func(t *testing.T) {
			client := New(urls[tc.network])
			client.Retries = 0
			hex := func(n uint64) string { return fmt.Sprintf("0x%x", n) }
			end := tc.first + tc.delta
			query := map[string]any{"fromBlock": hex(tc.first), "toBlock": hex(end)}
			if tc.address != "" {
				query["address"] = tc.address
			}
			var logs []json.RawMessage
			callErr := client.Call(t.Context(), "eth_getLogs", []any{query}, &logs)
			var providerErr *Error
			if !errors.As(callErr, &providerErr) || providerErr.Code != tc.code || !strings.Contains(providerErr.Message, tc.message) {
				t.Fatalf("unexpected provider error: %v", callErr)
			}
			firstEnd := tc.first + tc.suggestedDelta
			want := LogsRetryPlan{true, tc.suggested, []LogRange{{hex(tc.first), hex(firstEnd)}, {hex(firstEnd + 1), hex(end)}}}
			got, err := GetLogsRetryHelper(query, callErr)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("retry plan: got %+v, want %+v, error %v", got, want, err)
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
	t.Run("tron/tron response size", func(t *testing.T) {
		client := New(urls["tron"])
		client.Retries = 0
		client.HTTP = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{}}
		defer client.HTTP.CloseIdleConnections()
		const first = 71_583_814
		query := map[string]any{"fromBlock": fmt.Sprintf("0x%x", first), "toBlock": fmt.Sprintf("0x%x", first+5001)}
		var logs []json.RawMessage
		callErr := client.Call(t.Context(), "eth_getLogs", []any{query}, &logs)
		var rpcErr *Error
		if !errors.As(callErr, &rpcErr) || rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "exceed max block range") {
			t.Fatalf("unexpected Tron RPC error: %v", callErr)
		}
		plan, err := GetLogsRetryHelper(query, rpcErr)
		if err != nil || !plan.ShouldRetry || len(plan.Ranges) != 2 {
			t.Fatalf("retry plan: %+v (%v)", plan, err)
		}
		want := LogRange{fmt.Sprintf("0x%x", first), fmt.Sprintf("0x%x", first+4999)}
		if plan.Ranges[0] != want {
			t.Fatalf("first retry range %+v, want %+v", plan.Ranges[0], want)
		}
	})

}
