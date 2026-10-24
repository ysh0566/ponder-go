package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Supplementary native boundary evidence; no original source-test credit.
func TestConfiguredConcurrency(t *testing.T) {
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ ID uint64 }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		calls.Add(1)
		started <- struct{}{}
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": "0x1"})
	}))
	defer server.Close()
	// Release handlers even when an assertion fails, before Server.Close waits.
	defer close(release)
	client := New(server.URL)
	client.RequestsPerSecond, client.Retries = 0, 0
	if err := client.ConfigureConcurrency(2); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, scoped := range []*Client{client.WithCache(nil), client.WithCache(nil)} {
		go func(c *Client) {
			var value string
			done <- c.Call(context.Background(), "eth_chainId", []any{}, &value)
		}(scoped)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("two configured transport slots were not used")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var value string
	if err := client.Call(ctx, "eth_chainId", []any{}, &value); !errors.Is(err, context.Canceled) {
		t.Fatal("queued cancellation", err)
	}
	if calls.Load() != 2 {
		t.Fatal("cache-scoped clients did not share the transport limit", calls.Load())
	}
	// Two tokens release the blocked requests without closing the cleanup channel.
	release <- struct{}{}
	release <- struct{}{}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if err := client.ConfigureConcurrency(3); err == nil {
		t.Fatal("transport limit changed after requests")
	}
}
