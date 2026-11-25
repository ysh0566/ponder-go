package rpc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type queuedTransportFunc func(context.Context, string, any, any) <-chan error

func (f queuedTransportFunc) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	return f(ctx, method, params, out)
}

func countingTransport(t *testing.T, calls *atomic.Int32) AsyncRequester {
	t.Helper()
	return queuedTransportFunc(func(_ context.Context, method string, _ any, out any) <-chan error {
		if method != "eth_chainId" {
			t.Errorf("method = %s", method)
		}
		calls.Add(1)
		*out.(*string) = "hi"
		done := make(chan error, 1)
		done <- nil
		close(done)
		return done
	})
}

func TestSourceLoadBalance(t *testing.T) {
	t.Run("sends a request", func(t *testing.T) {
		var calls atomic.Int32
		client, err := NewLoadBalancedRequester(countingTransport(t, &calls))
		if err != nil {
			t.Fatal(err)
		}
		var out string
		err = client.Call(t.Context(), "eth_chainId", nil, &out)
		if err != nil || calls.Load() != 1 || out != "hi" {
			t.Fatal(calls.Load(), out, err)
		}
	})
	t.Run("splits requests between transports", func(t *testing.T) {
		var first, second atomic.Int32
		client, err := NewLoadBalancedRequester(countingTransport(t, &first), countingTransport(t, &second))
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			var out string
			if err := client.Call(t.Context(), "eth_chainId", nil, &out); err != nil || out != "hi" {
				t.Fatal(out, err)
			}
		}
		if first.Load() != 1 || second.Load() != 1 {
			t.Fatalf("child calls %d and %d", first.Load(), second.Load())
		}
	})
}

func TestSourceRateLimit(t *testing.T) {
	client := func(t *testing.T, calls *atomic.Int32) *RateLimitedRequester {
		t.Helper()
		r, err := NewRateLimitedRequester(countingTransport(t, calls), RateLimitOptions{RequestsPerSecond: 1, NextTick: true})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	t.Run("sends a request", func(t *testing.T) {
		var calls atomic.Int32
		r := client(t, &calls)
		var out string
		f := r.BeginCall(t.Context(), "eth_chainId", nil, &out)
		if calls.Load() != 1 {
			t.Fatal("child admission was delayed")
		}
		if err := <-f; err != nil || out != "hi" {
			t.Fatal(out, err)
		}
	})
	t.Run("limits request rate", func(t *testing.T) {
		var calls atomic.Int32
		r := client(t, &calls)
		// Start early in a fixed window so both calls arrive in the same second.
		now := time.Now()
		if now.Nanosecond() > 500_000_000 {
			time.Sleep(time.Until(now.Truncate(time.Second).Add(time.Second)))
		}
		var a, b string
		first := r.BeginCall(t.Context(), "eth_chainId", nil, &a)
		second := r.BeginCall(t.Context(), "eth_chainId", nil, &b)
		if calls.Load() != 1 {
			t.Fatal("second call admitted in first window")
		}
		if err := <-first; err != nil || a != "hi" || calls.Load() != 1 {
			t.Fatal(a, err, calls.Load())
		}
		if err := <-second; err != nil || b != "hi" || calls.Load() != 2 {
			t.Fatal(b, err, calls.Load())
		}
	})
}

func TestTransportQueueOwnership(t *testing.T) {
	entered := make(chan string, 2)
	complete := make(chan error, 1)
	child := queuedTransportFunc(func(_ context.Context, method string, _ any, _ any) <-chan error {
		entered <- method
		return complete
	})
	r, err := NewRateLimitedRequester(child, RateLimitOptions{RequestsPerSecond: 4, NextTick: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	first := r.BeginCall(ctx, "in-flight", nil, nil)
	if got := <-entered; got != "in-flight" {
		t.Fatal(got)
	}
	ctx2, cancel2 := context.WithCancel(t.Context())
	second := r.BeginCall(ctx2, "waiting", nil, nil)
	cancel2()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-first:
		t.Fatal("returned ownership before child completion")
	default:
	}
	complete <- context.Canceled
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case unexpected := <-entered:
		t.Fatalf("dispatched %q", unexpected)
	default:
	}
}
