package indexing

import (
	"context"
	"sync"

	"ponder.local/ponder-go/store"
)

type databaseBindingKey struct{}

// DatabaseBinding is a single immutable transaction capability. Revoke ends
// callback access without cancelling its context or independent RPC/cache jobs.
// Finish must be called after the enclosing SQL transaction has settled, passing
// its actual result (including an uncertain commit error).
type DatabaseBinding struct {
	database   *Database
	underlying *store.IndexingStore
	finishMu   sync.Mutex
	operations sync.WaitGroup
	finished   bool
	finishErr  error
}

// NewSessionDatabase creates a stable facade before any SQL transaction exists.
// Underlying returns nil for this facade; transaction handles cannot be obtained
// without the private callback capability through the facade's operations.
func NewSessionDatabase(session *store.IndexingSession, handler RetryableErrorHandler, classifiers ...DatabaseErrorClassifier) *Database {
	db := NewDatabase(nil, handler, classifiers...)
	db.session = session
	return db
}

func invalidBinding() error {
	return &store.NonRetryableUserError{Message: "indexing database requires its active transaction context"}
}
func (db *Database) configured() bool {
	return db != nil && (db.underlying != nil || db.session != nil)
}

// acquire retains the binding lease through the entire operation, so revocation
// joins operations already using it before allowing a subsequent generation.
func (db *Database) acquire(ctx context.Context) (*store.IndexingStore, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if db.session == nil {
		if db.underlying == nil {
			return nil, nil, invalidBinding()
		}
		return db.underlying, func() {}, nil
	}
	db.bindingMu.Lock()
	binding, _ := ctx.Value(databaseBindingKey{}).(*DatabaseBinding)
	if binding == nil || binding.database != db || db.binding != binding {
		db.bindingMu.Unlock()
		return nil, nil, invalidBinding()
	}
	binding.operations.Add(1)
	db.bindingMu.Unlock()
	return binding.underlying, binding.operations.Done, nil
}
func (db *Database) checkBinding(ctx context.Context) error {
	_, release, err := db.acquire(ctx)
	if err == nil {
		release()
	}
	return err
}

func (db *Database) Bind(ctx context.Context, writer *store.Writer) (context.Context, *DatabaseBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	db.bindingMu.Lock()
	defer db.bindingMu.Unlock()
	if db.session == nil || db.binding != nil {
		return nil, nil, invalidBinding()
	}
	underlying, err := db.session.Bind(writer)
	if err != nil {
		return nil, nil, err
	}
	binding := &DatabaseBinding{database: db, underlying: underlying}
	db.binding = binding
	return context.WithValue(ctx, databaseBindingKey{}, binding), binding, nil
}
func (binding *DatabaseBinding) Revoke() {
	db := binding.database
	db.bindingMu.Lock()
	if db.binding == binding {
		db.binding = nil
	}
	db.bindingMu.Unlock()
	binding.operations.Wait()
}
func (binding *DatabaseBinding) Finish(transactionErr error) error {
	binding.Revoke()
	binding.finishMu.Lock()
	defer binding.finishMu.Unlock()
	if binding.finished {
		return binding.finishErr
	}
	binding.finishErr = binding.database.session.Finish(binding.underlying, transactionErr)
	if binding.finishErr == nil {
		binding.finished = true
	}
	return binding.finishErr
}

// WithReadOnly permits outside-transaction row prefetch/read work only through
// the session's explicit read-only binding. It does not permit business writes.
func (db *Database) WithReadOnly(ctx context.Context, chain uint64, fn func(context.Context) error) error {
	if db.session == nil || fn == nil {
		return invalidBinding()
	}
	if db.readOnlyScope != nil {
		release, err := db.readOnlyScope()
		if err != nil {
			return err
		}
		defer release()
	}
	return db.session.WithReadOnly(ctx, chain, func(underlying *store.IndexingStore) error {
		db.bindingMu.Lock()
		if db.binding != nil {
			db.bindingMu.Unlock()
			return invalidBinding()
		}
		binding := &DatabaseBinding{database: db, underlying: underlying}
		db.binding = binding
		db.bindingMu.Unlock()
		defer binding.Revoke()
		return fn(context.WithValue(ctx, databaseBindingKey{}, binding))
	})
}

// Prefetch fills the current binding's row cache. Outside a business transaction
// it must run inside WithReadOnly; it never implicitly opens a write binding.
func (db *Database) Prefetch(ctx context.Context, events []store.ProfileEvent) error {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return db.capture(err)
	}
	defer release()
	return db.capture(underlying.Cache.Prefetch(ctx, events))
}
