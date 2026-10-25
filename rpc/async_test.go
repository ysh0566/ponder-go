package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func awaitAsyncCall(t *testing.T, future <-chan error) error {
	t.Helper()
	select {
	case err, ok := <-future:
		if !ok {
			t.Fatal("future closed without result")
		}
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("async RPC future did not resolve")
		return nil
	}
}
func asyncRPCReply(w http.ResponseWriter, id uint64, value any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": value})
}
func asyncRPCRequest(t *testing.T, r *http.Request) uint64 {
	t.Helper()
	var req struct{ ID uint64 }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Error(err)
	}
	return req.ID
}

func TestAsyncRPCAdmission(t *testing.T) {
	t.Run("FIFO identities survive reverse completion and shared transport", func(t *testing.T) {
		releaseFirst := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := asyncRPCRequest(t, r)
			if id == 1 {
				select {
				case <-releaseFirst:
				case <-r.Context().Done():
					return
				}
			}
			asyncRPCReply(w, id, id)
		}))
		defer server.Close()
		defer func() {
			select {
			case <-releaseFirst:
			default:
				close(releaseFirst)
			}
		}()
		client := New(server.URL)
		client.RequestsPerSecond = 0
		var first, second uint64
		one := client.BeginCall(context.Background(), "test", []any{}, &first)
		two := client.WithCache(nil).BeginCall(context.Background(), "test", []any{}, &second)
		if err := awaitAsyncCall(t, two); err != nil {
			t.Fatal(err)
		}
		if second != 2 {
			t.Fatalf("second identity=%d", second)
		}
		select {
		case <-one:
			t.Fatal("first completed before release")
		default:
		}
		close(releaseFirst)
		if err := awaitAsyncCall(t, one); err != nil {
			t.Fatal(err)
		}
		if first != 1 {
			t.Fatalf("first identity=%d", first)
		}
	})
	t.Run("queued cancellation resolves without stealing a slot", func(t *testing.T) {
		releaseFirst := make(chan struct{})
		firstEntered := make(chan struct{})
		var requests atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := asyncRPCRequest(t, r)
			requests.Add(1)
			if id == 1 {
				close(firstEntered)
				select {
				case <-releaseFirst:
				case <-r.Context().Done():
					return
				}
			}
			asyncRPCReply(w, id, id)
		}))
		defer server.Close()
		defer func() {
			select {
			case <-releaseFirst:
			default:
				close(releaseFirst)
			}
		}()
		client := New(server.URL)
		client.RequestsPerSecond = 0
		if err := client.ConfigureConcurrency(1); err != nil {
			t.Fatal(err)
		}
		var first, second, third uint64
		one := client.BeginCall(context.Background(), "test", nil, &first)
		select {
		case <-firstEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("first not admitted")
		}
		two := client.BeginCall(context.Background(), "test", nil, &second)
		ctx, cancel := context.WithCancel(context.Background())
		three := client.BeginCall(ctx, "test", nil, &third)
		cancel()
		if err := awaitAsyncCall(t, three); !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation=%v", err)
		}
		if requests.Load() != 1 || third != 0 {
			t.Fatalf("cancelled queue made IO: requests=%d result=%d", requests.Load(), third)
		}
		close(releaseFirst)
		if err := awaitAsyncCall(t, one); err != nil {
			t.Fatal(err)
		}
		if err := awaitAsyncCall(t, two); err != nil {
			t.Fatal(err)
		}
		if first != 1 || second != 2 || requests.Load() != 2 {
			t.Fatalf("identities/capacity differ: %d %d requests=%d", first, second, requests.Load())
		}
	})
	t.Run("active cancellation releases concurrency", func(t *testing.T) {
		entered := make(chan struct{})
		var requests atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := asyncRPCRequest(t, r)
			requests.Add(1)
			if id == 1 {
				close(entered)
				<-r.Context().Done()
				return
			}
			asyncRPCReply(w, id, id)
		}))
		defer server.Close()
		client := New(server.URL)
		client.RequestsPerSecond = 0
		client.Retries = 0
		if err := client.ConfigureConcurrency(1); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var first, second uint64
		one := client.BeginCall(ctx, "test", nil, &first)
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("first not started")
		}
		two := client.BeginCall(context.Background(), "test", nil, &second)
		cancel()
		if err := awaitAsyncCall(t, one); !errors.Is(err, context.Canceled) {
			t.Fatalf("active cancel=%v", err)
		}
		if err := awaitAsyncCall(t, two); err != nil {
			t.Fatal(err)
		}
		if second != 2 || requests.Load() != 2 {
			t.Fatal("capacity not released")
		}
	})
	t.Run("cancel during rate wait releases slot", func(t *testing.T) {
		client := New("http://127.0.0.1:1")
		client.RequestsPerSecond = 1
		client.Retries = 0
		if err := client.ConfigureConcurrency(1); err != nil {
			t.Fatal(err)
		}
		client.nextTime = time.Now().Add(time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		var out any
		future := client.BeginCall(ctx, "test", nil, &out)
		deadline := time.Now().Add(time.Second)
		for len(client.requestSlots) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("rate-wait request not admitted")
			}
			runtime.Gosched()
		}
		cancel()
		if err := awaitAsyncCall(t, future); !errors.Is(err, context.Canceled) {
			t.Fatalf("rate cancel=%v", err)
		}
		if len(client.requestSlots) != 0 {
			t.Fatal("rate cancellation leaked concurrency")
		}
	})
	t.Run("configuration closes at synchronous admission", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client := New("http://127.0.0.1:1")
		var out any
		future := client.BeginCall(ctx, "test", nil, &out)
		if err := client.ConfigureConcurrency(2); err == nil {
			t.Fatal("accepted concurrency changes after Begin")
		}
		if err := awaitAsyncCall(t, future); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("missing endpoint fails without IO", func(t *testing.T) {
		var out any
		if err := awaitAsyncCall(t, New().BeginCall(context.Background(), "test", nil, &out)); err == nil {
			t.Fatal("missing endpoint accepted")
		}
	})
}

func TestAsyncRPCRetryCompatibility(t *testing.T) {
	for _, code := range []int{-32000, -32601, -32602} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id := asyncRPCRequest(t, r)
				n := calls.Add(1)
				if n == 1 {
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": "test error"}})
					return
				}
				asyncRPCReply(w, id, "ok")
			}))
			defer server.Close()
			client := New(server.URL)
			client.RequestsPerSecond = 0
			client.Retries = 1
			var out string
			err := awaitAsyncCall(t, client.BeginCall(context.Background(), "test", nil, &out))
			if code == -32000 {
				if err != nil || calls.Load() != 2 || out != "ok" {
					t.Fatalf("retry changed: %v %d %s", err, calls.Load(), out)
				}
			} else {
				var rpcErr *Error
				if !errors.As(err, &rpcErr) || rpcErr.Code != code || calls.Load() != 1 {
					t.Fatalf("nonretry changed: %v calls=%d", err, calls.Load())
				}
			}
		})
	}
}
