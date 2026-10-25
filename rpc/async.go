package rpc

import (
	"context"
	"fmt"
	"sync/atomic"
)

// AsyncRequester binds a request and its output to a future synchronously.
// Implementations must perform real ordered admission before asynchronous
// completion. Merely starting a goroutine around an arbitrary Requester.Call
// does not satisfy this contract. The output belongs to the request until the
// buffered, single-result future resolves; network arrival order is not promised.
type AsyncRequester interface {
	BeginCall(context.Context, string, any, any) <-chan error
}

type asyncCallTask struct {
	client      *Client
	ctx         context.Context
	method      string
	params, out any
	result      chan error
	state       atomic.Uint32
	stopCancel  func() bool
}
type callAdmission struct {
	id      uint64
	url     string
	release func()
}

// BeginCall synchronously appends to the transport's FIFO admission queue. A
// single dispatcher reserves concurrency, rate slot and first-attempt ID/URL in
// that order before handing admitted work to concurrent network workers. This
// preserves request/future ownership even when responses complete out of order.
// Legacy synchronous Call retains its existing behavior; both paths share the
// same transport capacity, rate limiter and request-ID sequence.
func (c *Client) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	result := make(chan error, 1)
	base := c
	for base.transport != nil {
		base = base.transport
	}
	if len(base.URLs) == 0 {
		result <- fmt.Errorf("RPC endpoint is required")
		close(result)
		return result
	}
	task := &asyncCallTask{client: c, ctx: ctx, method: method, params: params, out: out, result: result}
	base.asyncStarted.Store(true)
	task.stopCancel = context.AfterFunc(ctx, func() {
		if task.state.CompareAndSwap(0, 2) {
			task.result <- ctx.Err()
			close(task.result)
		}
	})
	base.asyncMu.Lock()
	base.asyncPending = append(base.asyncPending, task)
	if !base.asyncRunning {
		base.asyncRunning = true
		go base.dispatchAsyncCalls()
	}
	base.asyncMu.Unlock()
	return result
}
func (c *Client) dispatchAsyncCalls() {
	for {
		c.asyncMu.Lock()
		if len(c.asyncPending) == 0 {
			c.asyncRunning = false
			c.asyncMu.Unlock()
			return
		}
		task := c.asyncPending[0]
		c.asyncPending[0] = nil
		c.asyncPending = c.asyncPending[1:]
		c.asyncMu.Unlock()
		if !task.state.CompareAndSwap(0, 1) {
			continue
		}
		complete := func(err error) { task.state.Store(2); task.stopCancel(); task.result <- err; close(task.result) }
		if err := task.ctx.Err(); err != nil {
			complete(err)
			continue
		}
		release, err := c.acquireRequest(task.ctx)
		if err != nil {
			complete(err)
			continue
		}
		if err = c.wait(task.ctx); err != nil {
			release()
			complete(err)
			continue
		}
		id := c.next.Add(1)
		admission := &callAdmission{id: id, url: c.URLs[(id-1)%uint64(len(c.URLs))], release: release}
		go func(task *asyncCallTask, admission *callAdmission) {
			err := task.client.callWithAdmission(task.ctx, false, task.method, task.params, task.out, admission)
			task.state.Store(2)
			task.stopCancel()
			task.result <- err
			close(task.result)
		}(task, admission)
	}
}
