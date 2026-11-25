package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestUpstreamProviderRangeErrors(t *testing.T) {
	var captures map[string]struct {
		Request struct {
			Method string
			Params []map[string]any
		}
		Status   int
		Response map[string]any
	}
	raw, err := os.ReadFile("../testdata/rpc/provider-errors.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &captures); err != nil {
		t.Fatal(err)
	}
	var endpoints map[string]string
	if path := os.Getenv("PONDER_TEST_RANGE_ERROR_URLS"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &endpoints); err != nil {
			t.Fatal(err)
		}
		for name := range captures {
			if endpoints[name] == "" {
				t.Fatalf("missing live endpoint for %s", name)
			}
		}
	}
	cases := []struct {
		provider, title, address string
		from, to, firstEnd       uint64
		status, code             int
		message                  string
		suggested                bool
	}{
		{"base", "base block range", "0x2Ae3F1Ec7F1F5012CFEab0185bfc7aa3cf0DEc22", 10_000_000, 10_020_000, 10_010_000, 503, -32011, "no backend is currently healthy to serve traffic", false},
		{"swell", "swell block range", "", 6_002_000, 6_003_001, 6_003_000, 400, -32602, "block range greater than 1000 max", true},
		{"avalanche", "avalanche block range", "0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7", 53_164_500, 53_166_548, 53_166_547, 200, -32000, "maximum is set to 2048", true},
		{"ankr-tac", "ankr request range", "0xB63B9f0eb4A6E6f191529D71d4D88cc8900Df2C9", 3_105_972, 3_115_973, 3_115_972, 200, -32000, "maximum [from, to] blocks distance: 10000", true},
	}
	hex := func(n uint64) string { return fmt.Sprintf("0x%x", n) }
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.title, func(t *testing.T) {
			capture, ok := captures[tc.provider]
			if !ok {
				t.Fatal("missing provider capture")
			}
			endpoint := endpoints[tc.provider]
			if endpoint == "" {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						ID     uint64
						Method string
						Params []map[string]any
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Method != capture.Request.Method || !reflect.DeepEqual(req.Params, capture.Request.Params) {
						t.Errorf("unexpected request: %+v, %v", req, err)
						http.Error(w, "unexpected request", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if capture.Status != 0 {
						w.WriteHeader(capture.Status)
					}
					// Each client allocates its own request ID.
					capture.Response["id"] = req.ID
					_ = json.NewEncoder(w).Encode(capture.Response)
				}))
				defer server.Close()
				endpoint = server.URL
			}
			client := New(endpoint)
			client.Retries = 0
			query := map[string]any{"fromBlock": hex(tc.from), "toBlock": hex(tc.to)}
			if tc.address != "" {
				query["address"] = tc.address
			}
			var result []json.RawMessage
			callErr := client.Call(t.Context(), "eth_getLogs", []any{query}, &result)
			var rpcErr *Error
			if tc.status == http.StatusOK {
				if !errors.As(callErr, &rpcErr) {
					t.Fatalf("expected JSON-RPC error, got %v", callErr)
				}
			} else {
				var httpErr *HTTPStatusError
				if !errors.As(callErr, &httpErr) || httpErr.StatusCode != tc.status {
					t.Fatalf("expected HTTP %d, got %v", tc.status, callErr)
				}
				if err := json.Unmarshal(httpErr.Details, &rpcErr); err != nil {
					t.Fatal(err)
				}
			}
			if rpcErr == nil || rpcErr.Code != tc.code || !strings.Contains(rpcErr.Message, tc.message) {
				t.Fatalf("provider details: %+v", rpcErr)
			}
			plan, err := GetLogsRetryHelper(query, fmt.Errorf("acquiring logs: %w", callErr))
			want := LogsRetryPlan{true, tc.suggested, []LogRange{{hex(tc.from), hex(tc.firstEnd)}, {hex(tc.firstEnd + 1), hex(tc.to)}}}
			if err != nil || !reflect.DeepEqual(plan, want) {
				t.Fatalf("plan=%+v want=%+v err=%v", plan, want, err)
			}
		})
	}
}
