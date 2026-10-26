package indexing

import (
	"context"
	"fmt"
	"sync"
)

// managedClientRoots is installed only by managed constructors. It seals root
// admission while permitting synchronous descendants of an admitted call. It
// never substitutes a caller deadline or cancels cache descendants on return.
// Actual I/O cancellation stays owned by ClientSet and the requester/cache gates.
type managedClientRoots struct {
	firstFailure error
	mu           sync.Mutex
	sealed       bool
	active       map[*managedClientPermit]bool
	done         chan struct{}
}
type managedClientPermit struct{ owner *managedClientRoots }
type managedClientPermitKey struct{}

func newManagedClientRoots() *managedClientRoots {
	g := &managedClientRoots{active: map[*managedClientPermit]bool{}, done: make(chan struct{})}
	close(g.done)
	return g
}
func (g *managedClientRoots) begin(ctx context.Context) (context.Context, func(), error) {
	if g == nil {
		return ctx, func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if p, ok := ctx.Value(managedClientPermitKey{}).(*managedClientPermit); ok && p.owner == g && g.active[p] {
		return ctx, func() {}, nil
	}
	if g.sealed {
		return nil, nil, fmt.Errorf("managed client admission is sealed")
	}
	if len(g.active) == 0 {
		g.done = make(chan struct{})
	}
	p := &managedClientPermit{owner: g}
	g.active[p] = true
	return context.WithValue(ctx, managedClientPermitKey{}, p), func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		delete(g.active, p)
		if len(g.active) == 0 {
			close(g.done)
		}
	}, nil
}
func (g *managedClientRoots) sealAndWait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	g.sealed = true
	done := g.done
	g.mu.Unlock()
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Managed readiness requires successful completion, not merely drained queues.
// Legacy ClientSets have a nil gate and retain their historical error policy.
func (g *managedClientRoots) recordFailure(err error) {
	if g == nil || err == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.firstFailure == nil {
		g.firstFailure = err
	}
}
func (g *managedClientRoots) failure() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.firstFailure
}
