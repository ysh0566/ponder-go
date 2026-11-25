package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/internal/testanvil"
)

type sourceFetch func(*http.Request) (*http.Response, error)

func (f sourceFetch) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Inject just the next response, as the original fetch spy does. All other
// requests, including the retry, go to the test's actual local Anvil instance.
func injectSourceResponse(c *Client, status int, body string) *atomic.Int32 {
	var calls atomic.Int32
	c.HTTP.Transport = sourceFetch(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	return &calls
}

func TestSourceRPCRequests(t *testing.T) {
	url := testanvil.Start(t, "--no-mining")
	control := New(url)
	control.RequestsPerSecond = 0
	run := func(name string, test func(*testing.T, *Client)) {
		t.Run(name, func(t *testing.T) {
			var snapshot string
			if err := control.Call(t.Context(), "evm_snapshot", []any{}, &snapshot); err != nil {
				t.Fatal(err)
			}
			defer func() {
				var reverted bool
				if err := control.Call(t.Context(), "evm_revert", []any{snapshot}, &reverted); err != nil || !reverted {
					t.Errorf("revert snapshot %s: %v, %v", snapshot, reverted, err)
				}
			}()
			c, err := NewSourceClient(25, url)
			if err != nil {
				t.Fatal(err)
			}
			test(t, c)
		})
	}
	blockNumber := func(t *testing.T, c *Client) {
		t.Helper()
		var number string
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		if err := c.Call(ctx, "eth_blockNumber", []any{}, &number); err != nil || number != "0x0" {
			t.Fatalf("eth_blockNumber: %q, %v", number, err)
		}
	}
	const tooMany = `{"message":"Too Many Requests"}`
	run("createRpc()", blockNumber)
	run("createRpc() handles rate limiting", func(t *testing.T, c *Client) {
		calls := injectSourceResponse(c, 429, tooMany)
		blockNumber(t, c)
		if calls.Load() != 2 {
			t.Fatalf("got %d attempts", calls.Load())
		}
	})
	run("createRpc() retry BlockNotFoundError", func(t *testing.T, c *Client) {
		var mined json.RawMessage
		if err := control.Call(t.Context(), "anvil_mine", []any{"0x1"}, &mined); err != nil {
			t.Fatal(err)
		}
		var latest, block map[string]any
		if err := control.Call(t.Context(), "eth_getBlockByNumber", []any{"latest", true}, &latest); err != nil || latest["number"] != "0x1" {
			t.Fatalf("mined block: %v %v", latest, err)
		}
		calls := injectSourceResponse(c, 200, `{"jsonrpc":"2.0","result":null,"id":1}`)
		err := c.CallWithOptions(t.Context(), "eth_getBlockByNumber", []any{"0x1", true}, &block, RequestOptions{RetryNullBlockRequest: true})
		if err != nil || block == nil || block["hash"] != latest["hash"] || calls.Load() != 2 {
			t.Fatalf("mined block lookup: %v, %v, %d attempts", block, err, calls.Load())
		}
	})
	run("https://github.com/ponder-sh/ponder/pull/2143", func(t *testing.T, c *Client) {
		now := time.Now()
		c.sourcePool.rpsNow = func() time.Time { return now }
		for range 10 {
			for range 20 {
				blockNumber(t, c)
			}
			now = now.Add(time.Second)
		}
		if limit := c.sourcePool.endpoints[0].limit; limit != 21 {
			t.Fatalf("RPS limit after 10 saturated seconds: %v", limit)
		}
		calls := injectSourceResponse(c, 429, tooMany)
		blockNumber(t, c)
		if limit := c.sourcePool.endpoints[0].limit; limit != 21*0.95 || calls.Load() != 2 {
			t.Fatalf("RPS limit after retry: %v, attempts %d", limit, calls.Load())
		}
	})
	topic0, topic1 := "0x"+strings.Repeat("0", 64), "0x"+strings.Repeat("1", 64)
	equal := func(t *testing.T, got, want []any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("topics = %#v, want %#v", got, want)
		}
	}
	run("sanitizeLogTopics removes trailing null values", func(t *testing.T, _ *Client) {
		equal(t, filter.SanitizeLogTopics([]any{topic0, topic1, nil, nil}), []any{topic0, topic1})
	})
	run("sanitizeLogTopics preserves null wildcards before a topic", func(t *testing.T, _ *Client) {
		equal(t, filter.SanitizeLogTopics([]any{topic0, nil, topic1, nil}), []any{topic0, nil, topic1})
	})
	run("sanitizeLogTopics does not mutate the input", func(t *testing.T, _ *Client) {
		input := []any{topic0, nil, nil}
		result := filter.SanitizeLogTopics(input)
		equal(t, result, []any{topic0})
		result[0] = topic1
		equal(t, input, []any{topic0, nil, nil})
	})
	run("sanitizeLogTopics preserves omitted and empty topics", func(t *testing.T, _ *Client) {
		equal(t, filter.SanitizeLogTopics([]any{}), []any{})
		equal(t, filter.SanitizeLogTopics([]any{nil, nil}), []any{})
	})
}

