package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
	"reflect"
)

// RuntimeLearned contains only learned values. It never contains an RPC object,
// context, cache value, future, database binding or outstanding request.
type RuntimeLearned struct {
	columns  ColumnAccessPattern
	counts   map[string]uint64
	profiles map[string]*clientProfile
}
type RuntimeEpoch struct {
	domains    *store.ManagedLiveDomains
	context    context.Context
	scope      *store.ManagedRuntimeScope
	service    *Service
	database   *Database
	clients    *ClientSet
	requesters []*runtimeRequester
}

func NewRuntimeEpoch(ctx context.Context, d *store.ManagedLiveDomains, o Options, requesters []rpc.Requester, learned *RuntimeLearned) (*RuntimeEpoch, error) {
	if o.Database != nil || o.Clients != nil {
		return nil, fmt.Errorf("runtime owns database and clients")
	}
	run, scope, err := d.NewRuntimeScope(ctx)
	if err != nil {
		return nil, err
	}
	e := &RuntimeEpoch{domains: d, context: run, scope: scope}
	fail := func(err error) (*RuntimeEpoch, error) {
		scope.Cancel()
		_ = scope.Seal(context.Background())
		return nil, err
	}
	if len(requesters) != len(o.Build.Chains) {
		return fail(fmt.Errorf("runtime requester count mismatch"))
	}
	reqs := make([]rpc.Requester, len(requesters))
	caches := make([]rpc.Cache, len(requesters))
	for i, r := range requesters {
		if r == nil {
			return fail(fmt.Errorf("runtime requester missing"))
		}
		if _, ok := r.(rpc.AsyncRequester); !ok {
			return fail(fmt.Errorf("runtime requires async requester"))
		}
		n, err := clientInteger(o.Build.Chains[i]["id"])
		if err != nil || !n.IsUint64() {
			return fail(fmt.Errorf("runtime chain id invalid"))
		}
		rr := &runtimeRequester{domains: d, base: r}
		rr.actions = rpc.NewActions(rr)
		e.requesters = append(e.requesters, rr)
		reqs[i] = rr
		caches[i] = &runtimeCache{domains: d, chain: n.Uint64()}
	}
	e.clients, err = NewStrictClientSet(o.Build.Chains, reqs, caches...)
	if err != nil {
		return fail(err)
	}
	e.clients.managedRoots = newManagedClientRoots()
	if learned != nil {
		o.ColumnAccessPattern = cloneColumns(learned.columns)
		o.EventCount = map[string]uint64{}
		for k, v := range learned.counts {
			o.EventCount[k] = v
		}
		e.clients.profiles = cloneRuntimeProfiles(learned.profiles)
	}
	if o.Errors == nil {
		o.Errors = &ErrorState{}
	}
	e.database = NewRuntimeDatabase(o.Errors)
	o.Database = e.database
	o.Clients = e.clients
	e.service, err = NewService(o)
	if err != nil {
		_ = e.clients.Close(context.Background())
		return fail(err)
	}
	return e, nil
}
func (e *RuntimeEpoch) Context() context.Context      { return e.context }
func (e *RuntimeEpoch) Service() *Service             { return e.service }
func (e *RuntimeEpoch) Clients() *ClientSet           { return e.clients }
func (e *RuntimeEpoch) Requester(i int) rpc.Requester { return e.requesters[i] }
func (e *RuntimeEpoch) Cancel()                       { e.scope.Cancel() }
func (e *RuntimeEpoch) Join(ctx context.Context) error {
	return errors.Join(e.clients.WaitPrefetch(ctx), e.clients.WaitCacheWrites(ctx), e.clients.managedRoots.failure())
}
func (e *RuntimeEpoch) Seal(ctx context.Context) (*RuntimeLearned, error) {
	if err := e.clients.managedRoots.sealAndWait(ctx); err != nil {
		return nil, err
	}
	if err := e.Join(ctx); err != nil {
		return nil, err
	}
	if err := e.scope.Seal(ctx); err != nil {
		return nil, err
	}
	l := &RuntimeLearned{columns: cloneColumns(e.service.ColumnAccessPattern()), counts: map[string]uint64{}}
	for k, v := range e.service.EventCount() {
		l.counts[k] = v
	}
	e.clients.mu.RLock()
	l.profiles = cloneRuntimeProfiles(e.clients.profiles)
	e.clients.mu.RUnlock()
	if err := e.clients.Close(ctx); err != nil {
		return nil, err
	}
	return l, nil
}

