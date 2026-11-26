package indexing

import (
	"context"
	"errors"
	"fmt"

	"github.com/ysh0566/ponder-go/store"
)

// Bootstrap binds the actual setup service to the chains admitted by this bootstrap transaction.
// Pending RPC/cache work settles before the business commit is attempted.
func (e *RuntimeEpoch) Bootstrap(ctx context.Context, b store.ManagedLiveBatch, a *store.ManagedLiveAcquisition) (store.ManagedLiveBatchResult, error) {
	return e.domains.ApplyRuntimeBootstrap(ctx, b, a, func(run context.Context, w *store.Writer, attribute func(uint64) error) error {
		bound, binding, err := e.database.Bind(run, w)
		if err != nil {
			return err
		}
		defer binding.Revoke()
		build := e.service.options.Build
		groups := make([][]Object, 0, len(b.Baselines))
		for i, group := range build.SetupCallbacks {
			if i >= len(build.Chains) {
				return fmt.Errorf("setup group has no configured chain")
			}
			id, err := clientInteger(build.Chains[i]["id"])
			if err != nil {
				return err
			}
			if _, ok := b.Baselines[id.Uint64()]; ok {
				groups = append(groups, group)
			}
		}
		err = e.service.processSetupGroups(bound, attribute, groups)
		return errors.Join(err, e.Join(bound))
	})
}
