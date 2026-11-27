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

// ObserveHistoricalGroup publishes both sides of a successfully consumed batch.
// Extraction is time spent obtaining it, transformation is time spent consuming it.
func (r *Registry) ObserveHistoricalGroup(extract, transform time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groupDuration.WithLabelValues("extract").Add(float64(extract) / float64(time.Millisecond))
	r.groupDuration.WithLabelValues("transform").Add(float64(transform) / float64(time.Millisecond))
}

// PlanHistoricalBlocks records the inclusive scan size and its cached coverage.
// Catch-up generators extend the same run and preserve its completed sample.
func (r *Registry) PlanHistoricalBlocks(chain string, total, cached float64, catchup bool) {
	r.updateHistoricalBlocks(chain, total, cached, 0, catchup)
}

// SetHistoricalBlocks publishes one consistent snapshot of a completed scan.
func (r *Registry) SetHistoricalBlocks(chain string, total, cached, completed float64) {
	r.updateHistoricalBlocks(chain, total, cached, completed, false)
}

func (r *Registry) updateHistoricalBlocks(chain string, total, cached, completed float64, preserveCompleted bool) {
	if r == nil || chain == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.historyTotal.WithLabelValues(chain).Set(total)
	r.historyCached.WithLabelValues(chain).Set(cached)
	sample := r.historyDone.WithLabelValues(chain)
	if !preserveCompleted {
		sample.Set(completed)
	}
}

// AddHistoricalBlocks records blocks covered by successful acquisition.
func (r *Registry) AddHistoricalBlocks(chain string, count float64) {
	if r == nil || chain == "" || count <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.historyDone.WithLabelValues(chain).Add(count)
}