// Abort joins work even when a handler/cache error prevents a normal seal.
func (e *RuntimeEpoch) Abort(ctx context.Context) error {
	e.Cancel()
	err := e.clients.Close(ctx)
	return errors.Join(err, e.scope.Seal(ctx))
}
func (e *RuntimeEpoch) Process(ctx context.Context, b store.ManagedLiveBatch, a *store.ManagedLiveAcquisition, events []Object, history bool) (result store.ManagedLiveBatchResult, err error) {
	if len(b.Blocks) != 1 {
		return store.ManagedLiveBatchResult{}, fmt.Errorf("runtime service dispatch requires exactly one block")
	}
	return e.processBlocks(ctx, b, a, [][]Object{events}, history)
}

// ProcessHistoricalBlocks preserves one source merged-output transaction while
// retaining per-block writer attribution inside that same native transaction.
func (e *RuntimeEpoch) ProcessHistoricalBlocks(ctx context.Context, b store.ManagedLiveBatch, a *store.ManagedLiveAcquisition, events [][]Object) (store.ManagedLiveBatchResult, error) {
	if len(b.Blocks) == 0 || len(events) != len(b.Blocks) {
		return store.ManagedLiveBatchResult{}, fmt.Errorf("historical merged blocks require exact event partitions")
	}
	return e.processBlocks(ctx, b, a, events, true)
}
func (e *RuntimeEpoch) processBlocks(ctx context.Context, b store.ManagedLiveBatch, a *store.ManagedLiveAcquisition, partitions [][]Object, history bool) (result store.ManagedLiveBatchResult, err error) {
	completed := map[string]uint64{}
	for k, v := range e.service.Metrics().Completed {
		completed[k] = v
	}
	settled := false
	defer func() {
		var stage *store.ManagedStageError
		unknown := errors.As(err, &stage) && stage.Outcome == "unknown"
		if !settled || (err != nil && !unknown) {
			for k := range e.service.Metrics().Completed {
				delete(e.service.Metrics().Completed, k)
			}
			for k, v := range completed {
				e.service.Metrics().Completed[k] = v
			}
		}
	}()
	index := 0
	result, err = e.domains.ApplyRuntimeBatch(ctx, b, a, func(run context.Context, w *store.Writer, block store.ManagedLiveBlock) error {
		if index >= len(partitions) || block != b.Blocks[index] {
			return fmt.Errorf("historical block callback order differs")
		}
		events := partitions[index]
		index++
		bound, binding, err := e.database.Bind(run, w)
		if err != nil {
			return err
		}
		defer binding.Revoke()
		if history {
			err = e.service.ProcessHistoricalEvents(bound, events, nil)
		} else {
			err = e.service.ProcessRealtimeEvents(bound, events)
		}
		return errors.Join(err, e.Join(bound))
	})
	settled = true
	return result, err
}
func cloneColumns(in ColumnAccessPattern) ColumnAccessPattern {
	if in == nil {
		return nil
	}
	out := ColumnAccessPattern{}
	for k, v := range in {
		if v == nil {
			continue
		}
		p := *v
		p.Block = append([]string(nil), v.Block...)
		p.Transaction = append([]string(nil), v.Transaction...)
		p.TransactionReceipt = append([]string(nil), v.TransactionReceipt...)
		p.Trace = append([]string(nil), v.Trace...)
		out[k] = &p
	}
	return out
}
func cloneRuntimeProfiles(in map[string]*clientProfile) map[string]*clientProfile {
	out := map[string]*clientProfile{}
	for k, v := range in {
		p := &clientProfile{entries: map[string]*ProfileEntry{}, order: append([]string(nil), v.order...), constants: append([]string(nil), v.constants...)}
		for key, entry := range v.entries {
			copy := *entry
			if entry.Recorded != nil {
				copy.Recorded = cloneRuntimeValue(reflect.ValueOf(entry.Recorded)).Interface().(*RecordedProfile)
			}
			p.entries[key] = &copy
		}
		out[k] = p
	}
	return out
}
func cloneRuntimeValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	if v.CanInterface() {
		if filter.IsUndefined(v.Interface()) {
			return v
		}
		if n, ok := v.Interface().(*big.Int); ok {
			if n == nil {
				return v
			}
			return reflect.ValueOf(new(big.Int).Set(n))
		}
		if n, ok := v.Interface().(big.Int); ok {
			return reflect.ValueOf(*new(big.Int).Set(&n))
		}
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		o := reflect.New(v.Type()).Elem()
		o.Set(cloneRuntimeValue(v.Elem()))
		return o
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		o := reflect.New(v.Type().Elem())
		o.Elem().Set(cloneRuntimeValue(v.Elem()))
		return o
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		o := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			o.SetMapIndex(it.Key(), cloneRuntimeValue(it.Value()))
		}
		return o
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		o := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			o.Index(i).Set(cloneRuntimeValue(v.Index(i)))
		}
		return o
	case reflect.Array:
		o := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			o.Index(i).Set(cloneRuntimeValue(v.Index(i)))
		}
		return o
	case reflect.Struct:
		o := reflect.New(v.Type()).Elem()
		o.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if o.Field(i).CanSet() && v.Field(i).CanInterface() {
				o.Field(i).Set(cloneRuntimeValue(v.Field(i)))
			}
		}
		return o
	}
	return v
}

