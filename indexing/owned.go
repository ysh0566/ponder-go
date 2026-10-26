package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

// OwnedOptions constructs both storage domains; arbitrary backend/cache pairs
// cannot establish the isolation required by a handler transaction.
type OwnedOptions struct {
	Storage    store.IndependentStorageOptions
	Service    Options
	Requesters []rpc.Requester
	Cache      store.CacheOptions
}

// Owned is a finite transaction-binding owner, not a historical/realtime runner.
// Clients, service, and database identities persist across batches. Close seals
// new batches, lets the admitted batch finish, then cancels and joins independent
// RPC/cache work before closing either backend. It never cancels a normal batch
// context merely because its SQL transaction committed or rolled back.
type Owned struct {
	storage       *store.IndependentStorage
	service       *Service
	clients       *ClientSet
	database      *Database
	caches        []*ownedCache
	requesters    []*ownedRequester
	mu            sync.Mutex
	active        bool
	activeDone    chan struct{}
	closing       bool
	closeDone     chan struct{}
	closeErr      error
	managedSealed bool
	managedCancel context.CancelFunc
	historical    *HistoricalSync
}

func OpenOwnedPostgres(ctx context.Context, o OwnedOptions) (*Owned, error) {
	return openOwned(ctx, o, store.OpenIndependentPostgres)
}
func OpenOwnedPGlite(ctx context.Context, o OwnedOptions) (*Owned, error) {
	return openOwned(ctx, o, store.OpenIndependentPGlite)
}
func openOwned(ctx context.Context, o OwnedOptions, open func(context.Context, store.IndependentStorageOptions) (*store.IndependentStorage, error)) (*Owned, error) {
	if o.Service.Database != nil || o.Service.Clients != nil {
		return nil, fmt.Errorf("owned indexing constructs its own database and clients")
	}
	// Validate requester/chain shape before any persistent storage is opened.
	if _, err := NewClientSet(o.Service.Build.Chains, o.Requesters); err != nil {
		return nil, err
	}
	for _, chain := range o.Service.Build.Chains {
		n, err := clientInteger(chain["id"])
		if err != nil || !n.IsUint64() {
			return nil, fmt.Errorf("owned cache requires uint64 chain id")
		}
	}
	storage, err := open(ctx, o.Storage)
	if err != nil {
		return nil, err
	}
	owner := &Owned{storage: storage, activeDone: make(chan struct{})}
	close(owner.activeDone)
	fail := func(err error) (*Owned, error) { _ = owner.closeResources(); return nil, err }
	caches := make([]rpc.Cache, len(o.Requesters))
	requesters := make([]rpc.Requester, len(o.Requesters))
	for i, chain := range o.Service.Build.Chains {
		n, _ := clientInteger(chain["id"])
		c := &ownedCache{base: storage.SyncCache().AsyncSourceRPCRequestCache(n.Uint64()), gate: newOwnedGate()}
		r := &ownedRequester{base: o.Requesters[i], gate: newOwnedGate()}
		owner.caches = append(owner.caches, c)
		owner.requesters = append(owner.requesters, r)
		caches[i] = c
		requesters[i] = r
	}
	owner.clients, err = NewStrictClientSet(o.Service.Build.Chains, requesters, caches...)
	if err != nil {
		return fail(err)
	}
	if o.Service.Errors == nil {
		o.Service.Errors = &ErrorState{}
	}
	owner.database = NewSessionDatabase(storage.BusinessActions().NewIndexingSession(o.Cache), o.Service.Errors)
	owner.database.readOnlyScope = owner.enterWork
	o.Service.Database = owner.database
	o.Service.Clients = owner.clients
	owner.service, err = NewService(o.Service)
	if err != nil {
		return fail(err)
	}
	return owner, nil
}
func (o *Owned) Service() *Service   { return o.service }
func (o *Owned) Database() *Database { return o.database }
func (o *Owned) Clients() *ClientSet { return o.clients }

// Batch binds a real journaled business transaction. The callback may dispatch
// setup/history handlers and assign checkpoints using the supplied Writer.
// Historical chooses HistoricalBatch's trigger setup, not an outer runner.
// As with Service dispatch, the callback must finish/join its own handler
// goroutines before returning; detached dispatch is not a supported batch API.
func (o *Owned) Batch(ctx context.Context, chain uint64, historical bool, fn func(context.Context, *Service, *store.Writer) error) (err error) {
	if fn == nil {
		return fmt.Errorf("owned batch callback is required")
	}
	if _, err = o.enterWork(ctx); err != nil {
		return err
	}
	var binding *DatabaseBinding
	defer func() {
		if p := recover(); p != nil {
			if binding != nil {
				binding.Revoke()
				_ = binding.Finish(fmt.Errorf("batch panicked"))
			}
			o.endBatch()
			panic(p)
		}
		if binding != nil {
			err = errors.Join(err, binding.Finish(err))
		}
		o.endBatch()
	}()
	run := o.storage.BusinessActions().RawBatch
	if historical {
		run = o.storage.BusinessActions().HistoricalBatch
	}
	return run(ctx, chain, func(w *store.Writer) error {
		bound, b, e := o.database.Bind(ctx, w)
		if e != nil {
			return e
		}
		binding = b
		defer binding.Revoke()
		return fn(bound, o.service, w)
	})
}
func (o *Owned) enterWork(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	scope, _ := ctx.Value(historicalScopeKey{}).(*HistoricalSync)
	if o.closing || o.managedSealed || o.active || scope != o.historical || (scope != nil && scope.sealed.Load()) {
		return nil, fmt.Errorf("owned indexing is closed or already bound")
	}
	o.active = true
	o.activeDone = make(chan struct{})
	return o.endBatch, nil
}
func (o *Owned) endBatch() { o.mu.Lock(); o.active = false; close(o.activeDone); o.mu.Unlock() }

