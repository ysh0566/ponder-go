package rpc

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPResponseSize(t *testing.T) {
	const envelope = `{"jsonrpc":"2.0","id":1,"result":"complete"}`
	for _, encoding := range []string{"length", "chunked", "gzip"} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/extra_%d", encoding, extra), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					body := envelope + strings.Repeat(" ", extra)
					switch encoding {
					case "length":
						w.Header().Set("Content-Length", strconv.Itoa(len(body)))
					case "chunked":
						w.(http.Flusher).Flush()
					case "gzip":
						w.Header().Set("Content-Encoding", "gzip")
						writer := gzip.NewWriter(w)
						_, _ = io.WriteString(writer, body)
						_ = writer.Close()
						return
					}
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				client := New(server.URL)
				client.MaxHTTPResponseBytes = int64(len(envelope))
				cache := &memoryCache{values: map[string]json.RawMessage{}}
				scoped := client.WithCache(cache)
				result := "initial"
				err := scoped.CallCached(context.Background(), "eth_chainId", []any{}, &result)
				if extra == 0 {
					if err != nil || result != "complete" || len(cache.values) != 1 {
						t.Fatalf("exact fit: result=%q, cache entries=%d, err=%v", result, len(cache.values), err)
					}
				} else if !errors.Is(err, ErrHTTPResponseTooLarge) || result != "initial" || len(cache.values) != 0 {
					t.Fatalf("overflow: result=%q, cache entries=%d, err=%v", result, len(cache.values), err)
				}
				if calls.Load() != 1 {
					t.Fatalf("sent %d requests", calls.Load())
				}
			})
		}
	}
	t.Run("alternate endpoint", func(t *testing.T) {
		large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(" ", 100))
		}))
		defer large.Close()
		small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":"0x1"}`)
		}))
		defer small.Close()
		client := New(large.URL, small.URL)
		client.MaxHTTPResponseBytes = 64
		var result string
		if err := client.Call(context.Background(), "eth_chainId", []any{}, &result); err != nil || result != "0x1" {
			t.Fatalf("second endpoint: result=%q, err=%v", result, err)
		}
	})
}

func TestLocalResponseLimitPagination(t *testing.T) {
	ctx := context.Background()
	query := map[string]any{"fromBlock": "0x0", "toBlock": "0x3"}
	limitErr := fmt.Errorf("local budget: %w", ErrHTTPResponseTooLarge)
	mock := &actionMock{answer: func(r ActionRequest) (any, error) {
		bounds := actionRanges([]ActionRequest{r})[0]
		if bounds[1]-bounds[0] > 1 {
			return nil, limitErr
		}
		return []any{actionLog(fmt.Sprintf("0x%x", bounds[0])), actionLog(fmt.Sprintf("0x%x", bounds[1]))}, nil
	}}
	pages, err := drainPages(NewActions(mock).GetLogsWithPagination(ctx, query, nil))
	if err != nil || !reflect.DeepEqual(pageRanges(pages), [][2]uint64{{0, 1}, {2, 3}}) ||
		!reflect.DeepEqual(actionRanges(mock.calls), [][2]uint64{{0, 3}, {0, 1}, {2, 3}}) {
		t.Fatalf("pages=%v, requests=%v, err=%v", pageRanges(pages), actionRanges(mock.calls), err)
	}
	for _, name := range []string{"single block", "fixed range"} {
		t.Run(name, func(t *testing.T) {
			mock := &actionMock{answer: func(ActionRequest) (any, error) { return nil, limitErr }}
			fixed := uint64(4)
			options := &ActionOptions{EthGetLogsBlockRange: &fixed}
			input := query
			if name == "single block" {
				input = map[string]any{"fromBlock": "0x2", "toBlock": "0x2"}
				options = nil
			}
			pages, err := drainPages(NewActions(mock).GetLogsWithPagination(ctx, input, options))
			if !errors.Is(err, ErrHTTPResponseTooLarge) || len(pages) != 0 || len(mock.calls) != 1 {
				t.Fatalf("pages=%v, requests=%d, err=%v", pages, len(mock.calls), err)
			}
		})
	}
}

func TestHTTPErrorPaginationDetails(t *testing.T) {
	const hint = "block range greater than 1 max"
	const bounded = 64 << 10
	errorBody := func(id string) string { return fmt.Sprintf(`{"id":%s,"error":{"code":-32602,"message":%q}}`, id, hint) }
	for _, tc := range []struct {
		name, media, body string
		chunked, retry    bool
	}{
		{"matching id", "application/json", errorBody("1"), false, true},
		{"unknown id", "application/json", errorBody("null"), false, true},
		{"unassigned id", "application/json", `{"error":{"message":"` + hint + `"}}`, false, true},
		{"mismatched id", "application/json", errorBody("2"), false, false},
		{"ambiguous result", "application/json", `{"id":1,"result":[],"error":{"message":"` + hint + `"}}`, false, false},
		{"result only", "application/json", `{"id":1,"result":"` + hint + `"}`, false, false},
		{"malformed json", "application/json", hint, false, false},
		{"plain text", "text/plain; charset=utf-8", hint, false, true},
		{"html", "text/html", hint, false, false},
		{"exact body limit", "text/plain", hint + strings.Repeat(" ", bounded-len(hint)), true, true},
		{"large chunked body", "text/plain", hint + strings.Repeat(" ", bounded), true, false},
		{"large declared body", "text/plain", hint + strings.Repeat(" ", bounded), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.media)
				if !tc.chunked {
					w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				}
				w.WriteHeader(http.StatusBadRequest)
				if tc.chunked {
					w.(http.Flusher).Flush()
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := New(server.URL)
			client.Retries = 0
			cache := &memoryCache{values: map[string]json.RawMessage{}}
			query := map[string]any{"fromBlock": "0x0", "toBlock": "0x2"}
			var result json.RawMessage
			err := client.WithCache(cache).CallCached(t.Context(), "eth_getLogs", []any{query}, &result)
			var status *HTTPStatusError
			if !errors.As(err, &status) || status.Error() != "RPC HTTP status 400" || result != nil || len(cache.values) != 0 {
				t.Fatalf("result=%s cache=%d err=%v", result, len(cache.values), err)
			}
			plan, err := GetLogsRetryHelper(query, err)
			if err != nil || plan.ShouldRetry != tc.retry {
				t.Fatalf("retry=%t want=%t err=%v", plan.ShouldRetry, tc.retry, err)
			}
		})
	}
}
