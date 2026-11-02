package historical

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	source "ponder.local/ponder-go/runtime"
	"ponder.local/ponder-go/store"
	"time"
)

// RuntimeLiveOptions opts into the isolated continuous-v6 runtime protocol.
// Wait is a cancellable scheduling boundary; OnProgress observes completed
// native transitions and cannot supply fetched blocks or database receipts.
type RuntimeLiveOptions struct {
	// HeaderCacheCapacity is an optimization only: 0 defaults to4096; 1..4096
	// can reduce memory without changing any proof or persistence requirement.
	HeaderCacheCapacity int
	PollingInterval     time.Duration
	RetryInterval       time.Duration
	Wait                func(context.Context, time.Duration) error
	OnProgress          func(context.Context, RuntimeLiveProgress) error
	OnDurableReorg      func(context.Context, store.ManagedLiveDomainStage) error
}

func runtimeDomainObserver(r *RuntimeRunner) func(context.Context, store.ManagedLiveDomainStage) error {
	if r.options.Live == nil || r.options.Live.OnDurableReorg == nil {
		return nil
	}
	return func(ctx context.Context, e store.ManagedLiveDomainStage) error {
		return r.options.Live.OnDurableReorg(context.WithValue(ctx, runtimeRunnerCallbackKey{}, r), e)
	}
}
func (r *RuntimeRunner) PublicCheckpoints(ctx context.Context) (map[uint64]string, error) {
	return r.domains.RuntimePublicCheckpoints(ctx)
}

type RuntimeLiveProgress struct {
	Type    string
	ChainID uint64
	Head    store.ManagedLiveHead
}

type runtimeLiveItem struct {
	index int
	event source.SourceRealtimeEvent
	head  bool
	// replay holds a real RPC-acquired and ABI-decoded initial block after a
	// global undo restores an inclusive Down cursor. It is not a header receipt.
	replay *runtimeAcquiredBlock
}
type runtimeLiveFetchError struct{ err error }

func (e *runtimeLiveFetchError) Error() string { return e.err.Error() }
func (e *runtimeLiveFetchError) Unwrap() error { return e.err }

type runtimeLiveQueue struct {
	cancel   context.CancelFunc
	stop     func()
	joins    []func()
	releases []func()
	next     func() (*source.SourceEventBatch, error, bool)
	children []*source.SourceHistoricalChildren
}

