package historical

import (
	"context"
	"errors"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	source "ponder.local/ponder-go/runtime"
	"ponder.local/ponder-go/store"
	"sort"
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
		cached, err := source.GetSourceCachedIntervals(ctx, true, filters, facade)
		if err != nil {
			return nil, err
		}
		requester := epoch.Requester(i)
		requests := source.NewSourceSyncProgressRPC(requester.(rpc.AsyncRequester))
		progress, err := source.GetLocalSourceSyncProgress(ctx, id, requests, filters, r.finalBlocks[i], cached)
		if err != nil {
			return nil, err
		}
		children, err := source.GetSourceChildAddresses(ctx, true, filters, facade)
		if err != nil {
			return nil, err
		}
		window, err := uintQuantity(chain["reorgWindow"])
		if err != nil {
			return nil, err
		}
		sources[i] = source.SourceHistoricalChain{Chain: chain, CacheRPCRequests: true, ReorgWindow: int(window), RefreshRequests: requests, Local: source.SourceLocalHistoricalOptions{ChainID: id, Requester: requester, EventCallbacks: r.build.EventCallbacks[i], Children: children, Progress: progress, CachedIntervals: cached, Store: facade, FilterSession: r.session, Acquisition: r.options.Historical.Acquisition}}
		if viem, ok := chain["viemChain"].(map[string]any); ok {
			if ms, ok := viem["blockTime"].(float64); ok {
				sources[i].BlockTimeMS = &ms
			}
		}
	}
	return sources, nil
}
func (r *RuntimeRunner) omnichainAcquiredGroups(ctx context.Context, epoch *indexing.RuntimeEpoch, control store.ManagedLiveControl, groups []source.SourceEventGroup, proofs map[uint64]map[uint64]store.ManagedLiveHead) ([]*runtimeAcquiredBlock, map[uint64]string, error) {
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
		for n := next; n <= end.BlockNumber; n++ {
			input := events[n]
			if len(input) != 1 {
				return nil, nil, fmt.Errorf("omnichain finalized block closure requires one interval1 block event per block")
			}
			raw, err := rpc.NewActions(epoch.Requester(i)).GetSourceBlockByNumber(ctx, source.Hex(n), true)
			if err != nil {
				return nil, nil, err
			}
			head, err := runtimeHead(g.ChainID, raw)
			if err != nil {
				return nil, nil, err
			}
			prior, _ := checkpoint.Decode(previous.Checkpoint)
			initial := n == prior.BlockNumber && head == previous
			if head != proofs[g.ChainID][n] || (!initial && head.ParentHash != previous.Hash) {
				return nil, nil, fmt.Errorf("omnichain block differs from acquired finalized lineage")
			}
			blocks = append(blocks, &runtimeAcquiredBlock{index: i, id: g.ChainID, head: head, raw: raw, events: input, history: true})
			previous = head
		}
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].head.Checkpoint < blocks[j].head.Checkpoint })
	return blocks, positions, nil
}
func (r *RuntimeRunner) runOmnichainFinalized(ctx context.Context, epoch *indexing.RuntimeEpoch, control store.ManagedLiveControl, proofs map[uint64]map[uint64]store.ManagedLiveHead, result RuntimeResult) (out RuntimeResult, err error) {
	out = result
	defer func() {
		if epoch != nil {
			err = errors.Join(err, epoch.Abort(context.Background()))
		}
	}()
	chains, err := r.omnichainHistoricalSources(epoch.Context(), epoch)
	if err != nil {
		return out, err
	}
	crash := make([]source.SourceCrashRecoveryCheckpoint, 0, len(chains))
	for _, c := range chains {
		crash = append(crash, source.SourceCrashRecoveryCheckpoint{ChainID: c.Local.ChainID, Checkpoint: store.ManagedLiveCursorCheckpoint(control, c.Local.ChainID)})
	}
	options := source.SourceHistoricalCoordinatorOptions{Chains: chains, BuildChains: chains, CrashRecovery: crash, QuerySize: 12000}
	for output, e := range source.HistoricalSourceOmnichain(epoch.Context(), options) {
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
		for {
			b := store.ManagedLiveBatch{ID: r.nextID("omni-history"), Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail}
			partitions := make([][]indexing.Object, len(blocks))
			for i, block := range blocks {
				partitions[i] = block.events
				if e = epoch.Clients().Prefetch(epoch.Context(), block.events); e != nil {
					return out, e
				}
				digest, e := runtimeInputsDigest(block.events)
				if e != nil {
					return out, e
				}
				entry := store.ManagedLiveBlock{Head: block.head, InputDigest: digest}
				cp, _ := checkpoint.Decode(positions[block.id])
				headCP, _ := checkpoint.Decode(block.head.Checkpoint)
				if cp.BlockNumber == headCP.BlockNumber {
					entry.SourceCheckpoint = positions[block.id]
				}
				b.Blocks = append(b.Blocks, entry)
			}
			acquired, e := r.domains.RetainBatch(ctx, b)
			if e != nil {
				return out, e
			}
			_, e = epoch.ProcessHistoricalBlocks(epoch.Context(), b, acquired, partitions)
			if e == nil {
				break
			}
			var invalid *indexing.InvalidEventAccessError
			if !errors.As(e, &invalid) {
				return out, e
			}
			for _, block := range blocks {
				e = r.domains.WithRuntimeSync(epoch.Context(), func(io context.Context, s *store.SourceSyncStore) error {
					var err error
					block.events, err = source.RefetchHistoricalSourceEvents(io, []source.SourceHistoricalChain{chains[block.index]}, block.events, s, nil)
					return err
				})
				if e != nil {
					return out, e
				}
			}
			out.Refetches++
		}
		out.HistoricalBlocks += len(blocks)
		control, e = r.domains.Control(ctx)
		if e != nil {
			return out, e
		}
		epoch.Clients().Clear()
	}
	for _, chain := range r.build.Chains {
		id := runtimeChainID(chain)
		if !store.ManagedLiveReachedBlock(control, id, r.ends[id]) {
			return out, fmt.Errorf("omnichain historical coordinator ended before complete final range")
		}
	}
	if _, err = epoch.Seal(ctx); err != nil {
		return out, err
	}
	epoch = nil
	if err = r.domains.ActivateRuntime(ctx, r.nextID("omni-activate"), control.Cursors); err != nil {
		return out, err
	}
	out.Ready, err = r.domains.Ready(ctx)
	if err != nil || !out.Ready {
		return out, errors.Join(fmt.Errorf("omnichain activation failed"), err)
	}
	if r.options.OnActivated != nil {
		if err = r.options.OnActivated(context.WithValue(ctx, runtimeRunnerCallbackKey{}, r)); err != nil {
			return out, err
		}
	}
	out.Completed = true
	return out, nil
}
