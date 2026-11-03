package historical

import (
	"context"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// Each operation re-enters the owned sync lane. A transaction-bound source
// store never escapes a callback or remains held across an indexing yield.
type runtimeHistoricalStore struct{ domains *store.ManagedLiveDomains }

func (s runtimeHistoricalStore) GetIntervals(ctx context.Context, filters []filter.Object) (out *store.SourceIntervalMap, err error) {
	err = s.domains.WithRuntimeSync(ctx, func(io context.Context, b *store.SourceSyncStore) error {
		out, err = b.GetIntervals(io, filters)
		return err
	})
	return
}
func (s runtimeHistoricalStore) GetChildAddresses(ctx context.Context, f filter.Object) (out *store.SourceAddressMap, err error) {
	err = s.domains.WithRuntimeSync(ctx, func(io context.Context, b *store.SourceSyncStore) error {
		out, err = b.GetChildAddresses(io, f)
		return err
	})
	return
}
func (s runtimeHistoricalStore) GetEventData(ctx context.Context, a store.SourceEventDataArgs) (out store.SourceEventData, err error) {
	err = s.domains.WithRuntimeSync(ctx, func(io context.Context, b *store.SourceSyncStore) error { out, err = b.GetEventData(io, a); return err })
	return
}
func (s runtimeHistoricalStore) GetSafeCrashRecoveryBlock(ctx context.Context, id uint64, ts int64) (out *store.SourceRecoveryBlock, err error) {
	err = s.domains.WithRuntimeSync(ctx, func(io context.Context, b *store.SourceSyncStore) error {
		out, err = b.GetSafeCrashRecoveryBlock(io, id, ts)
		return err
	})
	return
}
func (s runtimeHistoricalStore) WithTransaction(ctx context.Context, fn func(*store.SourceSyncStore) error) error {
	return s.domains.WithRuntimeSync(ctx, func(io context.Context, b *store.SourceSyncStore) error { return b.WithTransaction(io, fn) })
}
