package historical

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/finality"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	source "ponder.local/ponder-go/runtime"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
)

// RuntimeOptions selects the owned RuntimeRunner lifecycle. Default multichain
// supports its validated bounded/continuous source subsets. Omnichain uses a
// separate finalized, cached, interval1 block-callback format with source logical
// positions; unsupported combinations fail before storage DDL. BuildID identifies
// handler code; OpenID is unique per open. See docs/RUNTIME_SUPPORT.md.
type runtimeRunnerCallbackKey struct{}

type RuntimeOptions struct {
	// SourceCheckpoints opts into source initial positions for multichain.
	// The finalized omnichain format enables these positions automatically.
	SourceCheckpoints bool
	FactorySources    bool
	Live              *RuntimeLiveOptions
	// Historical.QuerySize is the bounded omnichain SQL target (1..12000);
	// zero uses12000. Other historical scheduling options remain unsupported.
	Historical                   Options
	BuildID, OpenID, ViewsSchema string
	// Requesters must construct fresh transport owners for every fetch epoch.
	Requesters  func(context.Context, uint64) ([]rpc.Requester, error)
	OnActivated func(context.Context) error
	// OnSetupCommitted observes authenticated setup commit before source events.
	OnSetupCommitted func(context.Context) error
}
type RuntimeResult struct {
	Recovered, Ready, Completed             bool
	HistoricalBlocks, LiveBlocks, Refetches int
}
type RuntimeRunner struct {
	domains           *store.ManagedLiveDomains
	setup             bool
	build             *config.ExecutableSourceIndexingBuild
	session           *filter.Session
	release           func()
	options           RuntimeOptions
	starts, ends      map[uint64]uint64
	latest, finalized map[uint64]store.ManagedLiveHead
	finalBlocks       []indexing.Object
	sequence          uint64
	active            atomic.Bool
	attempted         atomic.Bool
	failed            atomic.Bool
	transitioning     atomic.Bool
	mu                sync.Mutex
	closing           bool
	cancel            context.CancelFunc
	runDone           chan struct{}
	closeOnce         sync.Once
	closeDone         chan struct{}
	closeErr          error
}

