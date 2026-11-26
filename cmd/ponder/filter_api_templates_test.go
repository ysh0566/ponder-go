package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"

	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
)

const (
	swapSignature     = "Swap(address,address,address,bytes32,bool,uint256,uint256,bool,bool)"
	watched           = "0x1111111111111111111111111111111111111111"
	filterAPIAccountA = "0x00000000000000000000000000000000000000aa"
	filterAPIAccountB = "0x00000000000000000000000000000000000000bb"
	filterAPIAccountC = "0x00000000000000000000000000000000000000cc"
)

// Observe the actual RPC filter sent by the generated application to Anvil.
func observeTemplateLogs(t *testing.T, node string) (string, func() []json.RawMessage) {
	t.Helper()
	dest, err := url.Parse(node)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var filters []json.RawMessage
	proxy := httputil.NewSingleHostReverseProxy(dest)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		var call struct {
			Method string
			Params []json.RawMessage
		}
		if json.Unmarshal(body, &call) == nil && call.Method == "eth_getLogs" && len(call.Params) == 1 {
			mu.Lock()
			filters = append(filters, call.Params[0])
			mu.Unlock()
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(gateway.Close)
	return gateway.URL, func() []json.RawMessage {
		mu.Lock()
		defer mu.Unlock()
		return append([]json.RawMessage(nil), filters...)
	}
}

func templateRequest(t *testing.T, ctx context.Context, base, path, graph string, wantCode int) string {
	t.Helper()
	method := http.MethodGet
	if graph != "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(graph))
	if err != nil {
		t.Fatal(err)
	}
	if graph != "" {
		req.Header.Set("Content-Type", "application/graphql")
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != wantCode {
		t.Fatalf("%s: HTTP %d: %s (%v)", path, response.StatusCode, raw, err)
	}
	return strings.TrimSpace(string(raw))
}

func TestGoFilterAndAPITemplates(t *testing.T) {
	setProjectBuildFlags(t)
	for _, key := range []string{"PONDER_LOCAL_RPC_URL", "DATABASE_URL", "DATABASE_PRIVATE_URL", "DATABASE_VIEWS_SCHEMA", "PONDER_DATABASE_MODE"} {
		t.Setenv(key, "")
	}
	const outsider = "0x2222222222222222222222222222222222222222"
	t.Setenv("PONDER_LOCAL_CONTRACT", watched)
	code, err := os.ReadFile("testdata/templates/filter-api.hex")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, reference string
		first           uint64
		last            any
	}{
		{"filter", "0x54522dA62a15225C95b01bD61fF58b866C50471f", 14438081, nil},
		{"api-functions", "0x32353A6C91143bfd6C7d363B546e62a9A2489A20", 13142655, float64(13150000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := generatedGoTemplate(t, t.Context(), tc.name)
			path := filepath.Join(root, "network.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var network map[string]any
			if err := json.Unmarshal(raw, &network); err != nil {
				t.Fatal(err)
			}
			if network["name"] != "mainnet" || network["id"] != float64(1) || network["address"] != tc.reference || network["startBlock"] != float64(tc.first) || network["endBlock"] != tc.last {
				t.Fatal(network)
			}
			network["startBlock"], network["endBlock"] = 1, 2
			raw, err = json.Marshal(network)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			for _, backend := range []string{"postgres", "pglite"} {
				t.Run(backend, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
					t.Cleanup(cancel)
					node := testanvil.StartChain(t, 1, "--no-mining", "--timestamp", "1700000000")
					endpoint, requests := observeTemplateLogs(t, node)
					t.Setenv("PONDER_RPC_URL_1", endpoint)
					chain := contractTemplateChain{t: t, rpc: rpc.New(node)}
					chain.call("eth_accounts", []any{}, &chain.accounts)
					for _, addr := range []string{watched, outsider} {
						var unused any
						chain.call("anvil_setCode", []any{addr, "0x" + strings.TrimSpace(string(code))}, &unused)
					}
					var input []byte
					if tc.name == "filter" {
						input = append(crypto.Keccak256([]byte("swaps(bool)"))[:4], common.LeftPadBytes([]byte{1}, 32)...)
					} else {
						input = crypto.Keccak256([]byte("tokenEvents()"))[:4]
					}
					chain.send(outsider, input, true)
					ns := templateDatabase(t, ctx, backend)
					q, logPath := startTemplate(t, ctx, root, ns)
					defer func() {
						if t.Failed() {
							body, _ := os.ReadFile(logPath)
							t.Log(string(body))
						}
					}()
					base := strings.TrimSuffix(q.BaseURL, "/sql")
					get := func(path string, code int) string { t.Helper(); return templateRequest(t, ctx, base, path, "", code) }
					if tc.name == "api-functions" {
						if get("/count", 200) != "0" || get("/whale-transfers", 500) != "Not found" {
							t.Fatal("unexpected empty API response")
						}
					}
					chain.send(watched, input, true)
					chain.send(watched, input, true) // past the inclusive end
					// A block's callbacks can commit one at a time. Wait for its last event.
					collection, want := "swapEvents", 1
					if tc.name == "api-functions" {
						collection, want = "approvalEvents", 2
					}
					for {
						body := templateRequest(t, ctx, base, "/graphql", "{ events: "+collection+" { totalCount } }", 200)
						var result struct {
							Data   struct{ Events struct{ TotalCount int } }
							Errors []json.RawMessage
						}
						if err := json.Unmarshal([]byte(body), &result); err != nil || len(result.Errors) > 0 || result.Data.Events.TotalCount > want {
							t.Fatal(body, err)
						}
						if result.Data.Events.TotalCount == want {
							break
						}
						select {
						case <-ctx.Done():
							t.Fatalf("waiting for %d %s: %s", want, collection, body)
						case <-time.After(80 * time.Millisecond):
						}
					}
					if tc.name == "filter" {
						checkSwapFilter(t, ctx, q, base, requests())
					} else {
						checkTokenAPI(t, ctx, base)
					}
				})
			}
		})
	}
}

func checkSwapFilter(t *testing.T, ctx context.Context, q query.Client, base string, filters []json.RawMessage) {
	t.Helper()
	const a, b = filterAPIAccountA, filterAPIAccountB
	checkTemplateRows(t, ctx, q, `SELECT payer,recipient FROM "swapEvent" WHERE length(id)>0`, a+" "+b)
	for _, path := range []string{"/", "/graphql"} {
		body := templateRequest(t, ctx, base, path, "{ swapEvents { items { payer recipient } } }", 200)
		var result struct {
			Data struct {
				SwapEvents struct {
					Items []struct{ Payer, Recipient string }
				}
			}
		}
		if err := json.Unmarshal([]byte(body), &result); err != nil || len(result.Data.SwapEvents.Items) != 1 || result.Data.SwapEvents.Items[0].Payer != a || result.Data.SwapEvents.Items[0].Recipient != b {
			t.Fatal(body, err)
		}
	}
	if len(filters) == 0 {
		t.Fatal("missing eth_getLogs requests")
	}
	selector := crypto.Keccak256Hash([]byte(swapSignature)).Hex()
	for _, raw := range filters {
		var f struct {
			Address []string
			Topics  [][]string
		}
		if err := json.Unmarshal(raw, &f); err != nil || len(f.Topics) != 1 || !slices.Equal(f.Topics[0], []string{selector}) || !slices.Equal(f.Address, []string{watched}) {
			t.Fatalf("unexpected log request %s", raw)
		}
	}
}

func checkTokenAPI(t *testing.T, ctx context.Context, base string) {
	t.Helper()
	const a, b, c = filterAPIAccountA, filterAPIAccountB, filterAPIAccountC
	get := func(path string, code int) string { t.Helper(); return templateRequest(t, ctx, base, path, "", code) }
	for path, want := range map[string]string{"/count": "12", "/count/" + common.HexToAddress(a).Hex(): "3", "/count/" + b: "10", "/count/" + watched: "0"} {
		if got := get(path, 200); got != want {
			t.Fatalf("%s: got %q, want %q", path, got, want)
		}
	}
	get("/count/invalid", 500)
	get("/sql/db", 404)
	get("/sql/live", 404)
	// Compare independent GraphQL reads with the values used by the custom routes.
	graph := templateRequest(t, ctx, base, "/graphql", `{
		accounts(orderBy:"address") { items { address balance isOwner } }
		allowances { items { owner spender amount } }
		approvalEvents(where:{timestamp:1700000002},orderBy:"id") {items {amount}}
		transferEvents { totalCount }
	}`, 200)
	type account struct {
		Address, Balance string
		IsOwner          bool
	}
	type allowance struct{ Owner, Spender, Amount string }
	type approval struct{ Amount string }
	var result struct {
		Data struct {
			Accounts       struct{ Items []account }
			Allowances     struct{ Items []allowance }
			ApprovalEvents struct{ Items []approval }
			TransferEvents struct{ TotalCount int }
		}
	}
	if err := json.Unmarshal([]byte(graph), &result); err != nil ||
		!slices.Equal(result.Data.Accounts.Items, []account{{a, "4000000000000000001", false}, {b, "-45", false}, {c, "45", false}}) ||
		!slices.Equal(result.Data.Allowances.Items, []allowance{{a, c, "17"}}) ||
		!slices.Equal(result.Data.ApprovalEvents.Items, []approval{{"13"}, {"17"}}) ||
		result.Data.TransferEvents.TotalCount != 12 {
		t.Fatal(graph, err)
	}
	body := get("/whale-transfers", 200)
	var rows []struct{ Sender, SenderBalance, Amount string }
	if err := json.Unmarshal([]byte(body), &rows); err != nil || len(rows) != 10 {
		t.Fatal(body, err)
	}
	seen := map[string]bool{}
	for i, row := range rows {
		key := row.Sender + "/" + row.Amount
		if seen[key] {
			t.Fatal(row)
		}
		switch {
		case i < 2:
			if row.Sender != a || row.SenderBalance != "4.000000000000000001" || (row.Amount != "999" && row.Amount != "0.000000000000000001") {
				t.Fatal(row)
			}
		case i == 2:
			if row.Sender != c || row.SenderBalance != "0.000000000000000045" || row.Amount != "4.000000000000000001" {
				t.Fatal(row)
			}
		default:
			last, err := strconv.Atoi(strings.TrimPrefix(row.Amount, "0.00000000000000000"))
			if row.Sender != b || row.SenderBalance != "-0.000000000000000045" || err != nil || last < 1 || last > 9 {
				t.Fatal(row)
			}
		}
		seen[key] = true
	}
}