// Close's deadline bounds only this caller's wait. A single retained operation
// continues owning all unfinished work; subsequent Close calls join that same
// operation. Settled cache writes are not a claim of durable success.
func (o *Owned) Close(ctx context.Context) error {
	o.mu.Lock()
	if !o.closing {
		o.closing = true
		if o.clients != nil && o.clients.managedRoots != nil {
			o.clients.managedRoots.mu.Lock()
			o.clients.managedRoots.sealed = true
			o.clients.managedRoots.mu.Unlock()
		}
		o.closeDone = make(chan struct{})
		active := o.activeDone
		historical := o.historical
		if historical != nil {
			historical.cancel()
		}
		if o.managedCancel != nil {
			o.managedCancel()
		}
		go func() {
			if historical != nil {
				<-historical.done
			}
			<-active
			err := o.closeResources()
			o.mu.Lock()
			o.closeErr = err
			close(o.closeDone)
			o.mu.Unlock()
		}()
	}
	done := o.closeDone
	o.mu.Unlock()
	select {
	case <-done:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *Owned) closeResources() error {
	var result error
	if o.clients != nil && o.clients.managedRoots != nil {
		// Seal synchronously, but cancel/join physical I/O below before waiting
		// for escaped root calls, which may currently be blocked in that I/O.
		o.clients.managedRoots.mu.Lock()
		o.clients.managedRoots.sealed = true
		o.clients.managedRoots.mu.Unlock()
	}
	if o.clients != nil {
		result = errors.Join(result, o.clients.Close(context.Background()))
	}
	for _, r := range o.requesters {
		r.gate.close()
	}
	for _, c := range o.caches {
		c.gate.close()
		result = errors.Join(result, c.base.Close(context.Background()))
	}
	if o.clients != nil {
		result = errors.Join(result, o.clients.managedRoots.sealAndWait(context.Background()))
	}
	if o.storage != nil {
		result = errors.Join(result, o.storage.Close(context.Background()))
	}
	return result
}

// ownedGate additionally owns synchronous escaped calls, which the standalone
// adapter queue and ClientSet prefetch registries do not themselves track.
type ownedGate struct {
	mu      sync.Mutex
	closed  bool
	next    uint64
	pending map[uint64]context.CancelFunc
	done    chan struct{}
}

func newOwnedGate() *ownedGate {
	g := &ownedGate{pending: map[uint64]context.CancelFunc{}, done: make(chan struct{})}
	close(g.done)
	return g
}
func (g *ownedGate) begin(ctx context.Context) (context.Context, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, nil, fmt.Errorf("owned I/O is closed")
	}
	if len(g.pending) == 0 {
		g.done = make(chan struct{})
	}
	g.next++
	id := g.next
	child, cancel := context.WithCancel(ctx)
	g.pending[id] = cancel
	return child, func() {
		cancel()
		g.mu.Lock()
		delete(g.pending, id)
		if len(g.pending) == 0 {
			close(g.done)
		}
		g.mu.Unlock()
	}, nil
}
func (g *ownedGate) close() {
	g.mu.Lock()
	g.closed = true
	for _, cancel := range g.pending {
		cancel()
	}
	done := g.done
	g.mu.Unlock()
	<-done
}

type ownedRequester struct {
	base        rpc.Requester
	gate        *ownedGate
	actionsOnce sync.Once
	actions     *rpc.Actions
}

func (r *ownedRequester) actionOwner() *rpc.Actions {
	r.actionsOnce.Do(func() {
		if owner, ok := r.base.(interface{ Actions() *rpc.Actions }); ok {
			r.actions = owner.Actions()
		} else {
			r.actions = rpc.NewActions(r.base)
		}
	})
	return r.actions
}

func (r *ownedRequester) Call(ctx context.Context, method string, params, out any) error {
	ctx, finish, err := r.gate.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return r.base.Call(ctx, method, params, out)
}
func (r *ownedRequester) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	result := make(chan error, 1)
	ctx, finish, err := r.gate.begin(ctx)
	if err != nil {
		result <- err
		close(result)
		return result
	}
	if a, ok := r.base.(rpc.AsyncRequester); ok {
		done := a.BeginCall(ctx, method, params, out)
		go func() {
			defer finish()
			if done == nil {
				result <- fmt.Errorf("requester returned nil completion")
			} else {
				result <- <-done
			}
			close(result)
		}()
	} else {
		go func() { defer finish(); result <- r.base.Call(ctx, method, params, out); close(result) }()
	}
	return result
}

type ownedCache struct {
	base *store.AsyncSourceRPCRequestCache
	gate *ownedGate
}

func (c *ownedCache) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	ctx, finish, err := c.gate.begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer finish()
	return c.base.CacheGet(ctx, key)
}
func (c *ownedCache) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	ctx, finish, err := c.gate.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return c.base.CachePut(ctx, key, value)
}
func (c *ownedCache) BeginCachePut(ctx context.Context, key string, value json.RawMessage) <-chan error {
	return c.base.BeginCachePut(ctx, key, value)
}
func (c *ownedCache) BeginCacheGet(ctx context.Context, key string) <-chan rpc.CacheLookupResult {
	return c.base.BeginCacheGet(ctx, key)
}

var _ rpc.AsyncCache = (*ownedCache)(nil)
var _ rpc.AsyncCacheReader = (*ownedCache)(nil)
