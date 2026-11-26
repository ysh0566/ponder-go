package historical

import (
	"context"
	"errors"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	source "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
	"iter"
	"sync"
)

type runtimeAcquiredBlock struct {
	rangeFirst store.ManagedLiveHead
	index      int
	id         uint64
	head       store.ManagedLiveHead
	raw        indexing.Object
	events     []indexing.Object
	history    bool
}
type runtimeBlockQueue struct {
	cancel context.CancelFunc
	stop   func()
	next   func() (*source.SourceEventBatch, error, bool)
	jobs   sync.WaitGroup
}

func (q *runtimeBlockQueue) close() { q.stop(); q.cancel(); q.jobs.Wait() }
func (r *RuntimeRunner) newBlockQueue(ctx context.Context, epoch *indexing.RuntimeEpoch, c store.ManagedLiveControl, ends map[uint64]uint64, proofs map[uint64]*runtimeLineage, headers map[uint64]indexing.Object, children []*source.SourceHistoricalChildren, syncers []*source.SourceHistoricalSync, realtime []*source.SourceRealtimeSync) *runtimeBlockQueue {
	run, cancel := context.WithCancel(ctx)
	q := &runtimeBlockQueue{cancel: cancel}
	generators := make([]source.SourceEventGenerator, len(r.build.Chains))
	for i, chain := range r.build.Chains {
		i, chain := i, chain
		id, _ := uintQuantity(chain["id"])
		number, nextErr := store.ManagedLiveNextBlock(c, id)
		if nextErr != nil {
			number = 9007199254740992
		}
		previous := c.Cursors[id]
		rawPrevious := headers[id]
		generators[i] = source.SourceEventGenerator{Next: func(call context.Context) <-chan source.SourceEventStep {
			out := make(chan source.SourceEventStep, 1)
			if number > ends[id] {
				out <- source.SourceEventStep{Done: true}
				close(out)
				return out
			}
			n := number
			number++
			work, release, err := r.domains.BeginRuntimeIO(run)
			if err != nil {
				out <- source.SourceEventStep{Err: err}
				close(out)
				return out
			}
			q.jobs.Add(1)
			go func() {
				defer q.jobs.Done()
				defer release()
				defer close(out)
				acquired, err := r.acquireRuntimeBlock(work, epoch, i, n, previous, rawPrevious, proofs, children, syncers, realtime)
				if err != nil {
					out <- source.SourceEventStep{Err: err}
					return
				}
				previous, rawPrevious = acquired.head, acquired.raw
				out <- source.SourceEventStep{Batch: &source.SourceEventBatch{Events: []indexing.Object{{"runtimeBlock": acquired}}, Checkpoint: acquired.head.Checkpoint, BlockRange: [2]float64{float64(n), float64(n)}}}
			}()
			return out
		}}
	}
	q.next, q.stop = iter.Pull2(source.MergeSourceEventsByCompletion(run, generators))
	return q
}
func (r *RuntimeRunner) acquireRuntimeBlock(ctx context.Context, epoch *indexing.RuntimeEpoch, i int, number uint64, previous store.ManagedLiveHead, rawPrevious indexing.Object, proofs map[uint64]*runtimeLineage, children []*source.SourceHistoricalChildren, syncers []*source.SourceHistoricalSync, realtime []*source.SourceRealtimeSync) (*runtimeAcquiredBlock, error) {
	chain := r.build.Chains[i]
	id, _ := uintQuantity(chain["id"])
	raw, err := rpc.NewActions(epoch.Requester(i)).GetSourceBlockByNumber(ctx, source.Hex(number), true)
	if err != nil {
		return nil, err
	}
	head, err := runtimeHead(id, raw)
	if err != nil {
		return nil, err
	}
	previousCP, _ := checkpoint.Decode(previous.Checkpoint)
	sameInitial := r.options.SourceCheckpoints && number == previousCP.BlockNumber && head == previous
	if !sameInitial && head.ParentHash != previous.Hash {
		return nil, fmt.Errorf("live branch changed: close/reopen recovery is required; in-place reorg driver is pending")
	}
	final, _ := checkpoint.Decode(r.finalized[id].Checkpoint)
	history := number <= final.BlockNumber
	b := &runtimeAcquiredBlock{index: i, id: id, head: head, raw: raw, history: history}
	if history {
		if number == final.BlockNumber && head != r.finalized[id] {
			return nil, fmt.Errorf("canonical finalized identity changed")
		}
		proven, err := proofs[id].head(number)
		if err != nil {
			return nil, err
		}
		if proven != head {
			return nil, fmt.Errorf("historical block differs from authenticated finalized lineage")
		}
		prepared, err := source.PrepareManagedSourceBlock(ctx, chain, r.build.EventCallbacks[i], r.session, children[i], syncers[i], number, raw)
		if err != nil {
			return nil, err
		}
		if !runtimeRPCCacheEnabled(chain) {
			b.events, err = prepared.DecodeInMemory()
			return b, err
		}
		err = r.domains.WithRuntimeSync(ctx, func(io context.Context, s *store.SourceSyncStore) error {
			var e error
			b.events, e = prepared.PersistAndDecode(io, s)
			return e
		})
		return b, err
	}
	if realtime[i] == nil {
		filters := []filter.Object{}
		for _, cb := range r.session.Callbacks(r.build.EventCallbacks[i]) {
			filters = append(filters, cb["filter"].(map[string]any))
		}
		window, _ := uintQuantity(chain["reorgWindow"])
		realtime[i], err = source.NewSourceRealtimeSync(epoch.Requester(i), nil, children[i], rawPrevious, filters, source.SourceRealtimeChain{ID: id, Name: fmt.Sprint(chain["name"]), ReorgWindow: window, PollingInterval: 1000}, source.SourceRealtimeOptions{})
		if err != nil {
			return nil, err
		}
	}
	got := false
	for event, e := range realtime[i].Sync(ctx, raw, nil) {
		if e != nil {
			return nil, e
		}
		if event.Type == "finalize" {
			continue
		}
		if event.Type != "block" {
			return nil, fmt.Errorf("in-place realtime reorg is not implemented; reopen for durable recovery")
		}
		b.events, e = source.DecodeSourceRealtimeEvent(chain, r.build.EventCallbacks[i], r.session, children[i], event)
		if e != nil {
			return nil, e
		}
		got = true
	}
	if !got {
		return nil, fmt.Errorf("realtime synchronizer did not acquire expected block")
	}
	return b, nil
}

