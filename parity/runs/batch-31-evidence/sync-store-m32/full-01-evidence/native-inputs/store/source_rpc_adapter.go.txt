package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// SourceRPCRequestCache explicitly selects the source MD5/doNothing contract.
// Existing ProtocolCache.CacheGet/Put keeps the native SHA256/update contract.
type SourceRPCRequestCache struct {
	cache  *ProtocolCache
	writer *Writer
	chain  uint64
	mu     sync.Mutex
}

func (c *ProtocolCache) SourceRPCRequestCache(chain uint64) *SourceRPCRequestCache {
	return &SourceRPCRequestCache{cache: c, chain: chain}
}
func (w *Writer) SourceRPCRequestCache() *SourceRPCRequestCache {
	return &SourceRPCRequestCache{cache: w.s.protocol.Load(), writer: w, chain: w.event.Checkpoint.ChainID}
}
func (c *SourceRPCRequestCache) active() error {
	if c.writer != nil && c.writer.closed.Load() {
		return &NonRetryableUserError{Message: "indexing function returned"}
	}
	if c.cache == nil {
		return fmt.Errorf("protocol cache is not attached")
	}
	return c.cache.active()
}
func (c *SourceRPCRequestCache) request(key string) (map[string]any, error) {
	var parts []json.RawMessage
	if e := json.Unmarshal([]byte(key), &parts); e != nil || len(parts) != 3 {
		return nil, fmt.Errorf("invalid source RPC adapter key")
	}
	var chain uint64
	var method string
	var params any
	if e := json.Unmarshal(parts[0], &chain); e != nil {
		return nil, e
	}
	if chain != c.chain {
		return nil, fmt.Errorf("source RPC chain mismatch")
	}
	if e := json.Unmarshal(parts[1], &method); e != nil {
		return nil, e
	}
	if e := json.Unmarshal(parts[2], &params); e != nil {
		return nil, e
	}
	return map[string]any{"method": method, "params": params}, nil
}
func (c *SourceRPCRequestCache) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.active(); e != nil {
		return nil, false, e
	}
	request, e := c.request(key)
	if e != nil {
		return nil, false, e
	}
	var results []*string
	if c.writer != nil {
		results, e = c.cache.getSourceRPC(ctx, c.writer.tx, c.chain, []any{request})
	} else {
		results, e = c.cache.GetSourceRPCRawRequestResults(ctx, c.chain, []any{request})
	}
	if e != nil || len(results) == 0 || results[0] == nil {
		return nil, false, e
	}
	return json.RawMessage(*results[0]), true, nil
}
func (c *SourceRPCRequestCache) CachePut(ctx context.Context, key string, value json.RawMessage) error {
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
	block, e := sourceRequestBlock(request)
	if e != nil {
		return e
	}
	rows := []SourceRPCRawResult{{request, block, string(value)}}
	if c.writer != nil {
		return c.cache.insertSourceRPC(ctx, c.writer.tx, c.chain, rows)
	}
	return c.cache.InsertSourceRPCRawRequestResults(ctx, c.chain, rows)
}