func OpenRuntimePostgres(ctx context.Context, o RuntimeOptions) (*RuntimeRunner, error) {
	return openRuntime(ctx, o, store.OpenManagedLiveRuntimePostgres)
}
func OpenRuntimePGlite(ctx context.Context, o RuntimeOptions) (*RuntimeRunner, error) {
	return openRuntime(ctx, o, store.OpenManagedLiveRuntimePGlite)
}
func openRuntime(ctx context.Context, o RuntimeOptions, opener func(context.Context, store.IndependentStorageOptions, store.ManagedLiveRuntimeOptions) (*store.ManagedLiveDomains, error)) (*RuntimeRunner, error) {
	if o.Historical.Build == nil || o.BuildID == "" || o.OpenID == "" || o.Requesters == nil {
		return nil, fmt.Errorf("compiled build, BuildID, OpenID and fresh requester factory required")
	}
	if o.Historical.MaxQueryParameters != 0 || o.Historical.Clock != nil || o.Historical.RetryWait != nil || o.Historical.OnCommitted != nil {
		return nil, fmt.Errorf("runtime-v5 does not support historical scheduling, query or observer options")
	}
	if len(o.Historical.Owned.Requesters) != 0 || !reflect.ValueOf(o.Historical.Owned.Cache).IsZero() {
		return nil, fmt.Errorf("runtime-v5 requires its requester factory and per-block cache defaults")
	}
	if o.Live != nil {
		copy := *o.Live
		if copy.HeaderCacheCapacity < 0 || copy.HeaderCacheCapacity > 4096 {
			return nil, fmt.Errorf("runtime live header cache capacity must be between0 and4096")
		}
		if copy.PollingInterval < 0 || copy.RetryInterval < 0 {
			return nil, fmt.Errorf("runtime live intervals must be nonnegative")
		}
		o.Live = &copy
	}
	if o.FactorySources && (!o.SourceCheckpoints || o.Live == nil) {
		return nil, fmt.Errorf("factory sources require source checkpoints and continuous lifecycle")
	}
	b := o.Historical.Build.Executable()
	session, release, err := b.AcquireSession()
	if err != nil {
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			release()
		}
	}()
	if b == nil || (b.Ordering != "multichain" && b.Ordering != "omnichain") || len(b.Chains) == 0 || len(b.Chains) != len(b.EventCallbacks) || len(b.Chains) != len(b.Contracts) || len(b.Chains) != len(b.SetupCallbacks) {
		return nil, fmt.Errorf("runtime closure requires complete default multichain or supported finalized omnichain executable build")
	}
	if b.Ordering == "omnichain" && (o.Live != nil || o.FactorySources) {
		return nil, fmt.Errorf("omnichain currently requires bounded finalized non-factory runtime")
	}
	if b.Ordering == "omnichain" {
		if o.Historical.QuerySize < 0 || o.Historical.QuerySize > 12000 {
			return nil, fmt.Errorf("omnichain query size must be between1 and12000, or0 for the source default")
		}
		o.SourceCheckpoints = true
	} else if o.Historical.QuerySize != 0 {
		return nil, fmt.Errorf("runtime-v5 does not support historical scheduling, query or observer options")
	}
	if o.Historical.Owned.Storage.Ordering != "" && o.Historical.Owned.Storage.Ordering != schema.Ordering(b.Ordering.(string)) {
		return nil, fmt.Errorf("unsupported runtime ordering")
	}
	if o.Historical.Owned.Service.Database != nil || o.Historical.Owned.Service.Clients != nil || len(o.Historical.Owned.Service.Build.Chains) != 0 {
		return nil, fmt.Errorf("runtime owns service build/database/clients")
	}
	r := &RuntimeRunner{build: b, session: session, release: release, options: o, starts: map[uint64]uint64{}, latest: map[uint64]store.ManagedLiveHead{}, finalized: map[uint64]store.ManagedLiveHead{}, finalBlocks: make([]indexing.Object, len(b.Chains)), closeDone: make(chan struct{}), runDone: make(chan struct{})}
	close(r.runDone)
	for i, c := range b.Chains {
		cache, ok := c["cacheRpcRequests"].(bool)
		if !ok || (!cache && o.Live == nil) {
			return nil, fmt.Errorf("runtime-v5 bounded closure requires cacheRpcRequests=true")
		}
		id, e := uintQuantity(c["id"])
		if e != nil {
			return nil, e
		}
		if _, duplicate := r.starts[id]; duplicate {
			return nil, fmt.Errorf("duplicate runtime chain")
		}
		if len(b.SetupCallbacks[i]) != 0 && o.Live == nil {
			return nil, fmt.Errorf("setup callbacks are not yet supported by runtime-v5")
		}
		r.setup = r.setup || len(b.SetupCallbacks[i]) != 0
		if len(b.EventCallbacks[i]) == 0 {
			if o.SourceCheckpoints {
				return nil, fmt.Errorf("pinned getLocalSyncProgress with empty event filters: RangeError: The number Infinity cannot be converted to a BigInt because it is not an integer")
			}
			return nil, fmt.Errorf("runtime chain requires callbacks")
		}
		if b.Ordering == "omnichain" {
			if err := validateOmnichainCallbacks(b.EventCallbacks[i]); err != nil {
				return nil, err
			}
		}
		first := uint64(^uint64(0))
		for _, cb := range b.EventCallbacks[i] {
			if e := validateHandler(cb); e != nil {
				return nil, e
			}
			f, ok := cb["filter"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("runtime callback filter missing")
			}
			if factories := filter.GetFilterFactories(f); len(factories) != 0 {
				contractCall := f["type"] == "trace" && cb["type"] == "contract" && f["callType"] == "CALL"
				if !o.FactorySources || (f["type"] != "log" && f["type"] != "transaction" && f["type"] != "transfer" && !contractCall) {
					return nil, fmt.Errorf("runtime factory sources currently require opt-in log, transaction or transfer filters, or contract CALL trace filters")
				}
			}
			from, e := uintQuantity(filter.FilterFromBlock(f))
			if e != nil || ((!o.SourceCheckpoints || b.Ordering == "omnichain") && from == 0) {
				return nil, fmt.Errorf("runtime-v5 requires startBlock>=1")
			}
			first = min(first, from)
		}
		r.starts[id] = first
	}
	if o.OnSetupCommitted != nil && !r.setup {
		return nil, fmt.Errorf("setup observer requires setup callbacks")
	}
	// Freeze chain objects before binding identity for every runtime format.
	snapshot := *b
	snapshot.Chains = make([]indexing.Object, len(b.Chains))
	for i, chain := range b.Chains {
		copy := indexing.Object{}
		for k, v := range chain {
			copy[k] = v
		}
		snapshot.Chains[i] = copy
	}
	b = &snapshot
	if r.setup {
		if err := freezeRuntimeSetup(b); err != nil {
			return nil, err
		}
	}
	r.build = b
	plan, err := managedPlanWithUnbounded(b, o.Live != nil)
	if err != nil {
		return nil, err
	}
	if r.setup {
		b.Contracts = frozenSetupValue(reflect.ValueOf(b.Contracts)).Interface().([]indexing.Object)
		b.IndexingFunctions = frozenSetupValue(reflect.ValueOf(b.IndexingFunctions)).Interface().([]indexing.Object)
	}
	r.ends = plan.EndBlocks
	plan.BuildID = o.BuildID
	plan.ViewsSchema = o.ViewsSchema
	// No persistent mutation precedes these capability/range checks.
	reqs, err := o.Requesters(ctx, 0)
	if err != nil {
		return nil, err
	}
	if len(reqs) != len(b.Chains) {
		return nil, fmt.Errorf("runtime requester count mismatch")
	}
	for i, c := range b.Chains {
		if reqs[i] == nil {
			return nil, fmt.Errorf("nil runtime requester")
		}
		if _, ok := reqs[i].(rpc.AsyncRequester); !ok {
			return nil, fmt.Errorf("async runtime requester required")
		}
		id, _ := uintQuantity(c["id"])
		progress := source.NewSourceSyncProgressRPC(reqs[i].(rpc.AsyncRequester))
		latestResult := <-progress.BeginBlockByNumber(ctx, "latest", false, source.SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		latest, e := latestResult.Block, latestResult.Err
		if e != nil {
			return nil, e
		}
		n, e := uintQuantity(latest["number"])
		if e != nil {
			return nil, e
		}
		window, e := uintQuantity(c["reorgWindow"])
		if e != nil {
			return nil, e
		}
		if o.Live == nil && r.ends[id] > n {
			return nil, fmt.Errorf("bounded runtime end exceeds acquired latest; continuous polling is not implemented")
		}
		latestFinal := runtimeFinalityHeader(latest)
		acquired := map[*finality.Block]indexing.Object{latestFinal: latest}
		var blockTime *float64
		if viem, ok := c["viemChain"].(map[string]any); ok {
			if ms, ok := viem["blockTime"].(float64); ok {
				copy := ms
				blockTime = &copy
			}
		}
		finalHead, e := finality.GetFinalizedBlock(ctx, finality.Chain{ReorgWindow: int(window), BlockTimeMS: blockTime}, func(ctx context.Context, request finality.Request) (*finality.Block, error) {
			result := <-progress.BeginBlockByNumber(ctx, source.Hex(request.Number), request.IncludeTransactions, source.SourceSyncProgressRequestOptions{RetryNullBlockRequest: request.RetryNullBlockRequest})
			if result.Err != nil {
				return nil, result.Err
			}
			block := result.Block
			head := runtimeFinalityHeader(block)
			acquired[head] = block
			return head, nil
		}, latestFinal, nil)
		if e != nil {
			return nil, e
		}
		final, ok := acquired[finalHead]
		if !ok {
			return nil, fmt.Errorf("finality search returned an unacquired header")
		}

		r.latest[id], e = runtimeHead(id, latest)
		if e != nil {
			return nil, e
		}
		r.finalized[id], e = runtimeHead(id, final)
		if e != nil {
			return nil, e
		}
		r.finalBlocks[i] = final
	}
	storage := o.Historical.Owned.Storage
	storage.Ordering = schema.Ordering(b.Ordering.(string))
	if b.Ordering == "omnichain" {
		if err = r.validateOmnichainFinalizedRange(ctx, reqs); err != nil {
			return nil, err
		}
	}
	cachePolicy := map[uint64]bool{}
	for _, chain := range r.build.Chains {
		cachePolicy[runtimeChainID(chain)] = runtimeRPCCacheEnabled(chain)
	}
	r.domains, err = opener(ctx, storage, store.ManagedLiveRuntimeOptions{FactorySources: o.FactorySources, SourceCheckpoints: o.SourceCheckpoints, Setup: r.setup, CacheRPCRequests: cachePolicy, ManagedLiveOptions: store.ManagedLiveOptions{ManagedFiniteOptions: plan, OpenID: o.OpenID, FinalizedHeads: r.finalized}, StartBlocks: r.starts, Continuous: o.Live != nil, ObserveDomain: runtimeDomainObserver(r)})
	if err != nil {
		return nil, err
	}
	opened = true
	return r, nil
}
func runtimeHead(id uint64, b indexing.Object) (store.ManagedLiveHead, error) {
	n, e := uintQuantity(b["number"])
	if e != nil {
		return store.ManagedLiveHead{}, e
	}
	t, e := uintQuantity(b["timestamp"])
	if e != nil {
		return store.ManagedLiveHead{}, e
	}
	cp, e := checkpoint.Encode(checkpoint.Block(t, id, n, true))
	h, ok := b["hash"].(string)
	p, pok := b["parentHash"].(string)
	if !ok || !pok {
		return store.ManagedLiveHead{}, fmt.Errorf("acquired header lacks hashes")
	}
	return store.ManagedLiveHead{Checkpoint: cp, Hash: strings.ToLower(h), ParentHash: strings.ToLower(p)}, e
}
func (r *RuntimeRunner) Ready(ctx context.Context) (bool, error) {
	r.mu.Lock()
	closed := r.closing
	r.mu.Unlock()
	if closed || r.failed.Load() || r.transitioning.Load() {
		return false, nil
	}
	return r.domains.Ready(ctx)
}
func (r *RuntimeRunner) Control(ctx context.Context) (store.ManagedLiveControl, error) {
	return r.domains.Control(ctx)
}
func (r *RuntimeRunner) Close(ctx context.Context) error {
	if r.domains.RuntimeCallbackContext(ctx) || ctx.Value(runtimeRunnerCallbackKey{}) == r {
		return store.ErrManagedLiveBusy
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closing = true
		if r.cancel != nil {
			r.cancel()
		}
		done := r.runDone
		r.mu.Unlock()
		go func() { <-done; r.closeErr = r.domains.Close(context.Background()); r.release(); close(r.closeDone) }()
	})
	select {
	case <-r.closeDone:
		return r.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *RuntimeRunner) newEpoch(ctx context.Context, learned *indexing.RuntimeLearned) (*indexing.RuntimeEpoch, error) {
	r.sequence++
	reqs, err := r.options.Requesters(ctx, r.sequence)
	if err != nil {
		return nil, err
	}
	o := r.options.Historical.Owned.Service
	o.Build = indexing.Build{Chains: r.build.Chains, Contracts: r.build.Contracts, EventCallbacks: r.build.EventCallbacks, SetupCallbacks: r.build.SetupCallbacks, IndexingFunctions: r.build.IndexingFunctions, FilterSession: r.session}
	if o.FieldOrder == nil {
		o.FieldOrder = events.ProfileFieldOrder
	}
	return indexing.NewRuntimeEpoch(ctx, r.domains, o, reqs, learned)
}

