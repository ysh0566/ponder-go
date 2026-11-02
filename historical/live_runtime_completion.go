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
	"sync"
)

type runtimeAcquiredBlock struct {
	index   int
	id      uint64
	head    store.ManagedLiveHead
	raw     indexing.Object
	events  []indexing.Object
	history bool
}
type runtimeBlockQueue struct {
	cancel context.CancelFunc
	stop   func()
	next   func() (*source.SourceEventBatch, error, bool)
	jobs   sync.WaitGroup
}

func (q *runtimeBlockQueue) close() { q.stop(); q.cancel(); q.jobs.Wait() }
func (r *RuntimeRunner) newBlockQueue(ctx context.Context, epoch *indexing.RuntimeEpoch, c store.ManagedLiveControl, ends map[uint64]uint64, proofs map[uint64]map[uint64]store.ManagedLiveHead, headers map[uint64]indexing.Object, children []*source.SourceHistoricalChildren, syncers []*source.SourceHistoricalSync, realtime []*source.SourceRealtimeSync) *runtimeBlockQueue {
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
func (r *RuntimeRunner) acquireRuntimeBlock(ctx context.Context, epoch *indexing.RuntimeEpoch, i int, number uint64, previous store.ManagedLiveHead, rawPrevious indexing.Object, proofs map[uint64]map[uint64]store.ManagedLiveHead, children []*source.SourceHistoricalChildren, syncers []*source.SourceHistoricalSync, realtime []*source.SourceRealtimeSync) (*runtimeAcquiredBlock, error) {
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
		if proofs[id][number] != head {
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

func (r *RuntimeRunner) Run(ctx context.Context) (result RuntimeResult, runErr error) {
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
	defer func() {
		if runErr != nil {
			r.failed.Store(true)
			result.Ready = false
			result.Completed = false
		}
		if queue != nil {
			queue.close()
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
	if err = makeSources(); err != nil {
		return result, err
	}
	if control.Batches == 0 {
		baselines := map[uint64]store.ManagedLiveHead{}
		initial := map[uint64]string{}
		for i, c := range r.build.Chains {
			id, _ := uintQuantity(c["id"])
			f, _ := checkpoint.Decode(r.finalized[id].Checkpoint)
			n := uint64(0)
			if r.options.SourceCheckpoints {
				n = min(r.starts[id], f.BlockNumber)
			} else {
				n = min(r.starts[id]-1, f.BlockNumber, r.ends[id])
			}
			raw, e := rpc.NewActions(epoch.Requester(i)).GetSourceBlockByNumber(epoch.Context(), source.Hex(n), true)
			if e != nil {
				return result, e
			}
			baselines[id], e = runtimeHead(id, raw)
			if e != nil {
				return result, e
			}
			headers[id] = raw
			if r.options.SourceCheckpoints {
				initial[id] = baselines[id].Checkpoint
				if r.starts[id] <= f.BlockNumber {
					cp, _ := checkpoint.Decode(initial[id])
					initial[id] = checkpoint.MustEncode(checkpoint.Block(cp.BlockTimestamp, id, cp.BlockNumber, false))
				}
			}
		}
		// Setup becomes an irreversible safe business basis. Authenticate every
		// acquired baseline against the fixed finalized identity before user
		// callbacks or any bootstrap transaction can persist their effects.
		if r.setup || r.options.SourceCheckpoints {
			for i, c := range r.build.Chains {
				id, _ := uintQuantity(c["id"])
				if _, e := r.acquireFinalizedLineage(epoch.Context(), epoch.Requester(i), id, baselines[id]); e != nil {
					return result, e
				}
			}
		}
		b := store.ManagedLiveBatch{ID: r.nextID("bootstrap"), Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail, Baselines: baselines}
		if r.options.SourceCheckpoints {
			b.InitialCheckpoints = initial
		}
		a, e := r.domains.RetainBatch(run, b)
		if e != nil {
			return result, e
		}
		if r.setup {
			_, e = epoch.Bootstrap(epoch.Context(), b, a)
		} else {
			_, e = r.domains.ApplyRuntimeBatch(epoch.Context(), b, a, nil)
		}
		if e != nil {
			return result, e
		}
		control, e = r.domains.Control(run)
		if e != nil {
			return result, e
		}
		if r.setup && r.options.OnSetupCommitted != nil {
			if e = r.options.OnSetupCommitted(context.WithValue(run, runtimeRunnerCallbackKey{}, r)); e != nil {
				return result, e
			}
		}
	}
	proofs := map[uint64]map[uint64]store.ManagedLiveHead{}
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
		proofs[id], e = r.acquireFinalizedLineage(epoch.Context(), epoch.Requester(i), id, cursor)
		if e != nil {
			return result, e
		}
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
				return r.runContinuous(run, owned, control, headers, result)
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
			_, e = epoch.Process(epoch.Context(), b, a, input, block.history)
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
		if r.options.SourceCheckpoints && r.options.Live != nil {
			// Observe the real business receipt before finality. This is also a
			// cancellable boundary for a crash between execution and safe pruning.
			if e = r.liveNotify(run, "source-block", id, block.head); e != nil {
				return result, e
			}
		}
		cp, _ := checkpoint.Decode(block.head.Checkpoint)
		final, _ := checkpoint.Decode(r.finalized[id].Checkpoint)
		if block.history && cp.BlockNumber == min(final.BlockNumber, r.ends[id]) {
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
func runtimeChainID(c indexing.Object) uint64 { id, _ := uintQuantity(c["id"]); return id }
