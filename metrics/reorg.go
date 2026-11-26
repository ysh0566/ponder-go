package metrics

import "context"

type reorgKey struct{}

// WithReorgs attaches a registry for canonical fork rollbacks observed by this writer.
func WithReorgs(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, reorgKey{}, r)
}

// ObserveReorg counts one successful rollback of indexed blocks on the forked chain.
func ObserveReorg(ctx context.Context, chain string) {
	r, _ := ctx.Value(reorgKey{}).(*Registry)
	if r != nil {
		r.reorgs.WithLabelValues(chain).Inc()
	}
}
