package historical

import (
	"context"
	"errors"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/rpc"
	source "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/store"
	"iter"
	"sort"
	"time"
)

func (r *RuntimeRunner) omnichainHistoricalSources(ctx context.Context, epoch *indexing.RuntimeEpoch) ([]source.SourceHistoricalChain, error) {
	sources := make([]source.SourceHistoricalChain, len(r.build.Chains))
	facade := runtimeHistoricalStore{r.domains}
	for i, chain := range r.build.Chains {
		id := runtimeChainID(chain)
		filters := make([]filter.Object, len(r.build.EventCallbacks[i]))
		for j, cb := range r.build.EventCallbacks[i] {
			filters[j] = cb["filter"].(map[string]any)
		}
		cache := runtimeRPCCacheEnabled(chain)
		cached, err := source.GetSourceCachedIntervals(ctx, cache, filters, facade)
		if err != nil {
			return nil, err
		}
		requester := epoch.Requester(i)
		requests := source.NewSourceSyncProgressRPC(requester.(rpc.AsyncRequester))
		progress, err := source.GetLocalSourceSyncProgress(ctx, id, requests, filters, r.finalBlocks[i], cached)
		if err != nil {
			return nil, err
		}
		children, err := source.GetSourceChildAddresses(ctx, cache, filters, facade)
		if err != nil {
			return nil, err
		}
		window, err := uintQuantity(chain["reorgWindow"])
		if err != nil {
			return nil, err
		}
		sources[i] = source.SourceHistoricalChain{
			Chain: chain, CacheRPCRequests: cache, ReorgWindow: int(window), RefreshRequests: requests,
			Local: source.SourceLocalHistoricalOptions{
				ChainID: id, Requester: requester, EventCallbacks: r.build.EventCallbacks[i],
				Children: children, Progress: progress, CachedIntervals: cached, Store: facade,
				FilterSession: r.session, Clock: r.options.Historical.Clock, Acquisition: r.options.Historical.Acquisition,
			},
		}
		if viem, ok := chain["viemChain"].(map[string]any); ok {
			if ms, ok := viem["blockTime"].(float64); ok {
				sources[i].BlockTimeMS = &ms
			}
		}
	}
	return sources, nil
}

// Only source-bearing heights, the initial Down block and the logical group
// endpoint require journal segments. The intervening headers are authenticated
// by one persisted range per gap.
func omnichainGroupHeights(start, end uint64, includeStart, paged bool, events map[uint64][]indexing.Object) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		if !paged {
			for n := start; n <= end; n++ {
				if !yield(n) {
					return
				}
			}
			return
		}
		if start > end {
			return
		}
		heights := make([]uint64, 0, len(events)+2)
		for n := range events {
			heights = append(heights, n)
		}
		if includeStart && start != end && len(events[start]) == 0 {
			heights = append(heights, start)
		}
		if len(events[end]) == 0 {
			heights = append(heights, end)
		}
		sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
		for _, n := range heights {
			if !yield(n) {
				return
			}
		}
	}
}