type runtimeRequester struct {
	domains *store.ManagedLiveDomains
	base    rpc.Requester
	actions *rpc.Actions
}

func (r *runtimeRequester) Actions() *rpc.Actions { return r.actions }
func (r *runtimeRequester) Call(ctx context.Context, m string, p, out any) error {
	run, finish, err := r.domains.BeginRuntimeIO(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return r.base.Call(run, m, p, out)
}
func (r *runtimeRequester) BeginCall(ctx context.Context, m string, p, out any) <-chan error {
	result := make(chan error, 1)
	run, finish, err := r.domains.BeginRuntimeIO(ctx)
	if err != nil {
		result <- err
		close(result)
		return result
	}
	done := r.base.(rpc.AsyncRequester).BeginCall(run, m, p, out)
	go func() {
		defer finish()
		defer close(result)
		if done == nil {
			result <- fmt.Errorf("nil RPC completion")
		} else {
			result <- <-done
		}
	}()
	return result
}

type runtimeCache struct {
	domains *store.ManagedLiveDomains
	chain   uint64
}

func (c *runtimeCache) CacheGet(ctx context.Context, key string) (raw json.RawMessage, found bool, err error) {
	err = c.domains.WithRuntimeSync(ctx, func(run context.Context, s *store.SourceSyncStore) error {
		var e error
		raw, found, e = s.RPCRequestCache(c.chain).CacheGet(run, key)
		return e
	})
	return
}
func (c *runtimeCache) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	return c.domains.WithRuntimeSync(ctx, func(run context.Context, s *store.SourceSyncStore) error {
		return s.RPCRequestCache(c.chain).CachePut(run, key, value)
	})
}

func (c *runtimeCache) BeginCachePut(ctx context.Context, key string, value json.RawMessage) <-chan error {
	return c.domains.BeginRuntimeCachePut(ctx, c.chain, key, value)
}
func (c *runtimeCache) BeginCacheGet(ctx context.Context, key string) <-chan rpc.CacheLookupResult {
	return c.domains.BeginRuntimeCacheGet(ctx, c.chain, key)
}