func (r *RuntimeRunner) nextID(kind string) string {
	r.sequence++
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", r.options.OpenID, kind, r.sequence)))
	return "runtime-" + kind + "-" + hex.EncodeToString(sum[:])
}

func runtimeCanActivate(c store.ManagedLiveControl) bool {
	if c.Batches == 0 || len(c.ReplayTargets) > 0 {
		return false
	}
	for id, cut := range c.HistoryCuts {
		_, err := checkpoint.Decode(c.Cursors[id].Checkpoint)
		if err != nil || !store.ManagedLiveReachedBlock(c, id, cut) {
			return false
		}
	}
	return true
}
func runtimeInputsDigest(input []indexing.Object) (string, error) {
	values := make([]any, len(input))
	for i, event := range input {
		cb, ok := event["eventCallback"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("input callback missing")
		}
		chain, ok := event["chain"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("input chain missing")
		}
		value, err := runtimeStableInput(reflect.ValueOf(map[string]any{"type": event["type"], "callback": cb["name"], "chain": chain["id"], "checkpoint": event["checkpoint"], "event": event["event"]}))
		if err != nil {
			return "", err
		}
		values[i] = value
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func runtimeStableInput(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return []any{"null"}, nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return []any{"null"}, nil
		}
		return runtimeStableInput(v.Elem())
	}
	if v.CanInterface() {
		if n, ok := v.Interface().(*big.Int); ok {
			if n == nil {
				return []any{"nil-bigint"}, nil
			}
			return []any{"bigint", n.String()}, nil
		}
		if n, ok := v.Interface().(big.Int); ok {
			return []any{"bigint", n.String()}, nil
		}
		if filter.IsUndefined(v.Interface()) {
			return []any{"undefined"}, nil
		}
	}
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("input map key type")
		}
		out := map[string]any{}
		it := v.MapRange()
		for it.Next() {
			value, err := runtimeStableInput(it.Value())
			if err != nil {
				return nil, err
			}
			out[it.Key().String()] = value
		}
		return []any{"object", out}, nil
	case reflect.Slice, reflect.Array:
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			value, err := runtimeStableInput(v.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = value
		}
		return []any{"array", out}, nil
	}
	return managedStableValue(v, map[uintptr]bool{})
}

