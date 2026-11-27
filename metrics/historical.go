package metrics

import (
	"context"
	"slices"
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

// BackfillWindow belongs to one indexing run. Ordered workers share the wall
// clock for their group. An Isolated worker owns its chain's window.
type BackfillWindow struct {
	registry          *Registry
	chains            []string
	started, finished bool
}

// NewBackfillWindow prepares the labels without publishing a timestamp.
func (r *Registry) NewBackfillWindow(chains ...string) *BackfillWindow {
	if r == nil || len(chains) == 0 {
		return nil
	}
	return &BackfillWindow{registry: r, chains: slices.Clone(chains)}
}

// Start publishes a single rounded Unix second for the group before backfill.
// Retries retain the original time.
func (w *BackfillWindow) Start() {
	if w == nil || w.registry == nil {
		return
	}
	r := w.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if w.started {
		return
	}
	w.started = true
	stamp := float64(time.Now().Round(time.Second).Unix())
	for _, chain := range w.chains {
		r.historyEnd.DeleteLabelValues(chain)
		r.historyStart.WithLabelValues(chain).Set(stamp)
	}
}

// Finish records the first successful completion. Live polls and replays
// leave the historical timestamps intact.
func (w *BackfillWindow) Finish() {
	if w == nil || w.registry == nil {
		return
	}
	r := w.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if !w.started || w.finished {
		return
	}
	w.finished = true
	stamp := float64(time.Now().Round(time.Second).Unix())
	for _, chain := range w.chains {
		r.historyEnd.WithLabelValues(chain).Set(stamp)
	}
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
