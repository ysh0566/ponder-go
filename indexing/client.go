package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"

	"ponder.local/ponder-go/rpc"
)

// BalanceOptions is the getBalance subset of ponderActions. Immutable requests
// deliberately use latest, even when BlockNumber is also supplied.
type BalanceOptions struct {
	BlockNumber *big.Int
	Cache       string
}

// ClientSet owns stable per-chain client identities and the currently dispatched
// source event. It does not implement Viem's other public actions or prefetching.
type ClientSet struct {
	mu        sync.RWMutex
	event     Object
	clients   map[string]*Client
	closed    bool
	pending   map[uint64]context.CancelFunc
	nextWrite uint64
	drained   chan struct{}
}
type Client struct {
	set          *ClientSet
	requester    rpc.Requester
	cache        rpc.Cache
	chainID      any
	cacheEnabled bool
}

// NewClientSet accepts optional persistent RPC caches in the same order as chains.
// store.SourceRPCRequestCache implements rpc.Cache directly.
func NewClientSet(chains []Object, requesters []rpc.Requester, caches ...rpc.Cache) (*ClientSet, error) {
	if len(chains) != len(requesters) || len(caches) > len(chains) {
		return nil, fmt.Errorf("indexing client chain/requester/cache count mismatch")
	}
	s := &ClientSet{clients: map[string]*Client{}, pending: map[uint64]context.CancelFunc{}, drained: make(chan struct{})}
	close(s.drained)
	for i, chain := range chains {
		key, e := clientChainKey(chain)
		if e != nil {
			return nil, e
		}
		if _, ok := s.clients[key]; ok {
			return nil, fmt.Errorf("duplicate indexing chain %s", key)
		}
		if requesters[i] == nil {
			return nil, fmt.Errorf("missing requester for chain %s", key)
		}
		c := &Client{set: s, requester: requesters[i], chainID: json.Number(key)}
		c.cacheEnabled, _ = chain["cacheRpcRequests"].(bool)
		if i < len(caches) {
			c.cache = caches[i]
		}
		s.clients[key] = c
	}
	return s, nil
}
func clientChainKey(chain Object) (string, error) {
	n, e := clientInteger(chain["id"])
	if e != nil {
		return "", fmt.Errorf("invalid indexing chain id: %w", e)
	}
	return n.String(), nil
}
func (s *ClientSet) SetEvent(event Object) { s.mu.Lock(); defer s.mu.Unlock(); s.event = event }
func (s *ClientSet) GetClient(chain Object) (*Client, error) {
	key, e := clientChainKey(chain)
	if e != nil {
		return nil, e
	}
	c, ok := s.clients[key]
	if !ok {
		return nil, fmt.Errorf("unknown indexing chain %s", key)
	}
	return c, nil
}
func (c *Client) GetBalance(ctx context.Context, address string, options ...BalanceOptions) (*big.Int, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	c.set.mu.RLock()
	closed := c.set.closed
	c.set.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("indexing client is closed")
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("getBalance accepts at most one options object")
	}
	var opt BalanceOptions
	if len(options) == 1 {
		opt = options[0]
	}
	var tag string
	if opt.Cache == "immutable" {
		tag = "latest"
	} else {
		n := opt.BlockNumber
		if n == nil {
			c.set.mu.RLock()
			event := c.set.event
			var v any
			if event["type"] == "setup" {
				v = event["block"]
			} else if data, ok := event["event"].(map[string]any); ok {
				if block, ok := data["block"].(map[string]any); ok {
					v = block["number"]
				}
			}
			var e error
			n, e = clientInteger(v)
			c.set.mu.RUnlock()
			if e != nil {
				return nil, fmt.Errorf("getBalance requires event block: %w", e)
			}
		}
		if n.Sign() < 0 {
			return nil, fmt.Errorf("negative getBalance block number")
		}
		tag = "0x" + n.Text(16)
	}
	params := []any{address, tag}
	keyBytes, e := json.Marshal([]any{c.chainID, "eth_getBalance", params})
	if e != nil {
		return nil, e
	}
	key := string(keyBytes)
	var raw json.RawMessage
	if c.cacheEnabled && c.cache != nil {
		var hit bool
		raw, hit, e = c.cache.CacheGet(ctx, key)
		if e != nil {
			return nil, e
		}
		if hit {
			return decodeBalance(raw)
		}
	}
	if e = c.requester.Call(ctx, "eth_getBalance", params, &raw); e != nil {
		return nil, e
	}
	balance, e := decodeBalance(raw)
	if e != nil {
		return nil, e
	}
	if c.cacheEnabled && c.cache != nil {
		c.set.putAsync(ctx, c.cache, key, raw)
	} // Source ignores cache insertion failures and does not await writes.
	return balance, nil
}
func decodeBalance(raw json.RawMessage) (*big.Int, error) {
	var value string
	if e := json.Unmarshal(raw, &value); e != nil {
		return nil, e
	}
	if len(value) < 3 || value[:2] != "0x" {
		return nil, fmt.Errorf("invalid balance RPC quantity %q", value)
	}
	n, ok := new(big.Int).SetString(value[2:], 16)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid balance RPC quantity %q", value)
	}
	return n, nil
}
func clientInteger(v any) (*big.Int, error) {
	var text string
	switch n := v.(type) {
	case *big.Int:
		if n != nil && n.Sign() >= 0 {
			return new(big.Int).Set(n), nil
		}
	case big.Int:
		if n.Sign() >= 0 {
			return new(big.Int).Set(&n), nil
		}
	case json.Number:
		text = string(n)
	case string:
		text = n
	case int:
		text = fmt.Sprint(n)
	case int64:
		text = fmt.Sprint(n)
	case uint64:
		text = fmt.Sprint(n)
	case uint:
		text = fmt.Sprint(n)
	case float64:
		if n >= 0 && n <= 9007199254740991 && n == float64(uint64(n)) {
			text = fmt.Sprintf("%.0f", n)
		}
	}
	base := 10
	if len(text) > 2 && text[:2] == "0x" {
		base = 16
		text = text[2:]
	}
	if n, ok := new(big.Int).SetString(text, base); ok && n.Sign() >= 0 {
		return n, nil
	}
	return nil, fmt.Errorf("invalid nonnegative integer %v", v)
}

// putAsync owns each fire-and-forget write until completion. A caller using a
// transaction-bound cache must explicitly drain before ending that transaction;
// request completion never waits on cache insertion. Cache implementations must
// honor context cancellation for Close to complete.
func (s *ClientSet) putAsync(ctx context.Context, cache rpc.Cache, key string, raw json.RawMessage) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if len(s.pending) == 0 {
		s.drained = make(chan struct{})
	}
	s.nextWrite++
	id := s.nextWrite
	child, cancel := context.WithCancel(ctx)
	s.pending[id] = cancel
	value := append(json.RawMessage(nil), raw...)
	s.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.pending, id)
			if len(s.pending) == 0 {
				close(s.drained)
			}
			s.mu.Unlock()
		}()
		_ = cache.CachePut(child, key, value)
	}()
}

// WaitCacheWrites drains writes already registered by completed requests. The
// owner must not dispatch new requests concurrently with a transaction boundary.
func (s *ClientSet) WaitCacheWrites(ctx context.Context) error {
	s.mu.RLock()
	done := s.drained
	s.mu.RUnlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close prevents new requests, cancels and joins owned cache writers. Cancellation
// of the wait does not resurrect the set or lose ownership of pending writers.
func (s *ClientSet) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	for _, cancel := range s.pending {
		cancel()
	}
	s.mu.Unlock()
	return s.WaitCacheWrites(ctx)
}
