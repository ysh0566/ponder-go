package indexing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

// HistoricalSync is a revocable acquisition/refetch capability, not a raw sync
// store getter. It can overlap one sequential business binding. Every operation
// is joined when its enclosing WithHistoricalSync callback finishes; escaped
// handles and requesters reject later work. The caller must stop/join iterators
// before returning, including their background next-range prefetch.
type historicalScopeKey struct{}
type historicalPersistenceLeaseKey struct{}

// A token is an owner-issued identity, not a caller-supplied cancellation source.
// Wrapping it with WithoutCancel/WithValue does not mint another valid token.
type historicalPersistenceToken struct {
	context.Context
	scope *HistoricalSync
}

type historicalPersistenceLease struct {
	scope  *HistoricalSync
	gate   *ownedGate
	active bool // guarded by scope.lifecycleMu
}

type HistoricalSync struct {
	owner       *Owned
	store       *store.SourceSyncStore
	ctx         context.Context
	cancel      context.CancelFunc
	gate        *ownedGate
	done        chan struct{}
	lifecycleMu sync.Mutex
	sealed      atomic.Bool
	token       *historicalPersistenceToken
	requesterMu sync.Mutex
	requesters  map[uint64]rpc.Requester
}

// WithHistoricalSync owns one finite extraction session. Close cancels this
// session before waiting for its callback and admitted I/O, then closes clients
// and storage. A Close deadline never releases storage beneath unfinished work.
func (o *Owned) WithHistoricalSync(ctx context.Context, maxQueryParameters int, fn func(context.Context, *HistoricalSync) error) (err error) {
	if fn == nil {
		return fmt.Errorf("historical sync callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	if o.closing || o.managedSealed || o.historical != nil || o.active {
		o.mu.Unlock()
		return fmt.Errorf("owned historical sync is closed or already active")
	}
	source, err := o.storage.SyncCache().SourceSyncStore(maxQueryParameters)
	if err != nil {
		o.mu.Unlock()
		return err
	}
	run, cancel := context.WithCancel(ctx)
	scope := &HistoricalSync{owner: o, store: source, ctx: run, cancel: cancel, gate: newOwnedGate(), done: make(chan struct{})}
	scope.token = &historicalPersistenceToken{Context: context.WithValue(run, historicalScopeKey{}, scope), scope: scope}
	o.historical = scope
	o.mu.Unlock()
	defer func() {
		// Seal NEW roots, not the continuation rights of an admitted lease.
		// In particular, lookup completion can still admit RPC and a completed
		// handler request can still enqueue its already-authorized cache write.
		scope.lifecycleMu.Lock()
		scope.sealed.Store(true)
		scope.lifecycleMu.Unlock()
		err = errors.Join(err, o.clients.WaitPrefetch(context.Background()), o.clients.WaitCacheWrites(context.Background()))
		waitOwnedGate(scope.gate)
		// A persistence callback admitted before sealing may itself have admitted
		// a client job. Its descendants must also settle before local cleanup.
		err = errors.Join(err, o.clients.WaitPrefetch(context.Background()), o.clients.WaitCacheWrites(context.Background()))
		// Capture ONLY actual caller/owner cancellation. Sampling after cancel()
		// would turn every ordinary return/error into a fake context.Canceled.
		err = errors.Join(err, o.clients.managedRoots.failure())
		genuineCancellation := scope.ctx.Err()
		err = errors.Join(err, genuineCancellation)
		cancel()
		scope.gate.close() // now empty; never cancels admitted work on normal return
		o.mu.Lock()
		o.historical = nil
		close(scope.done)
		o.mu.Unlock()
	}()
	return fn(scope.token.Context, scope)
}

// waitOwnedGate joins without sealing or cancelling descendants. Its caller
// must prevent new roots; active persistence leases keep the gate nonempty until
// all their permitted children have settled.
func waitOwnedGate(g *ownedGate) {
	g.mu.Lock()
	done := g.done
	g.mu.Unlock()
	<-done
}

// PersistenceContext returns an opaque canonical identity for this live scope.
// It exposes neither storage nor authority to replace caller deadlines/Close.
func (s *HistoricalSync) PersistenceContext() context.Context {
	if s == nil {
		return nil
	}
	return s.token
}

// WithHistoricalPersistenceContext admits one persistence root atomically with
// scope sealing. ioCtx survives INTERNAL scheduling cancellation; workCtx still
// observes it. Both retain the same child permit and actual caller/owner lifetime.
// The callback must join its own goroutines, just like a transaction callback.
func (s *HistoricalSync) WithHistoricalPersistenceContext(schedule context.Context, token context.Context, fn func(ioCtx, workCtx context.Context) error) error {
	if s == nil || s.gate == nil || schedule == nil || fn == nil {
		return fmt.Errorf("historical persistence scope, schedule, and callback required")
	}
	if owner, _ := schedule.Value(historicalScopeKey{}).(*HistoricalSync); owner != s {
		return fmt.Errorf("historical persistence schedule has wrong owner or scope")
	}
	identity, ok := token.(*historicalPersistenceToken)
	if !ok || identity == nil || identity != s.token || identity.scope != s {
		return fmt.Errorf("historical persistence token has wrong owner or scope")
	}
	s.lifecycleMu.Lock()
	if s.sealed.Load() {
		s.lifecycleMu.Unlock()
		return fmt.Errorf("historical persistence scope is sealed")
	}
	if err := schedule.Err(); err != nil {
		s.lifecycleMu.Unlock()
		return err
	}
	if err := s.ctx.Err(); err != nil {
		s.lifecycleMu.Unlock()
		return err
	}
	_, releaseRoot, err := s.gate.begin(s.ctx)
	if err != nil {
		s.lifecycleMu.Unlock()
		return err
	}
	lease := &historicalPersistenceLease{scope: s, gate: newOwnedGate(), active: true}
	s.lifecycleMu.Unlock()
	ioCtx := context.WithValue(identity.Context, historicalPersistenceLeaseKey{}, lease)
	work, cancelWork := context.WithCancel(schedule)
	stopWork := context.AfterFunc(s.ctx, cancelWork)
	workCtx := context.WithValue(work, historicalPersistenceLeaseKey{}, lease)
	defer func() {
		s.lifecycleMu.Lock()
		lease.active = false
		s.lifecycleMu.Unlock()
		// The callback has returned; no new child can enter through this lease.
		// Already admitted child futures are joined without cancelling them.
		waitOwnedGate(lease.gate)
		lease.gate.close()
		stopWork()
		cancelWork()
		releaseRoot()
	}()
	return fn(ioCtx, workCtx)
}

func (s *HistoricalSync) begin(ctx context.Context) (context.Context, func(), error) {
	if s == nil || s.gate == nil || ctx == nil {
		return nil, nil, fmt.Errorf("historical sync scope is missing")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	lease, _ := ctx.Value(historicalPersistenceLeaseKey{}).(*historicalPersistenceLease)
	if lease != nil {
		if lease.scope != s || !lease.active {
			return nil, nil, fmt.Errorf("historical persistence child permit is revoked or foreign")
		}
	} else if s.sealed.Load() {
		return nil, nil, fmt.Errorf("historical sync scope is sealed")
	}
	if err := s.ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	merged, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	run, release, err := s.gate.begin(merged)
	if err != nil {
		stop()
		cancel()
		return nil, nil, err
	}
	childRelease := func() {}
	if lease != nil {
		run, childRelease, err = lease.gate.begin(run)
		if err != nil {
			release()
			stop()
			cancel()
			return nil, nil, err
		}
	}
	return run, func() { childRelease(); release(); stop(); cancel() }, nil
}

func (s *HistoricalSync) GetIntervals(ctx context.Context, filters []filter.Object) (*store.SourceIntervalMap, error) {
	run, release, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.store.GetIntervals(run, filters)
}
func (s *HistoricalSync) GetChildAddresses(ctx context.Context, factory filter.Object) (*store.SourceAddressMap, error) {
	run, release, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.store.GetChildAddresses(run, factory)
}
func (s *HistoricalSync) GetEventData(ctx context.Context, args store.SourceEventDataArgs) (store.SourceEventData, error) {
	run, release, err := s.begin(ctx)
	if err != nil {
		return store.SourceEventData{}, err
	}
	defer release()
	return s.store.GetEventData(run, args)
}
func (s *HistoricalSync) GetSafeCrashRecoveryBlock(ctx context.Context, chain uint64, timestamp int64) (*store.SourceRecoveryBlock, error) {
	run, release, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.store.GetSafeCrashRecoveryBlock(run, chain, timestamp)
}
func (s *HistoricalSync) WithTransaction(ctx context.Context, fn func(*store.SourceSyncStore) error) error {
	run, release, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.store.WithTransaction(run, fn)
}

// Requester is the actual configured transport through both acquisition-scope
// and owner gates. It advertises AsyncRequester only when the original transport
// has real synchronous admission; it never invents that contract around Call.
func (s *HistoricalSync) Requester(chainID uint64) (rpc.Requester, error) {
	if s == nil || s.owner == nil {
		return nil, fmt.Errorf("historical sync scope is missing")
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if s.sealed.Load() {
		return nil, fmt.Errorf("historical sync scope is sealed")
	}

	s.requesterMu.Lock()
	defer s.requesterMu.Unlock()
	if requester, ok := s.requesters[chainID]; ok {
		return requester, nil
	}
	for i, chain := range s.owner.service.options.Build.Chains {
		id, err := clientInteger(chain["id"])
		if err != nil || !id.IsUint64() {
			return nil, fmt.Errorf("invalid configured chain")
		}
		if id.Uint64() == chainID {
			r := &historicalRequester{scope: s, base: s.owner.requesters[i]}
			var requester rpc.Requester = r
			if _, ok := r.base.base.(rpc.AsyncRequester); ok {
				requester = &historicalAsyncRequester{r}
			}
			r.actions = r.base.actionOwner().WithRequester(requester)
			if s.requesters == nil {
				s.requesters = map[uint64]rpc.Requester{}
			}
			s.requesters[chainID] = requester
			return requester, nil
		}
	}
	return nil, fmt.Errorf("historical requester chain not configured")
}

type historicalRequester struct {
	scope   *HistoricalSync
	base    *ownedRequester
	actions *rpc.Actions
}

// Actions keeps the original RPC owner's learning but fixes every action call
// to this scope's revocable transport. Returning the base Actions directly
// would bypass both acquisition cancellation and Close's I/O joining.
func (r *historicalRequester) Actions() *rpc.Actions { return r.actions }

func (r *historicalRequester) Call(ctx context.Context, method string, params, out any) error {
	run, release, err := r.scope.begin(ctx)
	if err != nil {
		return err
	}
	defer release()
	return r.base.Call(run, method, params, out)
}

type historicalAsyncRequester struct{ *historicalRequester }

func (r *historicalAsyncRequester) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	result := make(chan error, 1)
	run, release, err := r.scope.begin(ctx)
	if err != nil {
		result <- err
		close(result)
		return result
	}
	// Admission is synchronous in the caller; completion alone is awaited in a
	// goroutine. Even a canceled caller must retain the actual provider future.
	pending := r.base.BeginCall(run, method, params, out)
	go func() {
		defer release()
		err, ok := <-pending
		if !ok {
			err = fmt.Errorf("historical requester completion closed without result")
		}
		result <- err
		close(result)
	}()
	return result
}

// Dialect comes from the constructor-owned backend, not a runner-supplied flag.
func (o *Owned) Dialect() string { return o.storage.BusinessStore().Backend.Dialect() }

func (o *Owned) HistoricalSnapshot(ctx context.Context) (store.HistoricalSnapshot, error) {
	release, err := o.enterWork(ctx)
	if err != nil {
		return store.HistoricalSnapshot{}, err
	}
	defer release()
	configured, err := o.configuredChains()
	if err != nil {
		return store.HistoricalSnapshot{}, err
	}
	snapshot, err := o.storage.BusinessActions().HistoricalSnapshot(ctx)
	if err != nil {
		return store.HistoricalSnapshot{}, err
	}
	if snapshot.Initialized {
		if len(snapshot.Chains) != len(configured) {
			return store.HistoricalSnapshot{}, fmt.Errorf("durable chain configuration mismatch")
		}
		for _, chain := range snapshot.Chains {
			if name, ok := configured[chain.ChainID]; !ok || name != chain.ChainName {
				return store.HistoricalSnapshot{}, fmt.Errorf("durable chain configuration mismatch")
			}
		}
	}
	return snapshot, nil
}

// Invalidate drops reusable row/buffer state while preserving learned profiles.
// It cannot race a business binding or row prefetch and does not rewind sync.
func (o *Owned) Invalidate(ctx context.Context) error {
	release, err := o.enterWork(ctx)
	if err != nil {
		return err
	}
	defer release()
	return o.database.session.Invalidate()
}