func (r *RuntimeRunner) omnichainAcquiredGroups(ctx context.Context, epoch *indexing.RuntimeEpoch, control store.ManagedLiveControl, groups []source.SourceEventGroup, proofs map[uint64]*runtimeLineage) ([]*runtimeAcquiredBlock, map[uint64]string, error) {
	positions := map[uint64]string{}
	blocks := []*runtimeAcquiredBlock{}
	byID := map[uint64]int{}
	for i, c := range r.build.Chains {
		byID[runtimeChainID(c)] = i
	}
	for _, g := range groups {
		i, ok := byID[g.ChainID]
		if !ok {
			return nil, nil, fmt.Errorf("omnichain yielded unknown chain")
		}
		if _, duplicate := positions[g.ChainID]; duplicate {
			return nil, nil, fmt.Errorf("omnichain yielded duplicate chain group")
		}
		positions[g.ChainID] = g.Checkpoint
		end, err := checkpoint.Decode(g.Checkpoint)
		if err != nil || end.ChainID != g.ChainID {
			return nil, nil, fmt.Errorf("invalid omnichain group checkpoint")
		}
		next, err := store.ManagedLiveNextBlock(control, g.ChainID)
		if err != nil {
			return nil, nil, err
		}
		events := map[uint64][]indexing.Object{}
		for _, ev := range g.Events {
			raw, ok := ev["checkpoint"].(string)
			if !ok {
				return nil, nil, fmt.Errorf("omnichain event lacks checkpoint")
			}
			cp, err := checkpoint.Decode(raw)
			if err != nil || cp.ChainID != g.ChainID || cp.BlockNumber < next || cp.BlockNumber > end.BlockNumber {
				return nil, nil, fmt.Errorf("omnichain event outside acquired group")
			}
			events[cp.BlockNumber] = append(events[cp.BlockNumber], ev)
		}
		previous := control.Cursors[g.ChainID]
		actions := rpc.NewActions(epoch.Requester(i))
		priorCP, _ := checkpoint.Decode(previous.Checkpoint)
		for n := range omnichainGroupHeights(next, end.BlockNumber, next == priorCP.BlockNumber, r.sparseChains[g.ChainID], events) {
			input := events[n]
			if len(input) == 0 && !r.sparseChains[g.ChainID] {
				return nil, nil, fmt.Errorf("omnichain block partition lacks its closing callback")
			}
			// Empty event ranges still need a fresh lineage check. Their
			// partitions have no callback inputs that require transaction bodies.
			raw, err := actions.GetSourceBlockByNumber(ctx, source.Hex(n), len(input) != 0)
			if err != nil {
				return nil, nil, err
			}
			head, err := runtimeHead(g.ChainID, raw)
			if err != nil {
				return nil, nil, err
			}
			prior, _ := checkpoint.Decode(previous.Checkpoint)
			initial := n == prior.BlockNumber && head == previous
			proven, err := proofs[g.ChainID].head(n)
			if err != nil {
				return nil, nil, err
			}
			var first store.ManagedLiveHead
			parent := head.ParentHash
			if r.sparseChains[g.ChainID] && n > prior.BlockNumber+1 {
				first, err = proofs[g.ChainID].head(prior.BlockNumber + 1)
				if err != nil {
					return nil, nil, err
				}
				parent = first.ParentHash
			}
			if head != proven || (!initial && parent != previous.Hash) {
				return nil, nil, fmt.Errorf("omnichain block differs from acquired finalized lineage")
			}
			if err := r.validateOmnichainPartition(g.ChainID, input, head); err != nil {
				return nil, nil, err
			}
			blocks = append(blocks, &runtimeAcquiredBlock{rangeFirst: first, index: i, id: g.ChainID, head: head, events: input, history: true})
			previous = head
		}
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].head.Checkpoint < blocks[j].head.Checkpoint })
	return blocks, positions, nil
}
func (r *RuntimeRunner) refetchOmnichainBlock(ctx context.Context, chain source.SourceHistoricalChain, block *runtimeAcquiredBlock) ([]indexing.Object, error) {
	if chain.CacheRPCRequests {
		var events []indexing.Object
		err := r.domains.WithRuntimeSync(ctx, func(io context.Context, s *store.SourceSyncStore) error {
			var err error
			events, err = source.RefetchHistoricalSourceEvents(io, []source.SourceHistoricalChain{chain}, block.events, s, nil)
			return err
		})
		return events, err
	}
	// The source store has no rows for a disabled chain. Bind the fresh
	// acquisition to the header already authenticated for this transaction.
	cp, err := checkpoint.Decode(block.head.Checkpoint)
	if err != nil {
		return nil, err
	}
	local := chain.Local
	syncer, err := source.NewSourceHistoricalSync(block.id, local.Requester, local.Actions, local.Children, local.Acquisition)
	if err != nil {
		return nil, err
	}
	header := indexing.Object{"number": source.Hex(cp.BlockNumber), "timestamp": source.Hex(cp.BlockTimestamp), "hash": block.head.Hash, "parentHash": block.head.ParentHash}
	prepared, err := source.PrepareManagedSourceBlock(ctx, chain.Chain, local.EventCallbacks, local.FilterSession, local.Children, syncer, cp.BlockNumber, header)
	if err != nil {
		return nil, err
	}
	events, err := prepared.DecodeInMemoryContext(ctx)
	if err != nil {
		return nil, err
	}
	return events, r.validateOmnichainPartition(block.id, events, block.head)
}

