package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type historicalKey struct{}

// WithHistorical attaches the application's historical timing registry to an
// indexing run. Passing nil disables measurements for realtime work.
func WithHistorical(ctx context.Context, r *Registry) context.Context {
	if Historical(ctx) == r {
		return ctx
	}
	return context.WithValue(ctx, historicalKey{}, r)
}

// Historical returns the registry attached to this indexing context.
func Historical(ctx context.Context) *Registry {
	r, _ := ctx.Value(historicalKey{}).(*Registry)
	return r
}

// StartExtract and StartTransform return a function that records a completed
// step. Durations use fractional milliseconds, as in Ponder's startClock.
func (r *Registry) StartExtract(step string) func() {
	if r == nil {
		return noTiming
	}
	return startTiming(r.extract, step)
}

func (r *Registry) StartTransform(step string) func() {
	if r == nil {
		return noTiming
	}
	return startTiming(r.transform, step)
}

func noTiming() {}

func startTiming(series *prometheus.GaugeVec, step string) func() {
	start := time.Now()
	return func() {
		elapsed := time.Since(start)
		series.WithLabelValues(step).Add(float64(elapsed) / float64(time.Millisecond))
	}
}
