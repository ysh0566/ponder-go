package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"ponder.local/ponder-go/rpc"
)

// AsyncSourceRPCRequestCache is an opt-in, standalone source-cache adapter.
// Unlike Writer.SourceRPCRequestCache and callback-bound RPCRequestCache, it
// owns asynchronous writes to independent protocol-cache transactions. The
// caller must drain or Close it before releasing the backend. Copying this
// handle preserves the queue identity; all mutable state lives behind pointers.
//
// This does not make the native Store concurrent: its transaction mutex still
// serializes SQL, so CacheGet can wait for an executing write. Admission is not
// a promise of commit order relative to other adapter/database operations.
type AsyncSourceRPCRequestCache struct {
	base  *SourceRPCRequestCache
	state *sourceRPCWriteQueue
}

type sourceRPCWriteQueue struct {
	mu      sync.Mutex
	closed  bool
	running bool
	queue   []*sourceRPCWrite
	pending map[*sourceRPCWrite]struct{}
	drained chan struct{}
}

type sourceRPCWrite struct {
	ctx    context.Context
	cancel context.CancelFunc
	query  string
	args   []any
	result chan error
}

var _ rpc.Cache = (*AsyncSourceRPCRequestCache)(nil)
var _ rpc.AsyncCache = (*AsyncSourceRPCRequestCache)(nil)

// AsyncSourceRPCRequestCache opts into synchronous admission and owned futures.
// No corresponding factory exists on Writer or a callback-bound SourceSyncStore:
// those adapters retain their synchronous transaction-ownership contract.
func (c *ProtocolCache) AsyncSourceRPCRequestCache(chain uint64) *AsyncSourceRPCRequestCache {
	done := make(chan struct{})
	close(done)
	return &AsyncSourceRPCRequestCache{
		base:  c.SourceRPCRequestCache(chain),
		state: &sourceRPCWriteQueue{pending: make(map[*sourceRPCWrite]struct{}), drained: done},
	}
}

func (c *AsyncSourceRPCRequestCache) active() error {
	if c == nil || c.base == nil || c.state == nil {
		return fmt.Errorf("async source RPC cache is not attached")
	}
	c.state.mu.Lock()
	closed := c.state.closed
	c.state.mu.Unlock()
	if closed {
		return fmt.Errorf("async source RPC cache is closed")
	}
	return c.base.active()
}

func (c *AsyncSourceRPCRequestCache) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	if err := c.active(); err != nil {
		return nil, false, err
	}
	return c.base.CacheGet(ctx, key)
}

// CachePut retains the old synchronous operation; only BeginCachePut queues.
func (c *AsyncSourceRPCRequestCache) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	if err := c.active(); err != nil {
		return err
	}
	return c.base.CachePut(ctx, key, value)
}

// BeginCachePut parses a fresh request, snapshots the result, constructs the
// exact MD5/DO NOTHING SQL rows, then appends that prepared write to the real
// owned queue before returning. The worker, not a scheduling handshake, consumes
// this queue. Validation errors resolve the same single-result future immediately.
func (c *AsyncSourceRPCRequestCache) BeginCachePut(ctx context.Context, key string, value json.RawMessage) <-chan error {
	result := make(chan error, 1)
	fail := func(err error) <-chan error { result <- err; close(result); return result }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := c.active(); err != nil {
		return fail(err)
	}
	request, err := c.base.request(key)
	if err != nil {
		return fail(err)
	}
	if !json.Valid(value) {
		return fail(fmt.Errorf("RPC result must be JSON"))
	}
	block, err := sourceRequestBlock(request)
	if err != nil {
		return fail(err)
	}
	query, args, err := c.base.cache.prepareSourceRPC(c.base.chain, []SourceRPCRawResult{{Request: request, BlockNumber: block, Result: string(value)}})
	if err != nil {
		return fail(err)
	}
	child, cancel := context.WithCancel(ctx)
	job := &sourceRPCWrite{ctx: child, cancel: cancel, query: query, args: args, result: result}
	q := c.state
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		cancel()
		return fail(fmt.Errorf("async source RPC cache is closed"))
	}
	if len(q.pending) == 0 {
		q.drained = make(chan struct{})
	}
	q.pending[job] = struct{}{}
	q.queue = append(q.queue, job)
	if !q.running {
		q.running = true
		go c.runWrites()
	}
	q.mu.Unlock()
	return result
}

func (c *AsyncSourceRPCRequestCache) runWrites() {
	q := c.state
	for {
		q.mu.Lock()
		if len(q.queue) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		job := q.queue[0]
		q.queue[0] = nil
		q.queue = q.queue[1:]
		q.mu.Unlock()
		err := c.base.cache.asyncRPCTransaction(job.ctx, func(tx Transaction) error {
			return tx.Exec(job.ctx, job.query, job.args...)
		})
		job.cancel()
		job.result <- err
		close(job.result)
		q.mu.Lock()
		delete(q.pending, job)
		if len(q.pending) == 0 {
			close(q.drained)
		}
		q.mu.Unlock()
	}
}

// WaitCacheWrites joins currently admitted writes. Callers must stop admitting
// work before treating this as a backend/transaction-lifetime boundary.
func (c *AsyncSourceRPCRequestCache) WaitCacheWrites(ctx context.Context) error {
	q := c.state
	q.mu.Lock()
	done := q.drained
	q.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close seals admission, cancels pending writes, and waits for owned completion.
// If ctx expires, ownership is retained and a later Close/WaitCacheWrites can
// join it. Backend operations already executing retain backend cancellation
// semantics: neither an in-flight PGlite round trip nor Backend.Begin waiting
// on a different Store's transaction on the same PGlite backend is forcibly
// interrupted. A Close deadline reports that wait without abandoning the job.
func (c *AsyncSourceRPCRequestCache) Close(ctx context.Context) error {
	q := c.state
	q.mu.Lock()
	q.closed = true
	for job := range q.pending {
		job.cancel()
	}
	q.mu.Unlock()
	return c.WaitCacheWrites(ctx)
}

// Only queued standalone writes use cancellable Store-lock acquisition. In
// particular, Close may be called while the same Store's callback owns s.mu;
// cancellation must not require that callback to return first. This does not
// alter the locking/commit behavior of existing synchronous cache operations.
func (c *ProtocolCache) asyncRPCTransaction(ctx context.Context, fn func(Transaction) error) error {
	if err := c.active(); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.s.mu.TryLock() {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	defer c.s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.active(); err != nil {
		return err
	}
	tx, err := c.s.Backend.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := c.s.lock(ctx, tx); err != nil {
		return err
	}
	if err := c.s.requireLease(ctx, tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