func (r *RuntimeRunner) runOmnichainFinalized(ctx context.Context, epoch *indexing.RuntimeEpoch, control store.ManagedLiveControl, proofs map[uint64]*runtimeLineage, result RuntimeResult, window *metrics.BackfillWindow) (out RuntimeResult, err error) {
	out = result
	defer func() {
		if epoch != nil {
			err = errors.Join(err, epoch.Abort(context.Background()))
		}
	}()
	for id, head := range control.LogOnlyScanHeads {
		cp, _ := checkpoint.Decode(head.Checkpoint)
		proven, err := proofs[id].head(cp.BlockNumber)
		if err != nil {
			return out, fmt.Errorf("scanned endpoint: %w", err)
		}
		if proven != head {
			return out, fmt.Errorf("scanned endpoint differs from finalized lineage")
		}
	}
	chains, err := r.omnichainHistoricalSources(epoch.Context(), epoch)
	if err != nil {
		return out, err
	}
	crash := make([]source.SourceCrashRecoveryCheckpoint, 0, len(chains))
	for _, c := range chains {
		crash = append(crash, source.SourceCrashRecoveryCheckpoint{ChainID: c.Local.ChainID, Checkpoint: store.ManagedLiveCursorCheckpoint(control, c.Local.ChainID)})
	}
	options := source.SourceHistoricalCoordinatorOptions{Chains: chains, BuildChains: chains, CrashRecovery: crash, QuerySize: r.options.Historical.QuerySize, Clock: r.options.Historical.Clock}
	groups := source.HistoricalSourceOmnichain(epoch.Context(), options)
	for output, e := range timedHistoricalGroups(ctx, groups) {
		if e != nil {
			return out, e
		}
		if output.Type == "pending" {
			if len(output.Pending) != 0 {
				return out, fmt.Errorf("omnichain finalized closure cannot execute pending live events")
			}
			continue
		}
		if output.Type != "events" {
			return out, fmt.Errorf("unknown omnichain historical output %s", output.Type)
		}
		blocks, positions, e := r.omnichainAcquiredGroups(epoch.Context(), epoch, control, output.Groups, proofs)
		if e != nil {
			return out, e
		}
		if len(blocks) == 0 {
			continue
		}
		readHeader := func(chain, number uint64) (store.ManagedLiveHead, error) { return proofs[chain].head(number) }
		var receipt store.ManagedLiveBatchResult
		attempts := 0
		for {
			b := store.ManagedLiveBatch{ID: r.nextID("omni-history"), Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail}
			partitions := make([][]indexing.Object, len(blocks))
			for i, block := range blocks {
				partitions[i] = block.events
				if e = epoch.PrefetchHistorical(epoch.Context(), block.events); e != nil {
					return out, e
				}
				digest, e := runtimeInputsDigest(block.events)
				if e != nil {
					return out, e
				}
				entry := store.ManagedLiveBlock{Head: block.head, InputDigest: digest}
				if block.rangeFirst != (store.ManagedLiveHead{}) {
					if err := entry.BindLogOnlyRange(ctx, block.rangeFirst, readHeader); err != nil {
						return out, err
					}
				}
				if r.sparseChains[block.id] {
					entry.LogEventCount = uint64(len(block.events))
					if len(block.events) > 0 {
						entry.LogFirstEvent = block.events[0]["checkpoint"].(string)
						entry.LogLastEvent = block.events[len(block.events)-1]["checkpoint"].(string)
					}
				}
				cp, _ := checkpoint.Decode(positions[block.id])
				headCP, _ := checkpoint.Decode(block.head.Checkpoint)
				if cp.BlockNumber == headCP.BlockNumber {
					entry.SourceCheckpoint = positions[block.id]
				}
				b.Blocks = append(b.Blocks, entry)
			}
			var acquired *store.ManagedLiveAcquisition
			if len(r.sparseChains) != 0 {
				acquired, e = r.domains.RetainPagedLogOnlyBatch(ctx, b, readHeader)
			} else {
				acquired, e = r.domains.RetainBatch(ctx, b)
			}
			if e != nil {
				return out, e
			}
			e = r.retryHistorical(epoch.Context(), func() error {
				attempts++
				var err error
				receipt, err = epoch.ProcessHistoricalBlocks(epoch.Context(), b, acquired, partitions)
				return err
			})
			if e == nil {
				break
			}
			var invalid *indexing.InvalidEventAccessError
			if !errors.As(e, &invalid) {
				return out, e
			}
			for _, block := range blocks {
				block.events, e = r.refetchOmnichainBlock(epoch.Context(), chains[block.index], block)
				if e != nil {
					return out, e
				}
			}
			out.Refetches++
		}
		out.HistoricalBlocks += len(blocks)
		for _, block := range blocks {
			r.observeSyncHead(ctx, block.index, block.head)
			if block.rangeFirst != (store.ManagedLiveHead{}) {
				first, _ := checkpoint.Decode(block.rangeFirst.Checkpoint)
				last, _ := checkpoint.Decode(block.head.Checkpoint)
				out.HistoricalBlocks += int(last.BlockNumber - first.BlockNumber)
			}
		}
		if registry := metrics.ChainSync(ctx); registry != nil {
			// The acquired batch is sorted by checkpoint.
			last := blocks[len(blocks)-1]
			cp, _ := checkpoint.Decode(last.head.Checkpoint)
			r.indexingTime.Commit(registry, last.id, cp.BlockTimestamp)
		}
		control, e = r.domains.Control(ctx)
		if e != nil {
			return out, e
		}
		epoch.Clients().Clear()
		if r.options.Historical.OnCommitted != nil || r.options.OnCommitted != nil {
			counts := make(map[uint64]int, len(output.Groups))
			for _, block := range blocks {
				counts[block.id] += len(block.events)
			}
			groups := make([]GroupResult, 0, len(output.Groups))
			for _, g := range output.Groups {
				groups = append(groups, GroupResult{ChainID: g.ChainID, Checkpoint: g.Checkpoint, BlockRange: g.BlockRange, Events: counts[g.ChainID]})
			}
			if err := r.notifyCommitted(ctx, groups, receipt, control, attempts, true); err != nil {
				return out, err
			}
		}
	}
	for _, chain := range r.build.Chains {
		id := runtimeChainID(chain)
		if !r.sparseChains[id] && !store.ManagedLiveReachedBlock(control, id, r.ends[id]) {
			return out, fmt.Errorf("omnichain historical coordinator ended before complete final range")
		}
	}
	if _, err = epoch.Seal(ctx); err != nil {
		return out, err
	}
	epoch = nil
	if len(r.sparseChains) != 0 {
		err = r.domains.ActivatePagedLogOnlyRuntime(ctx, r.nextID("omni-activate"), control.Cursors, func(chain, number uint64) (store.ManagedLiveHead, error) { return proofs[chain].head(number) })
	} else {
		err = r.domains.ActivateRuntime(ctx, r.nextID("omni-activate"), control.Cursors)
	}
	if err != nil {
		return out, err
	}
	out.Ready, err = r.domains.Ready(ctx)
	if err != nil || !out.Ready {
		return out, errors.Join(fmt.Errorf("omnichain activation failed"), err)
	}
	window.Finish()
	if registry := metrics.ChainSync(ctx); registry != nil {
		var end string
		for i, chain := range r.build.Chains {
			id := runtimeChainID(chain)
			// Activation verified this inclusive scan end, even if the last
			// matching event was in an earlier block.
			if head, err := proofs[id].head(r.ends[id]); err == nil {
				r.observeSyncHead(ctx, i, head)
				end = max(end, head.Checkpoint)
			}
			registry.SetSyncPhase(fmt.Sprint(chain["name"]), true)
		}
		if cp, err := checkpoint.Decode(end); err == nil {
			r.indexingTime.Commit(registry, cp.ChainID, cp.BlockTimestamp)
		}
	}
	if r.options.OnActivated != nil {
		if err = r.options.OnActivated(context.WithValue(ctx, runtimeRunnerCallbackKey{}, r)); err != nil {
			return out, err
		}
	}
	out.Completed = true
	return out, nil
}

// The coordinator prefetches while its consumer writes. The extract sample
// measures only the wait for the next yield. Early consumer exits drop that pair.
func timedHistoricalGroups[T any](ctx context.Context, groups iter.Seq2[T, error]) iter.Seq2[T, error] {
	registry := metrics.Historical(ctx)
	if registry == nil {
		return groups
	}
	return func(yield func(T, error) bool) {
		start := time.Now()
		for group, err := range groups {
			received := time.Now()
			if !yield(group, err) {
				return
			}
			if err == nil {
				registry.ObserveHistoricalGroup(received.Sub(start), time.Since(received))
			}
			start = time.Now()
		}
	}
}
