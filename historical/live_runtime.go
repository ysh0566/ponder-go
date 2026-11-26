package historical

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/finality"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	source "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
	"maps"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type runtimeRunnerCallbackKey struct{}

// RuntimeOptions selects the owned RuntimeRunner lifecycle. A nil Live runs a
// fixed range. Default multichain supports setup and per-chain RPC caching in
// either mode. Isolated supports finalized ranges or continuous per-chain
// finality and reorgs. Omnichain ranges must fit within the common finalized
// watermark.
// BuildID identifies handler code. OpenID is unique to a single opening.
type RuntimeOptions struct {
	// SourceCheckpoints enables source initial positions for bounded or continuous
	// multichain runs, including a first event at block zero.
	// Isolated and finalized Omnichain enable these positions automatically.
	SourceCheckpoints bool
	// Development marks this deployment as a development shadow in its metadata.
	Development bool
	// FactorySources discovers child addresses on the canonical branch.
	// Set SourceCheckpoints as well. Finalized Omnichain supports finite block, log, account and contract CALL sources.
	FactorySources bool
	Live           *RuntimeLiveOptions
	// OmnichainFinalityPollInterval lets Open wait for a bounded Omnichain range
	// to become globally finalized. Zero checks once. Cancellation stops the wait.
	OmnichainFinalityPollInterval time.Duration
	// Historical.QuerySize is the bounded omnichain SQL target (1..12000);
	// zero uses 12000. MaxQueryParameters controls batched sync writes in all orderings.
	// Bounded Omnichain accepts Clock. Bounded Isolated and Omnichain accept
	// OnCommitted after persisting each group and its safe checkpoint.
	Historical                   Options
	BuildID, OpenID, ViewsSchema string
	// Requesters supplies fresh transports for preflight, fetch epochs and uncached source scans.
	Requesters  func(context.Context, uint64) ([]rpc.Requester, error)
	OnActivated func(context.Context) error
	// OnSetupCommitted runs after all initial setup transactions commit and before source events.
	OnSetupCommitted func(context.Context) error
}
type RuntimeResult struct {
	Recovered, Ready, Completed             bool
	HistoricalBlocks, LiveBlocks, Refetches int
}
type RuntimeRunner struct {
	domains           *store.ManagedLiveDomains
	setup             bool
	sparseChains      map[uint64]bool
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
	if _, err := store.SourceQueryParameterBudget(o.Historical.MaxQueryParameters); err != nil {
		return nil, err
	}
	if o.Historical.RetryWait != nil {
		return nil, fmt.Errorf("runtime does not support historical RetryWait")
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
	if o.FactorySources && !o.SourceCheckpoints {
		return nil, fmt.Errorf("factory sources require source checkpoints")
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
	if b == nil || (b.Ordering != "multichain" && b.Ordering != "omnichain" && b.Ordering != string(schema.Isolated)) || len(b.Chains) == 0 || len(b.Chains) != len(b.EventCallbacks) || len(b.Chains) != len(b.Contracts) || len(b.Chains) != len(b.SetupCallbacks) {
		return nil, fmt.Errorf("runtime closure requires a complete multichain, finalized omnichain or isolated executable build")
	}
	if o.Historical.Clock != nil && b.Ordering != "omnichain" {
		return nil, fmt.Errorf("this ordering does not support historical Clock")
	}
	// These modes commit the safe checkpoint in the same transaction as the batch.
	if o.Historical.OnCommitted != nil && (o.Live != nil || (b.Ordering != "omnichain" && b.Ordering != string(schema.Isolated))) {
		return nil, fmt.Errorf("this mode does not support historical OnCommitted: bounded Isolated or Omnichain required")
	}
	if o.OmnichainFinalityPollInterval < 0 || (o.OmnichainFinalityPollInterval > 0 && b.Ordering != "omnichain") {
		return nil, fmt.Errorf("OmnichainFinalityPollInterval requires a nonnegative interval and Omnichain ordering")
	}
	if b.Ordering == "omnichain" && o.Live != nil {
		return nil, fmt.Errorf("%s currently requires bounded finalized runtime", b.Ordering)
	}
	if b.Ordering == "omnichain" {
		if o.Historical.QuerySize < 0 || o.Historical.QuerySize > 12000 {
			return nil, fmt.Errorf("omnichain query size must be between1 and12000, or0 for the source default")
		}
	} else if o.Historical.QuerySize != 0 {
		return nil, fmt.Errorf("this ordering does not support historical QuerySize")
	}
	if b.Ordering != "multichain" {
		o.SourceCheckpoints = true
	}
	if o.Historical.Owned.Storage.Ordering != "" && o.Historical.Owned.Storage.Ordering != schema.Ordering(b.Ordering.(string)) {
		return nil, fmt.Errorf("unsupported runtime ordering")
	}
	if o.Historical.Owned.Service.Database != nil || o.Historical.Owned.Service.Clients != nil || len(o.Historical.Owned.Service.Build.Chains) != 0 {
		return nil, fmt.Errorf("runtime owns service build/database/clients")
	}
	r := &RuntimeRunner{build: b, session: session, release: release, options: o, starts: map[uint64]uint64{}, latest: map[uint64]store.ManagedLiveHead{}, finalized: map[uint64]store.ManagedLiveHead{}, finalBlocks: make([]indexing.Object, len(b.Chains)), closeDone: make(chan struct{}), runDone: make(chan struct{})}
	close(r.runDone)
	r.sparseChains = make(map[uint64]bool)
	for i, c := range b.Chains {
		_, ok := c["cacheRpcRequests"].(bool)
		if !ok {
			return nil, fmt.Errorf("runtime requires boolean cacheRpcRequests for each chain")
		}
		id, e := uintQuantity(c["id"])
		if e != nil {
			return nil, e
		}
		if _, duplicate := r.starts[id]; duplicate {
			return nil, fmt.Errorf("duplicate runtime chain")
		}
		r.setup = r.setup || len(b.SetupCallbacks[i]) != 0
		if len(b.EventCallbacks[i]) == 0 {
			if o.SourceCheckpoints {
				return nil, fmt.Errorf("pinned getLocalSyncProgress with empty event filters: RangeError: The number Infinity cannot be converted to a BigInt because it is not an integer")
			}
			return nil, fmt.Errorf("runtime chain requires callbacks")
		}
		if b.Ordering == "omnichain" {
			dense, err := omnichainDenseCallbacks(b.EventCallbacks[i])
			if err != nil {
				return nil, err
			}
			if !dense {
				r.sparseChains[id] = true
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
			if e != nil || (!o.SourceCheckpoints && from == 0) {
				return nil, fmt.Errorf("runtime-v5 requires startBlock>=1")
			}
			first = min(first, from)
		}
		r.starts[id] = first
	}
	if o.OnSetupCommitted != nil && !r.setup {
		return nil, fmt.Errorf("setup observer requires setup callbacks")
	}
	// Keep the session lease on the original graph. Setup callbacks bind to
	// these private chain maps when their execution metadata is frozen.
	chains := make([]indexing.Object, len(b.Chains))
	for i, chain := range b.Chains {
		chains[i] = maps.Clone(chain)
	}
	b = &config.ExecutableSourceIndexingBuild{
		Chains:            chains,
		FinalizedBlocks:   b.FinalizedBlocks,
		Contracts:         b.Contracts,
		EventCallbacks:    b.EventCallbacks,
		SetupCallbacks:    b.SetupCallbacks,
		IndexingFunctions: b.IndexingFunctions,
		RPCs:              b.RPCs,
		Ordering:          b.Ordering,
	}
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
	// The plan has checked these values for cycles and bound them to its ID.
	// Keep nested metadata such as viemChain.blockTime stable for later epochs.
	for _, chain := range b.Chains {
		for key, value := range chain {
			if key != "rpc" && key != "ws" && value != nil {
				chain[key] = frozenRuntimeValue(reflect.ValueOf(value)).Interface()
			}
		}
	}
	if r.setup {
		b.Contracts = frozenRuntimeValue(reflect.ValueOf(b.Contracts)).Interface().([]indexing.Object)
		b.IndexingFunctions = frozenRuntimeValue(reflect.ValueOf(b.IndexingFunctions)).Interface().([]indexing.Object)
	}
	r.ends = plan.EndBlocks
	plan.BuildID = o.BuildID
	plan.Development = o.Development
	plan.ViewsSchema = o.ViewsSchema
	// No persistent mutation precedes these capability/range checks.
	reqs, err := o.Requesters(ctx, 0)
	if err != nil {
		return nil, err
	}
	if len(reqs) != len(b.Chains) {
		return nil, fmt.Errorf("runtime requester count mismatch")
	}
	if err := r.checkOpeningRange(ctx, reqs); err != nil {
		return nil, err
	}
	storage := o.Historical.Owned.Storage
	storage.Ordering = schema.Ordering(b.Ordering.(string))
	cachePolicy := map[uint64]bool{}
	var openLogScanRPC func(context.Context) (map[uint64]store.LogScanRPC, error)
	for _, chain := range r.build.Chains {
		id := runtimeChainID(chain)
		cachePolicy[id] = runtimeRPCCacheEnabled(chain)
		if r.sparseChains[id] && !cachePolicy[id] {
			openLogScanRPC = r.openLogScanRPC
		}
	}
	logFilters, logABIs := r.sparseSources()
	r.domains, err = opener(ctx, storage, store.ManagedLiveRuntimeOptions{
		ManagedLiveOptions: store.ManagedLiveOptions{ManagedFiniteOptions: plan, OpenID: o.OpenID, FinalizedHeads: r.finalized},
		MaxQueryParameters: o.Historical.MaxQueryParameters,
		OpenLogScanRPC:     openLogScanRPC,
		LogOnlyFilters:     logFilters,
		LogOnlyEventABIs:   logABIs,
		FactorySources:     o.FactorySources,
		SourceCheckpoints:  o.SourceCheckpoints,
		Setup:              r.setup,
		CacheRPCRequests:   cachePolicy,
		StartBlocks:        r.starts,
		Continuous:         o.Live != nil,
		ObserveDomain:      runtimeDomainObserver(r),
	})
	if err != nil {
		return nil, err
	}
	opened = true
	return r, nil
}

func (r *RuntimeRunner) acquireOpeningHeads(ctx context.Context, reqs []rpc.Requester) error {
	for i, c := range r.build.Chains {
		if reqs[i] == nil {
			return fmt.Errorf("nil runtime requester")
		}
		if _, ok := reqs[i].(rpc.AsyncRequester); !ok {
			return fmt.Errorf("async runtime requester required")
		}
		id, _ := uintQuantity(c["id"])
		progress := source.NewSourceSyncProgressRPC(reqs[i].(rpc.AsyncRequester))
		latestResult := <-progress.BeginBlockByNumber(ctx, "latest", false, source.SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		latest, e := latestResult.Block, latestResult.Err
		if e != nil {
			return e
		}
		n, e := uintQuantity(latest["number"])
		if e != nil {
			return e
		}
		window, e := uintQuantity(c["reorgWindow"])
		if e != nil {
			return e
		}
		if r.options.Live == nil && r.options.OmnichainFinalityPollInterval == 0 && r.ends[id] > n {
			return fmt.Errorf("bounded runtime end exceeds acquired latest block")
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
			got, err := uintQuantity(block["number"])
			if err != nil || got != request.Number {
				return nil, fmt.Errorf("invalid finality header at requested height %d", request.Number)
			}
			head := runtimeFinalityHeader(block)
			acquired[head] = block
			return head, nil
		}, latestFinal, nil)
		if e != nil {
			return e
		}
		final, ok := acquired[finalHead]
		if !ok {
			return fmt.Errorf("finality search returned an unacquired header")
		}

		r.latest[id], e = runtimeHead(id, latest)
		if e != nil {
			return e
		}
		r.finalized[id], e = runtimeHead(id, final)
		if e != nil {
			return e
		}
		if r.build.Ordering == string(schema.Isolated) {
			f, _ := checkpoint.Decode(r.finalized[id].Checkpoint)
			if r.lastSetupBlock(i) > f.BlockNumber {
				return fmt.Errorf("isolated setup block on chain %d exceeds chain finality", id)
			}
			if r.options.Live == nil && r.ends[id] > f.BlockNumber {
				return fmt.Errorf("isolated end on chain %d exceeds chain finality", id)
			}
		}
		r.finalBlocks[i] = final
	}
	return nil
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
	h, p = strings.ToLower(h), strings.ToLower(p)
	var decoded [32]byte
	for _, hash := range [2]string{h, p} {
		if err := decodeLineageHash(decoded[:], hash); err != nil {
			return store.ManagedLiveHead{}, err
		}
	}
	return store.ManagedLiveHead{Checkpoint: cp, Hash: h, ParentHash: p}, e
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

func (r *RuntimeRunner) walkFinalizedLineage(ctx context.Context, requester rpc.Requester, id uint64, boundary store.ManagedLiveHead, visit func(uint64, store.ManagedLiveHead) error) error {
	target, err := checkpoint.Decode(boundary.Checkpoint)
	if err != nil {
		return err
	}
	head := r.finalized[id]
	cp, err := checkpoint.Decode(head.Checkpoint)
	if err != nil {
		return err
	}
	if cp.ChainID != id || target.ChainID != id {
		return fmt.Errorf("finalized range refers to another chain")
	}
	a := rpc.NewActions(requester)
	if target.BlockNumber > cp.BlockNumber {
		return fmt.Errorf("private safe cursor exceeds acquired finality")
	}
	low := r.logScanLineageStart(id, target.BlockNumber)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if visit != nil {
			if err := visit(cp.BlockNumber, head); err != nil {
				return err
			}
		}
		if cp.BlockNumber == target.BlockNumber && head != boundary {
			return fmt.Errorf("safe boundary differs from authenticated finalized lineage")
		}
		if cp.BlockNumber == low {
			return nil
		}
		b, e := a.GetSourceBlockByNumber(ctx, source.Hex(cp.BlockNumber-1), false)
		if e != nil {
			return e
		}
		previous, e := runtimeHead(id, b)
		if e != nil {
			return e
		}
		next, e := checkpoint.Decode(previous.Checkpoint)
		if e != nil || next.BlockNumber+1 != cp.BlockNumber || next.BlockTimestamp > cp.BlockTimestamp || previous.Hash != head.ParentHash {
			return fmt.Errorf("finalized predecessor chain changed")
		}
		head, cp = previous, next
	}
}

func runtimeFinalityHeader(block indexing.Object) *finality.Block {
	return &finality.Block{Number: fmt.Sprint(block["number"]), Timestamp: fmt.Sprint(block["timestamp"]), Hash: fmt.Sprint(block["hash"]), ParentHash: fmt.Sprint(block["parentHash"]), LogsBloom: fmt.Sprint(block["logsBloom"])}
}
