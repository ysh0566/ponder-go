package metrics

import (
	"context"
	"sync"
	"time"
)

type realtimeKey struct{}

// WithRealtime attaches the writer's registry to polled source heads.
func WithRealtime(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, realtimeKey{}, r)
}

// StartRealtimeBlock starts timing a received head. Its acknowledgement records
// both observations once, only if the consumer accepts the head. A missing
// registry returns nil, as for any other head without an acknowledgement.
func StartRealtimeBlock(ctx context.Context, chain string, minedSeconds uint64) func(bool) {
	r, _ := ctx.Value(realtimeKey{}).(*Registry)
	if r == nil || chain == "" {
		return nil
	}
	received := time.Now()
	var once sync.Once
	return func(accepted bool) {
		once.Do(func() {
			if accepted {
				elapsed := time.Since(received)
				arrival := float64(received.UnixMilli()) - float64(minedSeconds)*1000
				r.blockArrival.WithLabelValues(chain).Observe(arrival)
				r.realtimeLatency.WithLabelValues(chain).Observe(float64(elapsed) / float64(time.Millisecond))
			}
		})
	}
}
