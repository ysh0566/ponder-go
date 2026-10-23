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
func (c *EventProtocolCache) request(key string) (uint64, string, error) {
	chain, hash, e := protocolRequest(key)
	if e != nil {
		return 0, "", e
	}
	if chain != c.w.event.Checkpoint.ChainID {
		return 0, "", fmt.Errorf("event RPC chain mismatch")
	}
	return chain, hash, nil
}
func (c *EventProtocolCache) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.active(); e != nil {
		return nil, false, e
	}
	chain, hash, e := c.request(key)
	if e != nil {
		return nil, false, e
	}
	r, e := c.w.tx.Query(ctx, "SELECT result FROM "+c.c.table("rpc_request_results")+" WHERE chain_id=$1 AND request_hash=$2", chain, hash)
	if e != nil || len(r.Rows) == 0 {
		return nil, false, e
	}
	return json.RawMessage(fmt.Sprint(r.Rows[0]["result"])), true, nil
}
func (c *EventProtocolCache) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.active(); e != nil {
		return e
	}
	chain, hash, e := c.request(key)
	if e != nil {
		return e
	}
	if !json.Valid(value) {
		return fmt.Errorf("RPC result must be JSON")
	}
	// The native key does not yet resolve the source request's block parameter.
	// Keep it unknown and conservatively invalidate it on a chain rewind.
	return c.w.tx.Exec(ctx, "INSERT INTO "+c.c.table("rpc_request_results")+"(chain_id,request_hash,block_number,result) VALUES($1,$2,NULL,$3) ON CONFLICT(chain_id,request_hash) DO UPDATE SET result=excluded.result,block_number=NULL", chain, hash, string(value))
}
