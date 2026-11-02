package store

import (
	"context"
	"fmt"
	"sync"
)

type runtimeScopeKey struct{}
type runtimeIOKey struct{}
type runtimeIOPermit struct{ scope *ManagedRuntimeScope }
type runtimeBatchKey struct{}
type runtimeCommitContextKey struct{}

// ManagedRuntimeScope is a revocable fetch epoch within one durable owner
// generation. Seal stops new I/O and joins admitted work without cancelling it;
// Cancel is reserved for actual caller/owner cancellation. No requester/future
// is stored here or transferred into the next epoch.
type ManagedRuntimeScope struct {
	domains    *ManagedLiveDomains
	generation uint64
	context    context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	sealed     bool
	pending    uint64
	permits    map[*runtimeIOPermit]bool
	done       chan struct{}
}

func (d *ManagedLiveDomains) NewRuntimeScope(ctx context.Context) (context.Context, *ManagedRuntimeScope, error) {
	if !d.mu.TryLock() {
		return nil, nil, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if !d.live().opts.liveRuntime {
		return nil, nil, ErrManagedLiveLegacyMutation
	}
	if err := d.gate(ctx); err != nil {
		return nil, nil, err
	}
	if err := d.runtimeQuiescent(); err != nil {
		return nil, nil, err
	}
	run, cancel := context.WithCancel(ctx)
	s := &ManagedRuntimeScope{domains: d, generation: d.live().opened.Generation, cancel: cancel, done: make(chan struct{})}
	close(s.done)
	s.context = context.WithValue(run, runtimeScopeKey{}, s)
	d.runtimeScopeMu.Lock()
	d.runtimeScope = s
	d.runtimeScopeMu.Unlock()
	return s.context, s, nil
}
func (s *ManagedRuntimeScope) Cancel() {
	if s != nil {
		s.cancel()
	}
}
func (s *ManagedRuntimeScope) Seal(ctx context.Context) error {
	if s == nil {
		return nil
	}
	permit, _ := ctx.Value(runtimeIOKey{}).(*runtimeIOPermit)
	if (permit != nil && permit.scope == s) || ctx.Value(runtimeBatchKey{}) == s || s.domains.runtimeBatch.Load() {
		return ErrManagedLiveBusy
	}
	s.mu.Lock()
	s.sealed = true
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
		s.cancel()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (d *ManagedLiveDomains) runtimeQuiescent() error {
	d.runtimeScopeMu.Lock()
	s := d.runtimeScope
	d.runtimeScopeMu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sealed || s.pending != 0 {
		return ErrManagedLiveBusy
	}
	return nil
}
func (d *ManagedLiveDomains) runtimeScopeFor(ctx context.Context) (*ManagedRuntimeScope, error) {
	if !d.live().opts.liveRuntime {
		return nil, ErrManagedLiveLegacyMutation
	}
	if err := d.live().usable(ctx); err != nil {
		return nil, err
	}
	scope, _ := ctx.Value(runtimeScopeKey{}).(*ManagedRuntimeScope)
	d.runtimeScopeMu.Lock()
	current := d.runtimeScope
	d.runtimeScopeMu.Unlock()
	if scope == nil || scope != current || scope.domains != d || scope.generation != d.live().opened.Generation {
		return nil, fmt.Errorf("runtime sync context has wrong owner or epoch")
	}
	return scope, nil
}
func (d *ManagedLiveDomains) admitRuntimeSync(ctx context.Context) (context.Context, func(), error) {
	s, err := d.runtimeScopeFor(ctx)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, _ := ctx.Value(runtimeIOKey{}).(*runtimeIOPermit)
	if s.sealed && (parent == nil || parent.scope != s || !s.permits[parent]) {
		return nil, nil, fmt.Errorf("runtime sync epoch is sealed")
	}
	if err = s.context.Err(); err != nil {
		return nil, nil, err
	}
	if s.pending == 0 {
		s.done = make(chan struct{})
	}
	s.pending++
	run, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.context, cancel)
	if s.permits == nil {
		s.permits = map[*runtimeIOPermit]bool{}
	}
	permit := &runtimeIOPermit{scope: s}
	s.permits[permit] = true
	run = context.WithValue(run, runtimeIOKey{}, permit)
	finish := func() {
		stop()
		cancel()
		s.mu.Lock()
		delete(s.permits, permit)
		s.pending--
		if s.pending == 0 {
			close(s.done)
		}
		s.mu.Unlock()
	}
	return run, finish, nil
}
func (d *ManagedLiveDomains) runtimeSync(ctx context.Context, fn func(context.Context, *SourceSyncStore) error) error {
	return d.runtimePersist(ctx, fn)
}

func (d *ManagedLiveDomains) WithRuntimeSync(ctx context.Context, fn func(context.Context, *SourceSyncStore) error) error {
	if fn == nil {
		return fmt.Errorf("runtime sync callback required")
	}
	run, release, err := d.admitRuntimeSync(ctx)
	if err != nil {
		return err
	}
	defer release()
	return d.runtimeSync(run, fn)
}

// BeginRuntimeSync admits synchronously before returning the actual completion.
// A later epoch cannot receive this operation, even through WithoutCancel.
func (d *ManagedLiveDomains) BeginRuntimeSync(ctx context.Context, fn func(context.Context, *SourceSyncStore) error) <-chan error {
	done := make(chan error, 1)
	if fn == nil {
		done <- fmt.Errorf("runtime sync callback required")
		close(done)
		return done
	}
	run, release, err := d.admitRuntimeSync(ctx)
	if err != nil {
		done <- err
		close(done)
		return done
	}
	go func() { defer close(done); defer release(); done <- d.runtimeSync(run, fn) }()
	return done
}

func (d *ManagedLiveDomains) ApplyRuntimeBatch(ctx context.Context, b ManagedLiveBatch, a *ManagedLiveAcquisition, fn func(context.Context, *Writer, ManagedLiveBlock) error) (ManagedLiveBatchResult, error) {
	if !d.mu.TryLock() {
		return ManagedLiveBatchResult{}, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	scope, err := d.runtimeScopeFor(ctx)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	scope.mu.Lock()
	sealed := scope.sealed
	scope.mu.Unlock()
	if sealed {
		return ManagedLiveBatchResult{}, fmt.Errorf("runtime epoch sealed")
	}
	d.runtimeBatch.Store(true)
	defer d.runtimeBatch.Store(false)
	run := context.WithValue(ctx, runtimeBatchKey{}, scope)
	native := context.WithValue(context.WithoutCancel(run), runtimeCommitContextKey{}, run)
	return d.live().ApplyBatch(native, b, a, func(w *Writer, block ManagedLiveBlock) error {
		if fn == nil {
			return run.Err()
		}
		err := fn(run, w, block)
		if err != nil {
			return err
		}
		return run.Err()
	})
}
func (d *ManagedLiveDomains) RuntimeCallbackContext(ctx context.Context) bool {
	return ctx.Value(runtimeIOKey{}) != nil || ctx.Value(runtimeBatchKey{}) != nil
}

func (d *ManagedLiveDomains) BeginRuntimeIO(ctx context.Context) (context.Context, func(), error) {
	return d.admitRuntimeSync(ctx)
}