func (q *runtimeLiveQueue) close() {
	q.cancel()
	q.stop()
	for _, join := range q.joins {
		join()
	}
	for _, release := range q.releases {
		release()
	}
}
func runtimeLight(b indexing.Object) indexing.Object {
	return indexing.Object{"number": runtimeHeaderScalar(b["number"]), "hash": b["hash"], "parentHash": b["parentHash"], "timestamp": runtimeHeaderScalar(b["timestamp"])}
}
func (r *RuntimeRunner) liveWait(ctx context.Context, retry bool) error {
	d := r.options.Live.PollingInterval
	if d <= 0 {
		d = time.Second
	}
	if retry {
		d = r.options.Live.RetryInterval
		if d <= 0 {
			d = time.Second
		}
	}
	if r.options.Live.Wait != nil {
		return r.options.Live.Wait(context.WithValue(ctx, runtimeRunnerCallbackKey{}, r), d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (r *RuntimeRunner) liveNotify(ctx context.Context, kind string, id uint64, head store.ManagedLiveHead) error {
	if r.options.Live.OnProgress == nil {
		return nil
	}
	return r.options.Live.OnProgress(context.WithValue(ctx, runtimeRunnerCallbackKey{}, r), RuntimeLiveProgress{Type: kind, ChainID: id, Head: head})
}
func runtimeHashBlock(ctx context.Context, requester rpc.Requester, hash string) (indexing.Object, error) {
	var raw indexing.Object
	params := []any{hash, false}
	if err := requester.Call(ctx, "eth_getBlockByHash", params, &raw); err != nil {
		return nil, &runtimeLiveFetchError{err}
	}
	if raw == nil {
		return nil, fmt.Errorf("retained runtime block %s unavailable", hash)
	}
	return rpc.StandardizeSourceBlock(raw, rpc.ActionRequest{Method: "eth_getBlockByHash", Params: params})
}
func (r *RuntimeRunner) liveBranch(ctx context.Context, requester rpc.Requester, id uint64, c store.ManagedLiveControl, known *runtimeLiveHeaders) (indexing.Object, []indexing.Object, error) {
	cursor := c.Cursors[id]
	final := r.finalized[id]
	if h, ok := c.Finality[id]; ok {
		final = h
	}
	at, _ := checkpoint.Decode(cursor.Checkpoint)
	f, _ := checkpoint.Decode(final.Checkpoint)
	if f.BlockNumber > at.BlockNumber {
		final = cursor
		f = at
	}
	retained := []indexing.Object{}
	head := cursor
	for {
		raw := known.get(head.Hash)
		if raw == nil {
			var err error
			raw, err = runtimeHashBlock(ctx, requester, head.Hash)
			if err != nil {
				return nil, nil, err
			}
			known.put(raw)
		}
		actual, err := runtimeHead(id, raw)
		if err != nil || actual != head {
			return nil, nil, fmt.Errorf("retained runtime raw header differs from durable identity: %w", err)
		}
		cp, _ := checkpoint.Decode(head.Checkpoint)
		if cp.BlockNumber == f.BlockNumber {
			if head != final {
				return nil, nil, store.ErrManagedLiveDeepReorg
			}
			for i, j := 0, len(retained)-1; i < j; i, j = i+1, j-1 {
				retained[i], retained[j] = retained[j], retained[i]
			}
			return runtimeLight(raw), retained, nil
		}
		if cp.BlockNumber < f.BlockNumber {
			return nil, nil, store.ErrManagedLiveDeepReorg
		}
		retained = append(retained, runtimeLight(raw))
		parent := known.get(head.ParentHash)
		if parent == nil {
			parent, err = runtimeHashBlock(ctx, requester, head.ParentHash)
			if err != nil {
				return nil, nil, err
			}
			known.put(parent)
		}
		head, err = runtimeHead(id, parent)
		if err != nil {
			return nil, nil, err
		}
	}
}
func (r *RuntimeRunner) newLiveQueue(ctx context.Context, epoch *indexing.RuntimeEpoch, c store.ManagedLiveControl, known *runtimeLiveHeaders) (q *runtimeLiveQueue, err error) {
	run, cancel := context.WithCancel(ctx)
	q = &runtimeLiveQueue{cancel: cancel, stop: func() {}, children: make([]*source.SourceHistoricalChildren, len(r.build.Chains))}
	owned := q
	ok := false
	defer func() {
		if !ok {
			owned.close()
		}
	}()
	gens := []source.SourceEventGenerator{}
	for i, chain := range r.build.Chains {
		i, chain := i, chain
		id := runtimeChainID(chain)
		filters := []filter.Object{}
		for _, cb := range r.session.Callbacks(r.build.EventCallbacks[i]) {
			filters = append(filters, cb["filter"].(map[string]any))
		}
		var child *source.SourceHistoricalChildren
		if runtimeRPCCacheEnabled(chain) && !r.options.FactorySources {
			err = r.domains.WithRuntimeSync(epoch.Context(), func(io context.Context, s *store.SourceSyncStore) error {
				var e error
				child, e = source.GetSourceChildAddresses(io, true, filters, s)
				return e
			})
		} else {
			child, err = source.GetSourceChildAddresses(epoch.Context(), false, filters, nil)
		}
		if err != nil {
			return nil, err
		}
		q.children[i] = child
		final, retained, e := r.liveBranch(run, epoch.Requester(i), id, c, known)
		if e != nil {
			return nil, e
		}
		window, e := uintQuantity(chain["reorgWindow"])
		if e != nil {
			return nil, e
		}
		syncer, e := source.NewManagedSourceRealtimeSync(epoch.Requester(i), child, final, retained, filters, source.SourceRealtimeChain{ID: id, Name: fmt.Sprint(chain["name"]), ReorgWindow: window, PollingInterval: 1000})
		if e == nil && r.options.FactorySources {
			syncer, e = source.RestoreManagedFactoryBranch(run, epoch.Requester(i), child, final, retained, filters, source.SourceRealtimeChain{ID: id, Name: fmt.Sprint(chain["name"]), ReorgWindow: window, PollingInterval: 1000})
			if errors.Is(e, source.ErrManagedFactoryAcquisition) {
				e = &runtimeLiveFetchError{e}
			}

		}
		if e != nil {
			return nil, e
		}
		work, release, e := r.domains.BeginRuntimeIO(run)
		if e != nil {
			return nil, e
		}
		q.releases = append(q.releases, release)
		cursor, _ := checkpoint.Decode(c.Cursors[id].Checkpoint)
		seq := func(yield func(*source.SourceEventBatch, error) bool) {
			emit := func(event source.SourceRealtimeEvent, head bool) bool {
				return yield(&source.SourceEventBatch{Events: []indexing.Object{{"runtimeLive": &runtimeLiveItem{index: i, event: event, head: head}}}}, nil)
			}
			n := cursor.BlockNumber
			first := true
			if r.options.SourceCheckpoints && !store.ManagedLiveReachedBlock(c, id, n) {
				raw, e := rpc.NewActions(epoch.Requester(i)).GetSourceBlockByNumber(work, source.Hex(n), true)
				if e != nil {
					yield(nil, &runtimeLiveFetchError{e})
					return
				}
				actual, e := runtimeHead(id, raw)
				if e != nil || actual != c.Cursors[id] {
					yield(nil, errors.Join(store.ErrManagedLiveDeepReorg, e))
					return
				}
				historical, e := source.NewSourceHistoricalSync(id, epoch.Requester(i), nil, child, r.options.Historical.Acquisition)
				if e != nil {
					yield(nil, e)
					return
				}
				prepared, e := source.PrepareManagedSourceBlock(work, chain, r.build.EventCallbacks[i], r.session, child, historical, n, raw)
				if e != nil {
					yield(nil, &runtimeLiveFetchError{e})
					return
				}
				input, e := prepared.DecodeInMemory()
				if e != nil {
					yield(nil, e)
					return
				}
				replay := &runtimeAcquiredBlock{index: i, id: id, head: actual, raw: raw, events: input}
				if !yield(&source.SourceEventBatch{Events: []indexing.Object{{"runtimeLive": &runtimeLiveItem{index: i, event: source.SourceRealtimeEvent{Type: "block", Block: raw}, replay: replay}}}}, nil) {
					return
				}
			}
			for {
				if e := work.Err(); e != nil {
					yield(nil, e)
					return
				}
				if n >= r.ends[id] {
					emit(source.SourceRealtimeEvent{Type: "head"}, true)
					return
				}
				if !first {
					if e := r.liveWait(work, false); e != nil {
						yield(nil, e)
						return
					}
				}
				first = false
				progress := source.NewSourceSyncProgressRPC(epoch.Requester(i).(rpc.AsyncRequester))
				task := progress.BeginBlockByNumber(work, "latest", true, source.SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
				// Drain the admitted source future before releasing its epoch permit.
				result, open := <-task
				if !open {
					yield(nil, fmt.Errorf("live source future closed without a result"))
					return
				}
				if err := work.Err(); err != nil {
					yield(nil, err)
					return
				}
				if result.Err != nil {
					yield(nil, &runtimeLiveFetchError{result.Err})
					return
				}
				raw := result.Block
				number, e := uintQuantity(raw["number"])
				if e != nil {
					yield(nil, e)
					return
				}
				if number > r.ends[id] {
					raw, e = rpc.NewActions(epoch.Requester(i)).GetSourceBlockByNumber(work, source.Hex(r.ends[id]), true)
					if e != nil {
						yield(nil, &runtimeLiveFetchError{e})
						return
					}
				}
				failed := false
				for event, e := range syncer.SyncManaged(work, raw) {
					if e != nil {
						if errors.Is(e, source.ErrManagedRealtimeFinality) {
							yield(nil, errors.Join(store.ErrManagedLiveDeepReorg, e))
						} else {
							yield(nil, &runtimeLiveFetchError{e})
						}
						failed = true
						break
					}
					if event.Type == "block" {
						n, _ = uintQuantity(event.Block["number"])
					}
					if !emit(event, false) {
						return
					}
					if event.Type == "reorg" {
						return
					}
				}
				if failed {
					return
				}
				if !emit(source.SourceRealtimeEvent{Type: "head", Block: raw}, true) {
					return
				}
			}
		}
		gen, join := source.ManagedSourceEventGenerator(work, iter.Seq2[*source.SourceEventBatch, error](seq))
		gens = append(gens, gen)
		q.joins = append(q.joins, join)
	}
	q.next, q.stop = iter.Pull2(source.MergeSourceEventsByCompletion(run, gens))
	ok = true
	return q, nil
}

func (r *RuntimeRunner) runContinuous(ctx context.Context, epoch *indexing.RuntimeEpoch, c store.ManagedLiveControl, headers map[uint64]indexing.Object, result RuntimeResult) (out RuntimeResult, runErr error) {
	out = result
	var q *runtimeLiveQueue
	defer func() {
		if runErr != nil {
			r.transitioning.Store(true)
			out.Ready = false
			out.Completed = false
		}
		if q != nil {
			q.close()
		}
		if epoch != nil {
			runErr = errors.Join(runErr, epoch.Abort(context.Background()))
		}
	}()
	capacity := r.options.Live.HeaderCacheCapacity
	if capacity == 0 {
		capacity = 4096
	}
	known := newRuntimeLiveHeaders(capacity)
	for _, raw := range headers {
		known.put(raw)
	}
	learned, err := epoch.Seal(ctx)
	if err != nil {
		return out, err
	}
	epoch = nil
	seen := map[uint64]bool{}
	activate := func() error {
		if e := r.domains.ActivateRuntime(ctx, r.nextID("live-activate"), c.Cursors); e != nil {
			return e
		}
		r.transitioning.Store(false)
		out.Ready = true
		if r.options.OnActivated != nil {
			if e := r.options.OnActivated(context.WithValue(ctx, runtimeRunnerCallbackKey{}, r)); e != nil {
				return e
			}
		}
		return r.liveNotify(ctx, "ready", 0, store.ManagedLiveHead{})
	}
	if runtimeCanActivate(c) && !c.Serving {
		if err = activate(); err != nil {
			return out, err
		}
		c, err = r.domains.Control(ctx)
		if err != nil {
			return out, err
		}
	}
	restart := func() error {
		for {
			var e error
			epoch, e = r.newEpoch(ctx, learned)
			if e != nil {
				return e
			}
			q, e = r.newLiveQueue(epoch.Context(), epoch, c, known)
			if e == nil {
				seen = map[uint64]bool{}
				return nil
			}
			var fetch *runtimeLiveFetchError
			if !errors.As(e, &fetch) || ctx.Err() != nil {
				return e
			}
			// A failed constructor has already canceled and joined its partial
			// queue. Seal this generation before any retry; keep only profiles.
			r.transitioning.Store(true)
			out.Ready = false
			learned, e = epoch.Seal(ctx)
			if e != nil {
				return e
			}
			epoch = nil
			c, e = r.domains.Control(ctx)
			if e != nil {
				return e
			}
			if c.Serving {
				if e = r.domains.PauseRuntime(ctx, r.nextID("restore-pause")); e != nil {
					return e
				}
			}
			if e = r.liveNotify(ctx, "fetch-retry", 0, store.ManagedLiveHead{}); e != nil {
				return e
			}
			if e = r.liveWait(ctx, true); e != nil {
				return e
			}
			c, e = r.domains.Control(ctx)
			if e != nil {
				return e
			}
		}
	}
	if err = restart(); err != nil {
		return out, err
	}
	retryFetch := func(e error) error {
		var fetch *runtimeLiveFetchError
		if !errors.As(e, &fetch) || ctx.Err() != nil {
			return e
		}
		r.transitioning.Store(true)
		out.Ready = false
		q.close()
		q = nil
		learned, e = epoch.Seal(ctx)
		if e != nil {
			return e
		}
		epoch = nil
		c, e = r.domains.Control(ctx)
		if e != nil {
			return e
		}
		if c.Serving {
			if e = r.domains.PauseRuntime(ctx, r.nextID("fetch-pause")); e != nil {
				return e
			}
		}
		if e = r.liveNotify(ctx, "fetch-retry", 0, store.ManagedLiveHead{}); e != nil {
			return e
		}
		if e = r.liveWait(ctx, true); e != nil {
			return e
		}
		c, e = r.domains.Control(ctx)
		if e != nil {
			return e
		}
		return restart()
	}
	for {
		batch, e, ok := q.next()
		if e != nil {
			if e = retryFetch(e); e != nil {
				return out, e
			}
			continue
		}
		if !ok {
			q.close()
			q = nil
			all := true
			for id, end := range r.ends {
				all = all && store.ManagedLiveReachedBlock(c, id, end)
			}
			if !all {
				return out, fmt.Errorf("continuous source ended before configured ranges completed")
			}
			learned, e = epoch.Seal(ctx)
			if e != nil {
				return out, e
			}
			epoch = nil
			if !c.Serving {
				if !runtimeCanActivate(c) {
					return out, store.ErrManagedLivePendingActions
				}
				if e = activate(); e != nil {
					return out, e
				}
			}
			out.Completed = true
			return out, nil
		}
		if len(batch.Events) != 1 {
			return out, fmt.Errorf("invalid live source envelope")
		}
		item, ok := batch.Events[0]["runtimeLive"].(*runtimeLiveItem)
		if !ok {
			return out, fmt.Errorf("invalid live source item")
		}
		i := item.index
		id := runtimeChainID(r.build.Chains[i])
		event := item.event
		if item.head {
			seen[id] = true
			if !c.Serving && len(seen) == len(r.build.Chains) && runtimeCanActivate(c) {
				q.close()
				q = nil
				learned, e = epoch.Seal(ctx)
				if e != nil {
					return out, e
				}
				epoch = nil
				if e = activate(); e != nil {
					return out, e
				}
				c, e = r.domains.Control(ctx)
				if e != nil {
					return out, e
				}
				if e = restart(); e != nil {
					return out, e
				}
			}
			continue
		}
		head, e := runtimeHead(id, event.Block)
		if e != nil {
			return out, e
		}
		known.put(event.Block)
		switch event.Type {
		case "block":
			var input []indexing.Object
			if item.replay != nil {
				input = item.replay.events
			} else {
				input, e = source.DecodeSourceRealtimeEvent(r.build.Chains[i], r.build.EventCallbacks[i], r.session, q.children[i], event)
				if e != nil {
					return out, e
				}
			}
			if e = epoch.Clients().Prefetch(epoch.Context(), input); e != nil {
				return out, e
			}
			digest, e := runtimeInputsDigest(input)
			if e != nil {
				return out, e
			}
			b := store.ManagedLiveBatch{ID: r.nextID("live-block"), Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []store.ManagedLiveBlock{{Head: head, InputDigest: digest}}}
			a, e := r.domains.RetainBatch(ctx, b)
			if e != nil {
				return out, e
			}
			if _, e = epoch.Process(epoch.Context(), b, a, input, false); e != nil {
				return out, e
			}
			epoch.Clients().Clear()
			out.LiveBlocks++
			c, e = r.domains.Control(ctx)
			if e != nil {
				return out, e
			}
			if e = r.liveNotify(ctx, "block", id, head); e != nil {
				return out, e
			}
		case "finalize":
			accepted, err := r.acceptContinuousFinality(epoch.Context(), epoch, i, c, head, known)
			if err != nil {
				if err = retryFetch(err); err != nil {
					return out, err
				}
				continue
			}
			if !accepted {
				continue
			}
			f := store.ManagedLiveFinalize{ID: r.nextID("live-finalize"), Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, ExpectedSafeOrdinal: c.SafeOrdinal, Finalized: map[uint64]store.ManagedLiveHead{id: head}}
			if e = r.persistContinuousFinality(ctx, epoch, q.children[i], i, c, head, f); e != nil {
				return out, e
			}
			a, e := r.domains.RetainFinalize(ctx, f)
			if e != nil {
				return out, e
			}
			if _, e = r.domains.ApplyFinalize(ctx, f, a); e != nil {
				return out, e
			}
			c, e = r.domains.Control(ctx)
			if e != nil {
				return out, e
			}
			if e = r.liveNotify(ctx, "finalize", id, head); e != nil {
				return out, e
			}
		case "reorg":
			r.transitioning.Store(true)
			out.Ready = false
			q.close()
			q = nil
			learned, e = epoch.Seal(ctx)
			if e != nil {
				return out, e
			}
			epoch = nil
			if c.Serving {
				if e = r.domains.PauseRuntime(ctx, r.nextID("reorg-pause")); e != nil {
					return out, e
				}
			}
			c, e = r.domains.Control(ctx)
			if e != nil {
				return out, e
			}
			if e = r.liveNotify(ctx, "paused", id, head); e != nil {
				return out, e
			}
			orphaned := []store.ManagedLiveHead{}
			cursor, _ := checkpoint.Decode(c.Cursors[id].Checkpoint)
			ancestor, _ := checkpoint.Decode(head.Checkpoint)
			if ancestor.BlockNumber > cursor.BlockNumber {
				head = c.Cursors[id]
			}
			for _, raw := range event.ReorgedBlocks {
				h, e := runtimeHead(id, raw)
				if e != nil {
					return out, e
				}
				cp, _ := checkpoint.Decode(h.Checkpoint)
				a, _ := checkpoint.Decode(head.Checkpoint)
				if cp.BlockNumber > a.BlockNumber && cp.BlockNumber <= cursor.BlockNumber {
					orphaned = append(orphaned, h)
					known.put(raw)
				}
			}
			request := store.ManagedLiveDomainReorg{Reorg: store.ManagedLiveReorg{ID: r.nextID("live-reorg"), Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: head}, Orphaned: orphaned}
			_, e = r.domains.CoordinateReorg(ctx, request)
			if e != nil {
				var stage *store.ManagedStageError
				if errors.As(e, &stage) && stage.Outcome == "observer" {
					return out, e
				}
				if resume := r.domains.Resume(ctx); resume != nil {
					return out, fmt.Errorf("continuous reorg sealed; close/reopen required: %w", errors.Join(e, resume))
				}
				if _, e = r.domains.CoordinateReorg(ctx, request); e != nil {
					return out, e
				}
			}
			c, e = r.domains.Control(ctx)
			if e != nil {
				return out, e
			}
			if e = r.liveNotify(ctx, "reorg", id, head); e != nil {
				return out, e
			}
			if e = restart(); e != nil {
				return out, e
			}
		default:
			return out, fmt.Errorf("unsupported actual source realtime event %q", event.Type)
		}
	}
}

// Finalized raw cache and business finality are separate fault domains. Source
// persistence is an idempotent native transaction before the receipt-backed
// business transition; a failed business transition is sealed for replay/reopen.
func (r *RuntimeRunner) persistContinuousFinality(ctx context.Context, epoch *indexing.RuntimeEpoch, child *source.SourceHistoricalChildren, i int, c store.ManagedLiveControl, head store.ManagedLiveHead, request store.ManagedLiveFinalize) error {
	id := runtimeChainID(r.build.Chains[i])
	authenticated, err := r.domains.RuntimeFinalizeHeaders(ctx, request)
	if err != nil {
		return err
	}
	// The native finalize planner is still authoritative when raw cache is off.
	if !runtimeRPCCacheEnabled(r.build.Chains[i]) {
		return nil
	}
	lineage := authenticated[id]
	prior := r.finalized[id]
	if h, ok := c.Finality[id]; ok {
		prior = h
	}
	from, _ := checkpoint.Decode(prior.Checkpoint)
	to, _ := checkpoint.Decode(head.Checkpoint)
	syncer, err := source.NewSourceHistoricalSync(id, epoch.Requester(i), nil, child, r.options.Historical.Acquisition)
	if err != nil {
		return err
	}
	for n := from.BlockNumber + 1; n <= to.BlockNumber; n++ {
		raw, err := rpc.NewActions(epoch.Requester(i)).GetSourceBlockByNumber(epoch.Context(), source.Hex(n), true)
		if err != nil {
			return err
		}
		h, err := runtimeHead(id, raw)
		if err != nil {
			return err
		}
		if expected, ok := lineage[n]; !ok || expected != h {
			return store.ErrManagedLiveBatchConflict
		}
		if n == to.BlockNumber && h != head {
			return store.ErrManagedLiveDeepReorg
		}
		prepared, err := source.PrepareManagedSourceBlock(epoch.Context(), r.build.Chains[i], r.build.EventCallbacks[i], r.session, child, syncer, n, raw)
		if err != nil {
			return err
		}
		if err = r.domains.WithRuntimeSync(epoch.Context(), func(io context.Context, s *store.SourceSyncStore) error {
			_, e := prepared.PersistAndDecode(io, s)
			return e
		}); err != nil {
			return err
		}
	}
	return nil
}
