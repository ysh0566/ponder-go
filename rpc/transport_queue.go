package rpc

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/ysh0566/ponder-go/async"
)

// LoadBalancedRequester sends each request to the next child in round-robin order.
type LoadBalancedRequester struct {
	mu       sync.Mutex
	children []AsyncRequester
	next     int
}

func NewLoadBalancedRequester(children ...AsyncRequester) (*LoadBalancedRequester, error) {
	if len(children) == 0 {
		return nil, errors.New("load balancing requires at least one transport")
	}
	for _, child := range children {
		if child == nil {
			return nil, errors.New("nil child transport")
		}
	}
	return &LoadBalancedRequester{children: append([]AsyncRequester(nil), children...)}, nil
}
func (r *LoadBalancedRequester) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	r.mu.Lock()
	child := r.children[r.next]
	r.next = (r.next + 1) % len(r.children)
	r.mu.Unlock()
	return child.BeginCall(ctx, method, params, out)
}
func (r *LoadBalancedRequester) Call(ctx context.Context, method string, params, out any) error {
	return awaitTransport(r.BeginCall(ctx, method, params, out))
}

func awaitTransport(done <-chan error) error {
	if done != nil {
		if err, ok := <-done; ok {
			return err
		}
	}
	return errors.New("child transport returned no completion")
}

type queuedRequest struct {
	ctx         context.Context
	method      string
	params, out any
}

type RateLimitOptions struct {
	RequestsPerSecond int
	// NextTick schedules admissions on the next event-loop turn.
	NextTick bool
}

type RateLimitedRequester struct {
	queue *async.Queue[queuedRequest, struct{}]
}

// NewRateLimitedRequester wraps an already configured transport. Both bounds
// apply across its callers; a cancellation removes work still waiting for a slot.
func NewRateLimitedRequester(child AsyncRequester, options RateLimitOptions) (*RateLimitedRequester, error) {
	if child == nil {
		return nil, errors.New("rate limiting requires a child transport")
	}
	loop := async.NewLoop()
	rate := float64(options.RequestsPerSecond)
	limits := async.Limits{Frequency: async.AtMost(rate), Concurrency: async.AtMost(math.Ceil(rate / 4))}
	queue, err := async.NewQueue(loop, async.QueueOptions{Limits: limits, InitialStart: true, NextTick: options.NextTick}, func(task queuedRequest) *async.Future[struct{}] {
		if err := task.ctx.Err(); err != nil {
			return async.Rejected[struct{}](loop, err)
		}
		response := child.BeginCall(task.ctx, task.method, task.params, task.out)
		r := async.NewPromise[struct{}](loop)
		go func() {
			if err := awaitTransport(response); err != nil {
				r.Reject(err)
			} else {
				r.Resolve(struct{}{})
			}
		}()
		return r.Promise
	})
	if err != nil {
		return nil, err
	}
	return &RateLimitedRequester{queue}, nil
}
func (r *RateLimitedRequester) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	done := make(chan error, 1)
	if err := ctx.Err(); err != nil {
		done <- err
		close(done)
		return done
	}
	f := r.queue.Add(queuedRequest{ctx, method, params, out})
	stop := context.AfterFunc(ctx, func() { r.queue.Cancel(f, ctx.Err()) })
	go func() {
		// Once admitted, the child must finish using the output before it
		// becomes available to the caller, including on cancellation.
		_, err := f.Await(context.Background())
		stop()
		done <- err
		close(done)
	}()
	return done
}
func (r *RateLimitedRequester) Call(ctx context.Context, method string, params, out any) error {
	return awaitTransport(r.BeginCall(ctx, method, params, out))
}
