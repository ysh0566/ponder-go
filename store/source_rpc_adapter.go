package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ysh0566/ponder-go/rpc"
)

// SourceRPCRequestCache explicitly selects the source MD5/doNothing contract.
// Existing ProtocolCache.CacheGet/Put keeps the native SHA256/update contract.
type SourceRPCRequestCache struct {
	cache  *ProtocolCache
	writer *Writer
	source *SourceSyncStore
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
	if c.source != nil {
		if err := c.source.scopeActive(context.Background()); err != nil {
			return err
		}
	}
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
	values, err := c.CacheGetMany(ctx, []string{key})
	if err != nil {
		return nil, false, err
	}
	return values[0], values[0] != nil, nil
}

func (c *SourceRPCRequestCache) CacheGetMany(ctx context.Context, keys []string) ([]json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.active(); err != nil {
		return nil, err
	}
	requests := make([]any, len(keys))
	for i, key := range keys {
		request, err := c.request(key)
		if err != nil {
			return nil, err
		}
		requests[i] = request
	}
	if len(keys) == 0 {
		return []json.RawMessage{}, nil
	}
	var results []*string
	var err error
	if c.writer != nil {
		results, err = c.cache.getSourceRPC(ctx, c.writer.tx, c.chain, requests)
	} else if c.source != nil {
		results, err = c.source.GetRPCRawRequestResults(ctx, c.chain, requests)
	} else {
		results, err = c.cache.GetSourceRPCRawRequestResults(ctx, c.chain, requests)
	}
	if err != nil {
		return nil, err
	}
	if len(results) != len(keys) {
		return nil, fmt.Errorf("expected %d cache results, got %d", len(keys), len(results))
	}
	values := make([]json.RawMessage, len(keys))
	for i, result := range results {
		if result != nil {
			values[i] = json.RawMessage(*result)
		}
	}
	return values, nil
}

func (c *SourceRPCRequestCache) writeRows(entries []rpc.CacheEntry) ([]SourceRPCRawResult, error) {
	rows := make([]SourceRPCRawResult, len(entries))
	for i, entry := range entries {
		request, err := c.request(entry.Key)
		if err != nil {
			return nil, err
		}
		if !json.Valid(entry.Value) {
			return nil, fmt.Errorf("RPC result must be JSON")
		}
		block, err := sourceRequestBlock(request)
		if err != nil {
			return nil, err
		}
		rows[i] = SourceRPCRawResult{Request: request, BlockNumber: block, Result: string(entry.Value)}
	}
	return rows, nil
}
func (c *SourceRPCRequestCache) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.active(); e != nil {
		return e
	}
	rows, e := c.writeRows([]rpc.CacheEntry{{Key: key, Value: value}})
	if e != nil {
		return e
	}
	if c.writer != nil {
		return c.cache.insertSourceRPC(ctx, c.writer.tx, c.chain, rows)
	}
	if c.source != nil {
		return c.source.InsertRPCRawRequestResults(ctx, c.chain, rows)
	}
	return c.cache.InsertSourceRPCRawRequestResults(ctx, c.chain, rows)
}
