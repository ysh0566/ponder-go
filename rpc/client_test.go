package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryCache struct {
	mu     sync.Mutex
	values map[string]json.RawMessage
}

func (m *memoryCache) CacheGet(_ context.Context, key string) (json.RawMessage, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[key]
	return v, ok, nil
}
func (m *memoryCache) CachePut(_ context.Context, key string, v json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = v
	return nil
}
func TestRetryAndCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID uint64 }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0x1"})
	}))
	defer server.Close()
	c := New(server.URL)
	c.RequestsPerSecond = 0
	c.Cache = &memoryCache{values: map[string]json.RawMessage{}}
	for i := 0; i < 2; i++ {
		var out string
		if e := c.CallCached(context.Background(), "eth_getBlockByHash", []any{"0x123", false}, &out); e != nil || out != "0x1" {
			t.Fatalf("%q %v", out, e)
		}
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
func TestInvalidParamsIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct{ ID uint64 }
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "invalid params"}})
	}))
	defer server.Close()
	c := New(server.URL)
	c.RequestsPerSecond = 0
	var out any
	if e := c.Call(context.Background(), "eth_getLogs", nil, &out); e == nil {
		t.Fatal("expected RPC error")
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestRateLimitCancellation(t *testing.T) {
	c := New("http://127.0.0.1:1")
	c.RequestsPerSecond = 1
	c.nextTime = time.Now().Add(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var out any
	start := time.Now()
	if e := c.Call(ctx, "eth_chainId", []any{}, &out); e == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("rate limiter ignored cancellation")
	}
}
