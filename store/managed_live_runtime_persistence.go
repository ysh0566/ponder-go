package store

import (
	"context"
	"errors"
)

// Runtime SQL finishes under the admitted operation's native transaction
// lifetime. The callback still receives the cancellable work context. This
// prevents database/sql cancellation from discarding a lifetime-lock connection
// while an ordinary owner cancellation is rolling back its transaction.
type runtimePersistenceTransaction struct {
	Transaction
	context context.Context
}

func (t *runtimePersistenceTransaction) Exec(_ context.Context, q string, args ...any) error {
	return t.Transaction.Exec(t.context, q, args...)
}
func (t *runtimePersistenceTransaction) Query(_ context.Context, q string, args ...any) (Result, error) {
	return t.Transaction.Query(t.context, q, args...)
}
func (d *ManagedLiveDomains) runtimePersist(ctx context.Context, fn func(context.Context, *SourceSyncStore) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := d.storage.syncCache.SourceSyncStore(0)
	if err != nil {
		return err
	}
	native := context.WithoutCancel(ctx)
	return source.WithTransaction(native, func(bound *SourceSyncStore) error {
		bound.bound.mu.Lock()
		bound.bound.tx = &runtimePersistenceTransaction{Transaction: bound.bound.tx, context: native}
		bound.bound.mu.Unlock()
		return errors.Join(fn(ctx, bound), ctx.Err())
	})
}
