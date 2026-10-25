package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"ponder.local/ponder-go/rpc"
)

type sourceRPCReadQueue struct {
	mu              sync.Mutex
	closed, running bool
	queue           []*sourceRPCRead
	pending         map[*sourceRPCRead]struct{}
	drained         chan struct{}
}
type sourceRPCRead struct {
	ctx    context.Context
	cancel context.CancelFunc
	query  string
	args   []any
	result chan rpc.CacheLookupResult
}

var _ rpc.AsyncCacheReader = (*AsyncSourceRPCRequestCache)(nil)

func newSourceRPCReadQueue() *sourceRPCReadQueue {
	done := make(chan struct{})
	close(done)
	return &sourceRPCReadQueue{pending: map[*sourceRPCRead]struct{}{}, drained: done}
}

// BeginCacheGet validates the adapter envelope, block domain and canonical source
// hash, then enqueues a prepared single-request SELECT before returning. Its queue
// is independent of write admission. Underlying Store/backend locks still govern
// SQL concurrency and visibility; this is not a new pool or transaction topology.
func (c *AsyncSourceRPCRequestCache) BeginCacheGet(ctx context.Context, key string) <-chan rpc.CacheLookupResult {
	result := make(chan rpc.CacheLookupResult, 1)
	fail := func(err error) <-chan rpc.CacheLookupResult {
		result <- rpc.CacheLookupResult{Err: err}
		close(result)
		return result
	}
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
	if _, err = sourceRequestBlock(request); err != nil {
		return fail(err)
	}
	hash, err := SourceRPCRequestHash(request)
	if err != nil {
		return fail(err)
	}
	child, cancel := context.WithCancel(ctx)
	job := &sourceRPCRead{ctx: child, cancel: cancel, query: "SELECT request_hash,result FROM " + c.base.cache.table("rpc_request_results") + " WHERE chain_id=$1 AND request_hash IN ($2)", args: []any{c.base.chain, hash}, result: result}
	q := c.reads
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
		go c.runReads()
	}
	q.mu.Unlock()
	return result
}
func (c *AsyncSourceRPCRequestCache) runReads() {
	q := c.reads
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
		var result rpc.CacheLookupResult
		result.Err = c.base.cache.asyncRPCTransaction(job.ctx, func(tx Transaction) error {
			rows, err := tx.Query(job.ctx, job.query, job.args...)
			if err != nil {
				return err
			}
			for _, row := range rows.Rows {
				if fmt.Sprint(row["request_hash"]) == job.args[1] {
					result.Value = json.RawMessage(fmt.Sprint(row["result"]))
					result.Hit = true
					break
				}
			}
			return nil
		})
		if result.Err != nil {
			result.Value = nil
			result.Hit = false
		}
		job.cancel()
		job.result <- result
		close(job.result)
		q.mu.Lock()
		delete(q.pending, job)
		if len(q.pending) == 0 {
			close(q.drained)
		}
		q.mu.Unlock()
	}
}

// WaitCacheReads joins admitted lookup work. Stop admission before using it as a
// backend-lifetime boundary; Close seals and cancels both read and write queues.
func (c *AsyncSourceRPCRequestCache) WaitCacheReads(ctx context.Context) error {
	q := c.reads
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
