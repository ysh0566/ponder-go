package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
)

type balanceRequester struct {
	params []any
	calls  int
	err    error
}

func (r *balanceRequester) Call(ctx context.Context, method string, params any, out any) error {
	if method != "eth_getBalance" {
		return errors.New("unexpected method")
	}
	r.calls++
	r.params = params.([]any)
	if r.err != nil {
		return r.err
	}
	return json.Unmarshal([]byte(`"0x2a"`), out)
}

type balanceCache struct {
	values     map[string]json.RawMessage
	gets, puts int
	putErr     error
}

func (c *balanceCache) CacheGet(_ context.Context, k string) (json.RawMessage, bool, error) {
	c.gets++
	v, ok := c.values[k]
	return v, ok, nil
}
func (c *balanceCache) CachePut(_ context.Context, k string, v json.RawMessage) error {
	c.puts++
	if c.putErr != nil {
		return c.putErr
	}
	c.values[k] = append(json.RawMessage{}, v...)
	return nil
}
func TestClientEventBalanceAndIdentity(t *testing.T) {
	a, b := &balanceRequester{}, &balanceRequester{}
	chains := []Object{{"id": 1}, {"id": 2}}
	s, e := NewClientSet(chains, []rpc.Requester{a, b})
	if e != nil {
		t.Fatal(e)
	}
	c, _ := s.GetClient(chains[0])
	again, _ := s.GetClient(Object{"id": uint64(1)})
	if c != again {
		t.Fatal("client identity changed")
	}
	other, _ := s.GetClient(chains[1])
	if c == other {
		t.Fatal("chains share client")
	}
	address := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	for _, test := range []struct {
		event   Object
		options []BalanceOptions
		tag     string
	}{{Object{"type": "setup", "block": big.NewInt(0)}, nil, "0x0"}, {Object{"event": Object{"block": Object{"number": big.NewInt(2)}}}, nil, "0x2"}, {nil, []BalanceOptions{{BlockNumber: big.NewInt(17)}}, "0x11"}, {nil, []BalanceOptions{{BlockNumber: big.NewInt(17), Cache: "immutable"}}, "latest"}} {
		s.SetEvent(test.event)
		v, e := c.GetBalance(context.Background(), address, test.options...)
		if e != nil || v.Int64() != 42 {
			t.Fatalf("balance %v %v", v, e)
		}
		if !reflect.DeepEqual(a.params, []any{address, test.tag}) {
			t.Fatalf("params %v", a.params)
		}
	}
	if b.calls != 0 {
		t.Fatal("wrong chain used")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := a.calls
	if _, e := c.GetBalance(ctx, address); !errors.Is(e, context.Canceled) || calls != a.calls {
		t.Fatal("cancellation did not prevent request")
	}
	s.SetEvent(nil)
	if _, e := c.GetBalance(context.Background(), address); e == nil {
		t.Fatal("missing block accepted")
	}
}
func TestClientPersistentCacheAndErrors(t *testing.T) {
	r := &balanceRequester{}
	cache := &balanceCache{values: map[string]json.RawMessage{}}
	chain := Object{"id": uint64(1), "cacheRpcRequests": true}
	s, e := NewClientSet([]Object{chain}, []rpc.Requester{r}, cache)
	if e != nil {
		t.Fatal(e)
	}
	c, _ := s.GetClient(chain)
	s.SetEvent(Object{"type": "setup", "block": 0})
	for i := 0; i < 2; i++ {
		if _, e := c.GetBalance(context.Background(), "0xabc"); e != nil {
			t.Fatal(e)
		}
		if e := s.WaitCacheWrites(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if r.calls != 1 || cache.gets != 2 || cache.puts != 1 {
		t.Fatalf("cache calls %d/%d/%d", r.calls, cache.gets, cache.puts)
	}
	if _, ok := cache.values[`[1,"eth_getBalance",["0xabc","0x0"]]`]; !ok {
		t.Fatalf("incompatible cache key: %v", cache.values)
	}
	cache.putErr = errors.New("cache unavailable")
	s.SetEvent(Object{"type": "setup", "block": 1})
	if _, e := c.GetBalance(context.Background(), "0xabc"); e != nil {
		t.Fatal("source ignores insertion errors", e)
	}
	if e := s.WaitCacheWrites(context.Background()); e != nil {
		t.Fatal(e)
	}
	r.err = errors.New("provider failed")
	s.SetEvent(Object{"type": "setup", "block": 2})
	if _, e := c.GetBalance(context.Background(), "0xabc"); e != r.err {
		t.Fatalf("RPC error identity lost %v", e)
	}
}

// This is a transport integration, not a prerecorded handler result: the actual
// rpc.Client performs HTTP JSON-RPC and the adapter decodes the returned quantity.
func TestClientHTTPTransport(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests = append(requests, body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": "0xde0b6b3a7640000"})
	}))
	defer server.Close()
	transport := rpc.New(server.URL)
	transport.RequestsPerSecond = 0
	transport.Retries = 0
	chain := Object{"id": 1}
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{transport})
	if err != nil {
		t.Fatal(err)
	}
	client, _ := clients.GetClient(chain)
	clients.SetEvent(Object{"event": Object{"block": Object{"number": big.NewInt(2)}}})
	value, err := client.GetBalance(context.Background(), "0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	if err != nil || value.String() != "1000000000000000000" {
		t.Fatalf("balance %v %v", value, err)
	}
	if len(requests) != 1 || requests[0]["method"] != "eth_getBalance" || !reflect.DeepEqual(requests[0]["params"], []any{"0x70997970C51812dc3A010C7d01b50e0d17dc79C8", "0x2"}) {
		t.Fatalf("RPC request %#v", requests)
	}
}

type gatedBalanceCache struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
	fail     error
}

func (c *gatedBalanceCache) CacheGet(context.Context, string) (json.RawMessage, bool, error) {
	return nil, false, nil
}
func (c *gatedBalanceCache) CachePut(ctx context.Context, _ string, _ json.RawMessage) error {
	close(c.started)
	defer close(c.finished)
	select {
	case <-c.release:
		return c.fail
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestClientAsyncCacheOwnership(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			cache := &gatedBalanceCache{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}), fail: errors.New("late cache failure")}
			r := &balanceRequester{}
			chain := Object{"id": 1, "cacheRpcRequests": true}
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{r}, cache)
			if err != nil {
				t.Fatal(err)
			}
			clients.SetEvent(Object{"type": "setup", "block": 0})
			client, _ := clients.GetClient(chain)
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			defer clients.Close(ctx)
			result := make(chan error, 1)
			go func() {
				value, e := client.GetBalance(ctx, "0xabc")
				if e == nil && value.Cmp(big.NewInt(42)) != 0 {
					e = fmt.Errorf("unexpected balance %v", value)
				}
				result <- e
			}()
			select {
			case <-cache.started:
			case <-ctx.Done():
				t.Fatal("cache insertion never started")
			}
			select {
			case e := <-result:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal("successful request awaited blocked cache insertion")
			}
			select {
			case <-cache.finished:
				t.Fatal("write finished before release")
			default:
			}
			if cancel {
				if e := clients.Close(ctx); e != nil {
					t.Fatal(e)
				}
			} else {
				close(cache.release)
				if e := clients.WaitCacheWrites(ctx); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case <-cache.finished:
			default:
				t.Fatal("owned cache writer not joined")
			}
			if e := clients.Close(ctx); e != nil {
				t.Fatal(e)
			}
			if _, e := client.GetBalance(ctx, "0xabc"); e == nil {
				t.Fatal("closed client accepted request")
			}
		})
	}
}
