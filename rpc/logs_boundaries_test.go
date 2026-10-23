package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type deniedImmutableCache struct{ get, put int }

func (c *deniedImmutableCache) CacheGet(context.Context, string) (json.RawMessage, bool, error) {
	c.get++
	return nil, false, errors.New("numeric request used immutable cache")
}
func (c *deniedImmutableCache) CachePut(context.Context, string, json.RawMessage) error {
	c.put++
	return errors.New("numeric request used immutable cache")
}
func TestRPCLogBoundaries(t *testing.T) {
	t.Run("provider_data_error", func(t *testing.T) {
		_, e := StandardizeLogs([]map[string]any{{"blockNumber": "0x1", "logIndex": "0x0"}}, []any{map[string]any{}})
		var provider *ProviderError
		if !errors.As(e, &provider) || provider.Message != "Invalid RPC response: 'log.blockHash' is a required property" || len(provider.Meta) != 2 {
			t.Fatal("missing structured provider error", e)
		}
		tx := map[string]any{"blockHash": "0x1", "blockNumber": "0x1", "from": "0x1", "hash": "0x1", "transactionIndex": "0x80000000"}
		_, e = StandardizeTransactions([]map[string]any{tx}, "eth_getBlockByNumber", []any{"0x1", true})
		if !errors.As(e, &provider) {
			t.Fatal("integer bound error must retain provider classification", e)
		}
	})
	t.Run("numeric_cache_bypass", func(t *testing.T) {
		count := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var q struct{ ID any }
			json.NewDecoder(r.Body).Decode(&q)
			count++
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": []any{}})
		}))
		defer s.Close()
		c := New(s.URL)
		c.Retries = 0
		c.RequestsPerSecond = 0
		cache := &deniedImmutableCache{}
		c.Cache = cache
		for i := 0; i < 2; i++ {
			if _, e := c.GetLogs(context.Background(), map[string]any{"fromBlock": "0x1", "toBlock": "0x2"}); e != nil {
				t.Fatal(e)
			}
		}
		if count != 2 || cache.get != 0 || cache.put != 0 {
			t.Fatal("numeric requests were cached", count, cache)
		}
	})
	t.Run("rpc_error_class", func(t *testing.T) {
		count := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var q struct{ ID any }
			json.NewDecoder(r.Body).Decode(&q)
			count++
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32601, "message": "method unavailable"}})
		}))
		defer s.Close()
		c := New(s.URL)
		c.Retries = 4
		c.RequestsPerSecond = 0
		_, e := c.GetLogs(context.Background(), map[string]any{})
		var remote *Error
		if !errors.As(e, &remote) || remote.Code != -32601 || count != 1 {
			t.Fatal("permanent RPC error was retried/reclassified", e, count)
		}
	})
}
