package store

import (
	"context"
	"fmt"
	"sync"
)

// sourceSyncTransaction serializes access to the callback-owned transaction.
// This is required by the PGlite protocol, and also prevents an escaped handle
// from accessing a transaction after the callback has finished.
type sourceSyncTransaction struct {
	mu     sync.Mutex
	tx     Transaction
	closed bool
	failed error
}

// WithTransaction gives fn a callback-scoped store whose operations share one
// transaction. Outside this callback, the original store retains the source's
// per-batch commits. Nested calls join the same transaction, not a savepoint;
// a nested callback error or database error prevents the outer commit even if
// its caller ignores that error. Callers must join their goroutines before fn
// returns. Neither the bound store nor its RPC adapter may be used afterward.
// The existing store lease and transaction lock remain in force; this API does
// not imply that separate database transactions can run concurrently.
func (s *SourceSyncStore) WithTransaction(ctx context.Context, fn func(*SourceSyncStore) error) error {
	if s == nil || s.cache == nil {
		return fmt.Errorf("source sync store is not attached")
	}
	if fn == nil {
		return fmt.Errorf("source sync transaction callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.bound != nil {
		if err := s.scopeActive(ctx); err != nil {
			return err
		}
		err := fn(s)
		s.bound.mu.Lock()
		defer s.bound.mu.Unlock()
		if err != nil && s.bound.failed == nil {
			s.bound.failed = err
		}
		if s.bound.closed {
			return fmt.Errorf("source sync transaction is closed")
		}
		return s.bound.failed
	}
	return s.cache.transaction(ctx, func(tx Transaction) error {
		state := &sourceSyncTransaction{tx: tx}
		bound := &SourceSyncStore{cache: s.cache, maxQueryParameters: s.maxQueryParameters, bound: state}
		// Invalidate on every exit, including panic. The cache's transaction owner
		// rolls back when this callback does not complete successfully.
		defer func() { state.mu.Lock(); state.closed = true; state.mu.Unlock() }()
		err := fn(bound)
		state.mu.Lock()
		defer state.mu.Unlock()
		state.closed = true
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return state.failed
	})
}

// scopeActive intentionally changes no standalone empty-input semantics.
func (s *SourceSyncStore) scopeActive(ctx context.Context) error {
	if s.bound == nil {
		return nil
	}
	s.bound.mu.Lock()
	defer s.bound.mu.Unlock()
	if s.bound.closed {
		return fmt.Errorf("source sync transaction is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.cache.active(); err != nil {
		return err
	}
	return s.bound.failed
}

func (s *SourceSyncStore) transaction(ctx context.Context, fn func(Transaction) error) error {
	if s.bound == nil {
		return s.cache.transaction(ctx, fn)
	}
	state := s.bound
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return fmt.Errorf("source sync transaction is closed")
	}
	if state.failed != nil {
		return state.failed
	}
	if err := ctx.Err(); err != nil {
		state.failed = err
		return err
	}
	if err := s.cache.active(); err != nil {
		state.failed = err
		return err
	}
	if err := fn(state.tx); err != nil {
		state.failed = err
		return err
	}
	return nil
}
