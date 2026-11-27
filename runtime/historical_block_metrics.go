package runtime

import (
	"context"
	"slices"

	"github.com/ysh0566/ponder-go/interval"
	"github.com/ysh0566/ponder-go/metrics"
)

// Atomic workers discover the exact confirmed prefix while reconciling the
// first poll. Keep its pre-scan gaps so a warm start can distinguish reused
// and newly acquired blocks without another RPC or database query.
type atomicHistoricalScan struct {
	registry *metrics.Registry
	chain    string
	start    uint64
	missing  []interval.Interval
}

type atomicBackfillKey struct{}

func (e *Engine) historicalScan(ctx context.Context, start uint64, missing []interval.Interval) *atomicHistoricalScan {
	registry := metrics.Historical(ctx)
	if registry == nil {
		return nil
	}
	if gaps, ok := ctx.Value(atomicBackfillKey{}).(map[uint64][]interval.Interval); ok {
		// A failed attempt may already have committed a prefix. Its newly
		// acquired heights still belong to this backfill on the next attempt.
		missing = interval.Union(append(gaps[e.chain.ID], missing...))
		gaps[e.chain.ID] = missing
	} else {
		missing = slices.Clone(missing)
	}
	return &atomicHistoricalScan{registry: registry, chain: e.chain.Name, start: start, missing: missing}
}

func (s *atomicHistoricalScan) empty() {
	if s != nil {
		s.registry.SetHistoricalBlocks(s.chain, 0, 0, 0)
	}
}

func (s *atomicHistoricalScan) finish(through uint64) {
	if s == nil {
		return
	}
	if through < s.start {
		s.empty()
		return
	}
	total := float64(through - s.start + 1)
	var completed float64
	for _, gap := range s.missing {
		if uint64(gap[0]) > through {
			break
		}
		completed += float64(min(uint64(gap[1]), through) - uint64(gap[0]) + 1)
	}
	s.registry.SetHistoricalBlocks(s.chain, total, total-completed, completed)
}
