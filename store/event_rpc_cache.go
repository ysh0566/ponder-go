package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// EventProtocolCache avoids starting another store/backend transaction while a
// handler already owns one (which would deadlock on PGlite and the store lock).
// Cache rows stay in the separate sync schema and share the handler transaction.
type EventProtocolCache struct {
	w  *Writer
	c  *ProtocolCache
	mu sync.Mutex
}

func (w *Writer) EventRPCCache() *EventProtocolCache {
	return &EventProtocolCache{w: w, c: w.s.protocol.Load()}
}
func (c *EventProtocolCache) active() error {
	if c.w.closed.Load() {
		return &NonRetryableUserError{Message: "indexing function returned"}
	}
	if c.c == nil {
		return fmt.Errorf("protocol cache is not attached")
	}
	return c.c.active()
}
func (c *EventProtocolCache) request(key string) (nativeRPCKey, error) {
	request, err := parseNativeRPCKey(key)
	if err == nil && request.chain != c.w.event.Checkpoint.ChainID {
		return nativeRPCKey{}, fmt.Errorf("event RPC chain mismatch")
	}
	return request, err
}
func (c *EventProtocolCache) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.active(); e != nil {
		return nil, false, e
	}
	request, e := c.request(key)
	if e != nil {
		return nil, false, e
	}
	return c.c.readNativeRPC(ctx, c.w.tx, request)
}
func (c *EventProtocolCache) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.active(); e != nil {
		return e
	}
	request, e := c.request(key)
	if e != nil {
		return e
	}
	if !json.Valid(value) {
		return fmt.Errorf("RPC result must be JSON")
	}
	return c.c.writeNativeRPC(ctx, c.w.tx, request, value)
}
