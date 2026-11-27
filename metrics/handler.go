package metrics

import (
	"context"
	"time"
)

type handlerKey struct{}

// WithHandlers attaches the writer's registry to setup and event callbacks.
func WithHandlers(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, handlerKey{}, r)
}

// Handlers returns the callback timing registry across historical and live phases.
func Handlers(ctx context.Context) *Registry {
	r, _ := ctx.Value(handlerKey{}).(*Registry)
	return r
}

// ObserveHandler records the elapsed callback time in fractional milliseconds.
func (r *Registry) ObserveHandler(chain, event string, elapsed time.Duration) {
	if r != nil {
		r.handlerDuration.WithLabelValues(chain, event).Observe(float64(elapsed) / float64(time.Millisecond))
	}
}
