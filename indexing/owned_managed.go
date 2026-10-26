package indexing

import (
	"context"
	"fmt"

	"ponder.local/ponder-go/store"
)

// Managed constructors are separate namespace formats. They never adopt an
// existing unmanaged owner or the legacy Lifecycle recovery protocol.
func OpenManagedOwnedPostgres(ctx context.Context, o OwnedOptions, managed store.ManagedFiniteOptions) (*Owned, error) {
	owner, err := openOwned(ctx, o, func(ctx context.Context, storage store.IndependentStorageOptions) (*store.IndependentStorage, error) {
		return store.OpenManagedIndependentPostgres(ctx, storage, managed)
	})
	if err == nil {
		owner.clients.managedRoots = newManagedClientRoots()
	}
	return owner, err
}
func OpenManagedOwnedPGlite(ctx context.Context, o OwnedOptions, managed store.ManagedFiniteOptions) (*Owned, error) {
	owner, err := openOwned(ctx, o, func(ctx context.Context, storage store.IndependentStorageOptions) (*store.IndependentStorage, error) {
		return store.OpenManagedIndependentPGlite(ctx, storage, managed)
	})
	if err == nil {
		owner.clients.managedRoots = newManagedClientRoots()
	}
	return owner, err
}

// ActivateManagedFinite seals every new business/source root before activating
// the durable endpoint. It is a one-way, once-per-owner transition even when
// activation fails: recovery requires closing and reopening the managed owner.
// The caller must have received successful outer Run return, not only its
// callback's Completed flag. The store independently checks durable endpoints.
func (o *Owned) ActivateManagedFinite(ctx context.Context, expected []store.CheckpointState) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	m := o.storage.ManagedFinite()
	if m == nil {
		return fmt.Errorf("owner is not managed finite")
	}
	o.mu.Lock()
	if o.closing || o.managedSealed || o.active || o.historical != nil {
		o.mu.Unlock()
		return fmt.Errorf("managed activation requires a joined, unbound owner")
	}
	run, cancel := context.WithCancel(ctx)
	o.managedSealed, o.active = true, true
	o.activeDone = make(chan struct{})
	o.managedCancel = cancel
	o.mu.Unlock()
	defer func() {
		cancel()
		o.mu.Lock()
		o.managedCancel = nil
		o.active = false
		close(o.activeDone)
		o.mu.Unlock()
	}()
	if err = o.clients.managedRoots.sealAndWait(run); err != nil {
		return err
	}
	if err = o.clients.WaitPrefetch(run); err != nil {
		return err
	}
	if err = o.clients.WaitCacheWrites(run); err != nil {
		return err
	}
	if err = o.clients.managedRoots.failure(); err != nil {
		return err
	}
	if err = o.clients.Close(run); err != nil {
		return err
	}
	for _, requester := range o.requesters {
		requester.gate.close()
	}
	for _, cache := range o.caches {
		cache.gate.close()
	}
	if err = o.database.session.Invalidate(); err != nil {
		return err
	}
	return m.Activate(run, append([]store.CheckpointState(nil), expected...))
}

// Managed metadata uses the restricted store control path. It cannot mutate
// business state or reopen sealed indexing roots.
func (o *Owned) ManagedMetadata(ctx context.Context) (store.AppMetadata, error) {
	m := o.storage.ManagedFinite()
	if m == nil {
		return store.AppMetadata{}, fmt.Errorf("owner is not managed finite")
	}
	return m.Metadata(ctx)
}
func (o *Owned) ManagedReady(ctx context.Context) (bool, error) {
	m := o.storage.ManagedFinite()
	if m == nil {
		return false, fmt.Errorf("owner is not managed finite")
	}
	return m.Ready(ctx)
}