func TestSourceRPCHTTP(t *testing.T) {
	t.Run("slow body returns TimeoutError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			start := time.Now()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for now := range tick.C {
				elapsed := now.Sub(start)
				fmt.Fprintf(w, "data:%d\n", elapsed.Milliseconds())
				w.(http.Flusher).Flush()
				if elapsed >= 500*time.Millisecond {
					fmt.Fprint(w, "done\n")
					return
				}
			}
		}))
		defer server.Close()
		c := New(server.URL)
		c.Retries = 0
		c.HTTP.Timeout = 300 * time.Millisecond
		var result any
		err := c.Call(t.Context(), "test", []string{"test"}, &result)
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("expected body timeout, got %v", err)
		}
	})
}

func TestSourceRequestBoundaries(t *testing.T) {
	t.Run("null policy is specific to block lookups", func(t *testing.T) {
		for _, tc := range []struct {
			method      string
			retry       bool
			wantMissing bool
		}{
			{"eth_getBlockByHash", true, true},
			{"eth_getBlockByNumber", true, true},
			{"eth_getBlockByHash", false, false},
			{"eth_getTransactionReceipt", true, false},
		} {
			t.Run(fmt.Sprintf("%s-%t", tc.method, tc.retry), func(t *testing.T) {
				var attempts atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct{ ID uint64 }
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						return
					}
					attempts.Add(1)
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":null}`, req.ID)
				}))
				defer server.Close()
				client, err := NewSourceClient(1, server.URL)
				if err != nil {
					t.Fatal(err)
				}
				client.Retries = 1
				var result *string
				err = client.CallWithOptions(t.Context(), tc.method, []any{"0x1", false}, &result, RequestOptions{RetryNullBlockRequest: tc.retry})
				if tc.wantMissing {
					if !errors.Is(err, ErrBlockNotFound) || attempts.Load() != 2 {
						t.Fatalf("after %d attempts: %v", attempts.Load(), err)
					}
				} else if err != nil || result != nil || attempts.Load() != 1 {
					t.Fatalf("after %d attempts: %v, %v", attempts.Load(), result, err)
				}
			})
		}
	})
	t.Run("pagination retains the original range error", func(t *testing.T) {
		for _, fixed := range []bool{false, true} {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct{ ID uint64 }
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				attempts.Add(1)
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32000,"message":"Max range: 3"}}`, req.ID)
			}))
			func() {
				defer server.Close()
				client, err := NewSourceClient(1, server.URL)
				if err != nil {
					t.Fatal(err)
				}
				client.Retries = 1
				client.FixedLogBlockRange = fixed
				var result any
				query := []map[string]any{{"fromBlock": "0x0", "toBlock": "0xffffffff"}}
				err = client.Call(t.Context(), "eth_getLogs", query, &result)
				var protocol *Error
				want := int32(1)
				if fixed {
					want = 2
				}
				if !errors.As(err, &protocol) || protocol.Code != -32000 || attempts.Load() != want {
					t.Fatalf("fixed %t: attempts %d, error %v", fixed, attempts.Load(), err)
				}
			}()
		}
	})
	t.Run("source permanent errors", func(t *testing.T) {
		for _, e := range []error{&HTTPStatusError{StatusCode: 405}, &HTTPStatusError{StatusCode: 404}, &HTTPStatusError{StatusCode: 501}, &HTTPStatusError{StatusCode: 505}, &Error{Code: -32700}, &Error{Code: -32601}, &Error{Code: -32004}, &Error{Code: -32006}, &Error{Code: -32000, Message: "execution reverted"}} {
			if !sourcePermanentError(e) {
				t.Error(e)
			}
		}
		if sourcePermanentError(&Error{Code: -32602}) || sourcePermanentError(&HTTPStatusError{StatusCode: 429}) {
			t.Fatal("retryable source error marked permanent")
		}
	})
	t.Run("async and sync cache views share endpoint admission", func(t *testing.T) {
		var calls atomic.Int32
		active := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct{ ID uint64 }
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				return
			}
			calls.Add(1)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"0x1"}`, req.ID)
		}))
		defer active.Close()
		inactive := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("inactive endpoint selected") }))
		defer inactive.Close()
		c, err := NewSourceClient(4, inactive.URL, active.URL)
		if err != nil {
			t.Fatal(err)
		}
		p := c.sourcePool
		p.endpoints[0].until = time.Now().Add(time.Hour)
		now := time.Now()
		p.rpsNow = func() time.Time { return now }
		view := c.WithCache(&memoryCache{values: map[string]json.RawMessage{}})
		pending := make([]<-chan error, 19)
		out := make([]string, len(pending))
		for i := range pending {
			pending[i] = view.BeginCall(t.Context(), "eth_chainId", []any{}, &out[i])
		}
		var syncResult string
		if err := c.Call(t.Context(), "eth_chainId", []any{}, &syncResult); err != nil || syncResult != "0x1" {
			t.Fatal(syncResult, err)
		}
		for i, done := range pending {
			if err := <-done; err != nil || out[i] != "0x1" {
				t.Fatalf("async %d: %s, %v", i, out[i], err)
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
		defer cancel()
		err = <-view.BeginCall(ctx, "eth_chainId", []any{}, &syncResult)
		if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 20 {
			t.Fatal(calls.Load(), err)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.endpoints[1].active != 0 || p.endpoints[1].limit != 20 || len(p.endpoints[1].seconds) != 1 {
			t.Fatalf("admission counters %v, limit %v", p.endpoints[1].active, p.endpoints[1].limit)
		}
	})
}
