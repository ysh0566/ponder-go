package metrics

import (
	"context"
	"time"
)

type rpcKey struct{}

// WithRPC attaches the writer's registry to requests made during this run.
func WithRPC(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, rpcKey{}, r)
}

// ObserveRPCAttempt records a successful request's elapsed time in milliseconds
// or counts a failed attempt. Cache hits and queue waits do not create observations.
func ObserveRPCAttempt(ctx context.Context, chain, method string, elapsed time.Duration, err error) {
	r, _ := ctx.Value(rpcKey{}).(*Registry)
	if r == nil || chain == "" {
		return
	}
	if err != nil {
		r.rpcErrors.WithLabelValues(chain, method).Inc()
		return
	}
	r.rpcDuration.WithLabelValues(chain, method).Observe(float64(elapsed) / float64(time.Millisecond))
}