func runtimeHistoricalComplete(c store.ManagedLiveControl) bool {
	if c.Batches == 0 {
		return false
	}
	for id, cut := range c.HistoryCuts {
		_, err := checkpoint.Decode(c.Cursors[id].Checkpoint)
		if err != nil || !store.ManagedLiveReachedBlock(c, id, cut) {
			return false
		}
	}
	return true
}

func (r *RuntimeRunner) acquireFinalizedLineage(ctx context.Context, requester rpc.Requester, id uint64, boundary store.ManagedLiveHead) (map[uint64]store.ManagedLiveHead, error) {
	out := map[uint64]store.ManagedLiveHead{}
	target, err := checkpoint.Decode(boundary.Checkpoint)
	if err != nil {
		return nil, err
	}
	head := r.finalized[id]
	cp, err := checkpoint.Decode(head.Checkpoint)
	if err != nil {
		return nil, err
	}
	a := rpc.NewActions(requester)
	if target.BlockNumber > cp.BlockNumber {
		return nil, fmt.Errorf("private safe cursor exceeds acquired finality")
	}
	for {
		if cp.BlockNumber <= r.ends[id] {
			out[cp.BlockNumber] = head
		}
		if cp.BlockNumber == target.BlockNumber {
			if head != boundary {
				return nil, fmt.Errorf("safe boundary differs from authenticated finalized lineage")
			}
			return out, nil
		}
		b, e := a.GetSourceBlockByNumber(ctx, source.Hex(cp.BlockNumber-1), false)
		if e != nil {
			return nil, e
		}
		previous, e := runtimeHead(id, b)
		if e != nil {
			return nil, e
		}
		next, e := checkpoint.Decode(previous.Checkpoint)
		if e != nil || next.BlockNumber+1 != cp.BlockNumber || next.BlockTimestamp > cp.BlockTimestamp || previous.Hash != head.ParentHash {
			return nil, fmt.Errorf("finalized predecessor chain changed")
		}
		head, cp = previous, next
	}
}

func runtimeFinalityHeader(block indexing.Object) *finality.Block {
	return &finality.Block{Number: fmt.Sprint(block["number"]), Timestamp: fmt.Sprint(block["timestamp"]), Hash: fmt.Sprint(block["hash"]), ParentHash: fmt.Sprint(block["parentHash"]), LogsBloom: fmt.Sprint(block["logsBloom"])}
}
