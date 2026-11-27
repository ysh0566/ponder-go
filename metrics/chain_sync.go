package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

type chainSyncKey struct{}

// WithChainSync attaches a writer's registry for chain positions and phases.
func WithChainSync(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, chainSyncKey{}, r)
}

func ChainSync(ctx context.Context) *Registry {
	r, _ := ctx.Value(chainSyncKey{}).(*Registry)
	return r
}

func chainGauge(name, help string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, []string{"chain"})
}

// SetSyncBlock publishes a single canonical position. A fork can move it back.
func (r *Registry) SetSyncBlock(chain string, number, timestamp uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.syncBlock.WithLabelValues(chain).Set(float64(number))
	r.syncTimestamp.WithLabelValues(chain).Set(float64(timestamp))
}

// SetSyncPhase reports a chain that has finished backfill. A backfill-only
// chain has no realtime sample. A live chain reaching its end publishes zero.
func (r *Registry) SetSyncPhase(chain string, complete bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if complete {
		if r.liveChains[chain] {
			r.syncRealtime.WithLabelValues(chain).Set(0)
			delete(r.liveChains, chain)
		}
		r.syncComplete.WithLabelValues(chain).Set(1)
		return
	}
	r.syncComplete.DeleteLabelValues(chain)
	r.syncRealtime.WithLabelValues(chain).Set(1)
	r.liveChains[chain] = true
}
