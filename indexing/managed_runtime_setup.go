package indexing

import (
	"context"
	"errors"
	"ponder.local/ponder-go/store"
)

// Bootstrap binds the actual setup service to one all-chain native transaction.
// Pending RPC/cache work settles before the business commit is attempted.
func (e *RuntimeEpoch) Bootstrap(ctx context.Context, b store.ManagedLiveBatch, a *store.ManagedLiveAcquisition) (store.ManagedLiveBatchResult, error) {
	return e.domains.ApplyRuntimeBootstrap(ctx, b, a, func(run context.Context, w *store.Writer, attribute func(uint64) error) error {
		bound, binding, err := e.database.Bind(run, w)
		if err != nil {
			return err
		}
		defer binding.Revoke()
		err = e.service.processSetupEvents(bound, attribute)
		return errors.Join(err, e.Join(bound))
	})
}
