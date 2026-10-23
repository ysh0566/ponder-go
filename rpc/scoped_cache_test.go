package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestScopedCacheSharesTransport(t *testing.T) {
	var mu sync.Mutex
	ids := []uint64{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ ID uint64 }
		if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		ids = append(ids, request.ID)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": "0x1"})
	}))
	defer s.Close()
	base := New(s.URL)
	base.RequestsPerSecond = 0
	base.ChainID = 1
	cache := &memoryCache{values: map[string]json.RawMessage{}}
	view := base.WithCache(cache)
	var result string
	if e := base.Call(context.Background(), "eth_chainId", []any{}, &result); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e := view.CallCached(context.Background(), "eth_chainId", []any{}, &result); e != nil {
			t.Fatal(e)
		}
	}
	if e := base.Call(context.Background(), "eth_chainId", []any{}, &result); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(ids, []uint64{1, 2, 3}) {
		t.Fatal("scoped view reset IDs or missed cache", ids)
	}
	if _, ok := cache.values[`[1,"eth_chainId",[]]`]; !ok {
		t.Fatal("scoped cache changed chain key", cache.values)
	}
}
func TestScopedCacheSharesRateLimit(t *testing.T) {
	base := New("http://127.0.0.1:1")
	base.RequestsPerSecond = 1
	base.nextTime = time.Now().Add(time.Hour)
	view := base.WithCache(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var out any
	if e := view.Call(ctx, "eth_chainId", []any{}, &out); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("scoped view bypassed shared limiter", e)
	}
}