// RunIndexing allows a managed multichain or omnichain build to use
// runtime.LaunchApplication and its normal HTTP lifecycle.
func (r *RuntimeRunner) RunIndexing(ctx context.Context, ready func(bool)) error {
	ready(false)
	result, err := r.run(ctx, ready)
	ready(err == nil && result.Ready)
	return err
}

func (r *RuntimeRunner) Run(ctx context.Context) (RuntimeResult, error) {
	return r.run(ctx, func(bool) {})
}

func (r *RuntimeRunner) run(ctx context.Context, ready func(bool)) (result RuntimeResult, runErr error) {
	if !r.attempted.CompareAndSwap(false, true) {
		return result, fmt.Errorf("runtime owner already attempted; close and reopen")
	}
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return result, store.ErrManagedLiveClosed
	}
	run, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.runDone = make(chan struct{})
	r.active.Store(true)
	r.mu.Unlock()
	var epoch *indexing.RuntimeEpoch
	var queue *runtimeBlockQueue
	proofs := make(map[uint64]*runtimeLineage)
	defer func() {
		if runErr != nil {
			r.failed.Store(true)
			result.Ready = false
			result.Completed = false
		}
		if queue != nil {
			queue.close()
		}
		for _, lineage := range proofs {
			if err := lineage.close(); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
		if epoch != nil {
			runErr = errors.Join(runErr, epoch.Abort(context.Background()))
		}
		if runErr != nil {
			r.failed.Store(true)
			result.Ready = false
			result.Completed = false
			if control, e := r.domains.Control(context.Background()); e == nil && control.Serving {
				runErr = errors.Join(runErr, r.domains.PauseRuntime(context.Background(), r.nextID("error-pause")))
			}
		}
		cancel()
		r.mu.Lock()
		r.active.Store(false)
		close(r.runDone)
		r.mu.Unlock()
	}()
	control, err := r.domains.Control(run)
	if err != nil {
		return result, err
	}
	result.Recovered = control.Recoveries > 0
	epoch, err = r.newEpoch(run, nil)
	if err != nil {
		return result, err
	}
	headers := map[uint64]indexing.Object{}
	children := make([]*source.SourceHistoricalChildren, len(r.build.Chains))
	syncers := make([]*source.SourceHistoricalSync, len(children))
	realtime := make([]*source.SourceRealtimeSync, len(children))
	makeSources := func() error {
		for i, c := range r.build.Chains {
			id, _ := uintQuantity(c["id"])
			filters := []filter.Object{}
			for _, cb := range r.session.Callbacks(r.build.EventCallbacks[i]) {
				filters = append(filters, cb["filter"].(map[string]any))
			}
			var child *source.SourceHistoricalChildren
			if runtimeRPCCacheEnabled(c) && !r.options.FactorySources {
				if err := r.domains.WithRuntimeSync(epoch.Context(), func(io context.Context, s *store.SourceSyncStore) error {
					var e error
					child, e = source.GetSourceChildAddresses(io, true, filters, s)
					return e
				}); err != nil {
					return err
				}
			} else {
				var e error
				child, e = source.GetSourceChildAddresses(epoch.Context(), false, filters, nil)
				if e != nil {
					return e
				}
			}
			if r.options.FactorySources {
				if cursor, exists := control.Cursors[id]; exists {
					raw, e := runtimeHashBlock(epoch.Context(), epoch.Requester(i), cursor.Hash)
					if e != nil {
						return e
					}
					actual, e := runtimeHead(id, raw)
					if e != nil || actual != cursor {
						return fmt.Errorf("factory prefix root differs from durable cursor: %w", e)
					}
					cp, e := checkpoint.Decode(cursor.Checkpoint)
					if e != nil {
						return e
					}
					scan := true
					if !store.ManagedLiveReachedBlock(control, id, cp.BlockNumber) {
						if cp.BlockNumber == 0 {
							scan = false
						} else {
							expectedParent := raw["parentHash"]
							beforeTime, _ := uintQuantity(raw["timestamp"])
							raw, e = runtimeHashBlock(epoch.Context(), epoch.Requester(i), raw["parentHash"].(string))
							if e == nil {
								pn, pe := uintQuantity(raw["number"])
								pt, te := uintQuantity(raw["timestamp"])
								if pe != nil || te != nil || raw["hash"] != expectedParent || pn+1 != cp.BlockNumber || pt > beforeTime {
									return fmt.Errorf("factory Down prefix parent differs")
								}
							}
							if e != nil {
								return e
							}
						}
					}
					if scan {
						_, e = source.RestoreManagedFactoryBranch(epoch.Context(), epoch.Requester(i), child, raw, nil, filters, source.SourceRealtimeChain{ID: id, Name: fmt.Sprint(c["name"])})
						if e != nil {
							return e
						}
					}
				}
			}
			children[i] = child
			var e error
			syncers[i], e = source.NewSourceHistoricalSync(id, epoch.Requester(i), nil, child, r.options.Historical.Acquisition)
			if e != nil {
				return e
			}
			realtime[i] = nil
		}
		return nil
	}
	// The finalized Omnichain coordinator owns its historical child map. Its
	// uncached generator scans the factory interval from the configured start,
	// even when the business events resume at a later crash checkpoint.
	if r.build.Ordering != "omnichain" {
		if err = makeSources(); err != nil {
			return result, err
		}
	}
	control, err = r.bootstrapRuntime(run, epoch, control)
	if err != nil {
		return result, err
	}

	for i, c := range r.build.Chains {
		id, _ := uintQuantity(c["id"])
		cursor := control.Cursors[id]
		n, e := checkpoint.Decode(cursor.Checkpoint)
		if e != nil {
			return result, e
		}
		raw, e := rpc.NewActions(epoch.Requester(i)).GetSourceBlockByNumber(epoch.Context(), source.Hex(n.BlockNumber), true)
		if e != nil {
			return result, e
		}
		h, e := runtimeHead(id, raw)
		if e != nil || h != cursor {
			return result, fmt.Errorf("safe restart header differs from canonical RPC: %w", e)
		}
		headers[id] = raw
		final, e := checkpoint.Decode(r.finalized[id].Checkpoint)
		if e != nil {
			return result, e
		}
		high := final.BlockNumber
		if !r.sparseChains[id] {
			high = min(high, r.ends[id])
		}
		lineage, e := newRuntimeLineage(id, high, r.logScanLineageStart(id, n.BlockNumber))
		if e != nil {
			return result, e
		}
		proofs[id] = lineage
		e = r.walkFinalizedLineage(epoch.Context(), epoch.Requester(i), id, cursor, func(number uint64, head store.ManagedLiveHead) error {
			if number <= high {
				return lineage.append(number, head)
			}
			return nil
		})
		if e != nil {
			return result, e
		}
		if e = lineage.finish(); e != nil {
			return result, e
		}
	}
	if r.build.Ordering == "omnichain" {
		owned := epoch
		epoch = nil
		return r.runOmnichainFinalized(run, owned, control, proofs, result)
	}
	activated := false
	for {
		if err = run.Err(); err != nil {
			return result, err
		}
		if queue == nil {
			if r.options.Live != nil && runtimeHistoricalComplete(control) {
				owned := epoch
				epoch = nil
				return r.runContinuous(run, owned, control, headers, result, ready)
			}

			if !activated && runtimeCanActivate(control) {
				learned, e := epoch.Seal(run)
				if e != nil {
					return result, e
				}
				epoch = nil
				if e = r.domains.ActivateRuntime(run, r.nextID("activate"), control.Cursors); e != nil {
					return result, e
				}
				result.Ready, e = r.domains.Ready(run)
				if e != nil || !result.Ready {
					return result, errors.Join(fmt.Errorf("runtime activation failed"), e)
				}
				ready(true)
				if r.options.OnActivated != nil {
					if e = r.options.OnActivated(context.WithValue(run, runtimeRunnerCallbackKey{}, r)); e != nil {
						return result, e
					}
				}
				activated = true
				epoch, e = r.newEpoch(run, learned)
				if e != nil {
					return result, e
				}
				if e = makeSources(); e != nil {
					return result, e
				}
				control, e = r.domains.Control(run)
				if e != nil {
					return result, e
				}
			}
			ends := map[uint64]uint64{}
			work := false
			for _, c := range r.build.Chains {
				cp, _ := checkpoint.Decode(control.Cursors[runtimeChainID(c)].Checkpoint)
				end := r.ends[runtimeChainID(c)]
				if !activated {
					if !runtimeHistoricalComplete(control) {
						end = control.HistoryCuts[runtimeChainID(c)]
					} else {
						end = cp.BlockNumber
						if target, ok := control.ReplayTargets[runtimeChainID(c)]; ok {
							t, e := checkpoint.Decode(target)
							if e != nil {
								return result, e
							}
							end = t.BlockNumber
						}
					}
				}
				ends[runtimeChainID(c)] = end
				if !store.ManagedLiveReachedBlock(control, runtimeChainID(c), end) {
					work = true
				}
			}
			if !work {
				if !activated {
					return result, fmt.Errorf("bounded range ended before replay/activation completed")
				}
				_, e := epoch.Seal(run)
				if e != nil {
					return result, e
				}
				epoch = nil
				result.Completed = true
				return result, nil
			}
			queue = r.newBlockQueue(epoch.Context(), epoch, control, ends, proofs, headers, children, syncers, realtime)
		}
		batch, e, ok := queue.next()
		if e != nil {
			return result, e
		}
		if !ok {
			queue.close()
			queue = nil
			continue
		}
		if len(batch.Events) != 1 {
			return result, fmt.Errorf("invalid runtime source envelope")
		}
		block, ok := batch.Events[0]["runtimeBlock"].(*runtimeAcquiredBlock)
		if !ok {
			return result, fmt.Errorf("invalid runtime acquired block")
		}
		i, id := block.index, block.id
		input := block.events
		if e = epoch.Clients().Prefetch(epoch.Context(), input); e != nil {
			return result, e
		}
		var receipt store.ManagedLiveBatchResult
		attempts := 0
		for {
			digest, e := runtimeInputsDigest(input)
			if e != nil {
				return result, e
			}
			b := store.ManagedLiveBatch{ID: r.nextID("block"), Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail, Blocks: []store.ManagedLiveBlock{{Head: block.head, InputDigest: digest}}}
			a, e := r.domains.RetainBatch(run, b)
			if e != nil {
				return result, e
			}
			apply := func() error {
				attempts++
				var err error
				receipt, err = epoch.Process(epoch.Context(), b, a, input, block.history)
				return err
			}
			if block.history {
				e = r.retryHistorical(epoch.Context(), apply)
			} else {
				e = apply()
			}
			var invalid *indexing.InvalidEventAccessError
			if e == nil {
				break
			}
			if !block.history || !errors.As(e, &invalid) {
				return result, e
			}
			if runtimeRPCCacheEnabled(r.build.Chains[i]) {
				e = r.domains.WithRuntimeSync(epoch.Context(), func(io context.Context, s *store.SourceSyncStore) error {
					var e error
					chains := []source.SourceHistoricalChain{{Chain: r.build.Chains[i], Local: source.SourceLocalHistoricalOptions{ChainID: id, EventCallbacks: r.build.EventCallbacks[i], Children: children[i], FilterSession: r.session}}}
					input, e = source.RefetchHistoricalSourceEvents(io, chains, input, s, nil)
					return e
				})
			} else {
				var prepared *source.PreparedManagedSourceBlock
				number, _ := uintQuantity(block.raw["number"])
				prepared, e = source.PrepareManagedSourceBlock(epoch.Context(), r.build.Chains[i], r.build.EventCallbacks[i], r.session, children[i], syncers[i], number, block.raw)
				if e == nil {
					input, e = prepared.DecodeInMemory()
				}
			}
			if e != nil {
				return result, e
			}
			result.Refetches++
		}
		headers[id] = block.raw
		control, e = r.domains.Control(run)
		if e != nil {
			return result, e
		}
		if block.history {
			result.HistoricalBlocks++
		} else {
			result.LiveBlocks++
		}
		epoch.Clients().Clear()
		if block.history && r.options.Historical.OnCommitted != nil {
			group := GroupResult{ChainID: id, Events: len(input), Checkpoint: batch.Checkpoint, BlockRange: batch.BlockRange}
			if e := r.notifyHistoricalCommit(run, []GroupResult{group}, receipt, attempts); e != nil {
				return result, e
			}
		}
		if r.options.SourceCheckpoints && r.options.Live != nil {
			// Observe the real business receipt before finality. This is also a
			// cancellable boundary for a crash between execution and safe pruning.
			if e = r.liveNotify(run, "source-block", id, block.head); e != nil {
				return result, e
			}
		}
		cp, _ := checkpoint.Decode(block.head.Checkpoint)
		final, _ := checkpoint.Decode(r.finalized[id].Checkpoint)
		if (r.build.Ordering != string(schema.Isolated) || r.options.Live != nil) && block.history && cp.BlockNumber == min(final.BlockNumber, r.ends[id]) {
			f := store.ManagedLiveFinalize{ID: r.nextID("finalize"), Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail, ExpectedSafeOrdinal: control.SafeOrdinal, Finalized: map[uint64]store.ManagedLiveHead{id: block.head}}
			a, e := r.domains.RetainFinalize(run, f)
			if e != nil {
				return result, e
			}
			if _, e = r.domains.ApplyFinalize(run, f, a); e != nil {
				return result, e
			}
			control, e = r.domains.Control(run)
			if e != nil {
				return result, e
			}
		}
	}
}

// An Omnichain transaction can commit a group for each participating chain.
// Deliver all groups in that transaction before stopping on observer errors.
func (r *RuntimeRunner) notifyHistoricalCommit(ctx context.Context, groups []GroupResult, receipt store.ManagedLiveBatchResult, attempts int) error {
	observe := r.options.Historical.OnCommitted
	if observe == nil || !receipt.Applied {
		return nil
	}
	ctx = context.WithValue(ctx, runtimeRunnerCallbackKey{}, r)
	var errs []error
	for _, group := range groups {
		group.Applied = receipt.Applied
		group.Reconciled = receipt.Reconciled
		group.Attempts = attempts
		if err := observe(ctx, group); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func runtimeChainID(c indexing.Object) uint64 { id, _ := uintQuantity(c["id"]); return id }
