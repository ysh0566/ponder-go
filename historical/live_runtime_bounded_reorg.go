package historical

import (
	"context"
	"errors"
	"fmt"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	source "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/store"
)

type runtimeBoundedFork struct {
	index int
	head  store.ManagedLiveHead
}

func (f *runtimeBoundedFork) Error() string {
	return fmt.Sprintf("bounded runtime acquired a competing block at %s", f.head.Checkpoint)
}

func boundedHeaderAt(ctx context.Context, req rpc.Requester, id, number uint64) (store.ManagedLiveHead, error) {
	raw, err := rpc.NewActions(req).GetSourceBlockByNumber(ctx, source.Hex(number), false)
	if err != nil {
		return store.ManagedLiveHead{}, err
	}
	if n, err := uintQuantity(raw["number"]); err != nil || n != number {
		return store.ManagedLiveHead{}, fmt.Errorf("RPC returned a different height for chain %d block %d", id, number)
	}
	return runtimeHead(id, raw)
}

// There is no next block to reveal a replacement of a completed chain's tip.
// Check once after draining all workers, including peers that finished earlier.
func (r *RuntimeRunner) checkBoundedTips(ctx context.Context, epoch *indexing.RuntimeEpoch, c store.ManagedLiveControl) error {
	for i, chain := range r.build.Chains {
		id := runtimeChainID(chain)
		cursor := c.Cursors[id]
		at, _ := checkpoint.Decode(cursor.Checkpoint)
		final, _ := checkpoint.Decode(r.finalized[id].Checkpoint)
		if at.BlockNumber <= final.BlockNumber {
			continue
		}
		head, err := boundedHeaderAt(ctx, epoch.Requester(i), id, at.BlockNumber)
		if err != nil {
			return err
		}
		if head != cursor {
			return &runtimeBoundedFork{i, head}
		}
	}
	return nil
}

// Find the shared ancestor using durable receipts for the accepted branch.
// If the canonical chain changes again during acquisition, restart from its
// current block at the observed height. A static inconsistent response fails.
func (r *RuntimeRunner) boundedReorgBranch(ctx context.Context, epoch *indexing.RuntimeEpoch, c store.ManagedLiveControl, fork *runtimeBoundedFork) (store.ManagedLiveDomainReorg, error) {
	var result store.ManagedLiveDomainReorg
	req := epoch.Requester(fork.index)
	id := runtimeChainID(r.build.Chains[fork.index])
	cursor := c.Cursors[id]
	tip, _ := checkpoint.Decode(cursor.Checkpoint)
	final, _ := checkpoint.Decode(r.finalized[id].Checkpoint)
	if tip.BlockNumber < final.BlockNumber {
		return result, store.ErrManagedLiveDeepReorg
	}
	lineage, err := r.domains.RuntimeUnfinalizedLineage(ctx, id)
	if err != nil {
		return result, err
	}
	start := fork.head
	head := start
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		at, _ := checkpoint.Decode(head.Checkpoint)
		if at.BlockNumber <= tip.BlockNumber {
			old, ok := lineage[at.BlockNumber]
			if !ok {
				return result, store.ErrManagedLiveDeepReorg
			}
			if old == head {
				result.Reorg.Ancestor = head
				return result, nil
			}
			result.Orphaned = append(result.Orphaned, old)
		}
		if at.BlockNumber <= final.BlockNumber {
			return result, store.ErrManagedLiveDeepReorg
		}
		parent, err := boundedHeaderAt(ctx, req, id, at.BlockNumber-1)
		if err != nil {
			return result, err
		}
		p, _ := checkpoint.Decode(parent.Checkpoint)
		if parent.Hash != head.ParentHash || p.BlockTimestamp > at.BlockTimestamp {
			from, _ := checkpoint.Decode(start.Checkpoint)
			current, err := boundedHeaderAt(ctx, req, id, from.BlockNumber)
			if err != nil {
				return result, err
			}
			if current == start {
				return result, errors.New("bounded RPC returned inconsistent parent headers")
			}
			start, head = current, current
			result.Orphaned = nil
			continue
		}
		head = parent
	}
}

// The caller seals the active epoch before changing either storage domain.
func (r *RuntimeRunner) applyBoundedReorg(ctx context.Context, index int, c store.ManagedLiveControl, request store.ManagedLiveDomainReorg) (store.ManagedLiveControl, error) {
	if c.Serving {
		if err := r.domains.PauseRuntime(ctx, r.nextID("reorg-pause")); err != nil {
			return c, err
		}
		var err error
		c, err = r.domains.Control(ctx)
		if err != nil {
			return c, err
		}
	}
	if len(request.Orphaned) == 0 {
		return c, nil
	}
	request.Reorg.ID = r.nextID("bounded-reorg")
	request.Reorg.Generation = c.Generation
	request.Reorg.ExpectedLastOperation = c.LastOperation
	request.Reorg.ExpectedTail = c.Tail
	request.Reorg.ReplayBeforeServing = true
	if err := r.coordinateRuntimeReorg(ctx, index, request); err != nil {
		return c, err
	}
	return r.domains.Control(ctx)
}

func runtimeCursorHeader(head store.ManagedLiveHead) indexing.Object {
	cp, _ := checkpoint.Decode(head.Checkpoint)
	return indexing.Object{"number": source.Hex(cp.BlockNumber), "timestamp": source.Hex(cp.BlockTimestamp), "hash": head.Hash, "parentHash": head.ParentHash}
}
