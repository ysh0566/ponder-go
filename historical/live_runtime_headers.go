package historical

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
)

// This is an optimization, never a proof store. Evicted identities are fetched
// by their authenticated hash. Total history and orphan count cannot grow it.
type runtimeLiveHeaders struct {
	values map[string]indexing.Object
	order  []string
	limit  int
}

func newRuntimeLiveHeaders(limit int) *runtimeLiveHeaders {
	if limit < 1 {
		limit = 1
	}
	return &runtimeLiveHeaders{values: map[string]indexing.Object{}, limit: limit}
}
func (h *runtimeLiveHeaders) get(hash string) indexing.Object {
	if raw := h.values[hash]; raw != nil {
		return runtimeLight(raw)
	}
	return nil
}
func (h *runtimeLiveHeaders) put(raw indexing.Object) {
	hash := fmt.Sprint(raw["hash"])
	if _, present := h.values[hash]; !present {
		h.order = append(h.order, hash)
	}
	h.values[hash] = runtimeLight(raw)
	for len(h.order) > h.limit {
		delete(h.values, h.order[0])
		h.order = h.order[1:]
	}
}

// Global undo may rewind one chain below its retained public finality. The
// source's temporary private floor can then emit already-confirmed prefixes.
// Authenticate the complete old finalized predecessor chain before ignoring
// them; the durable store's monotonic finality fence remains unchanged.
func (r *RuntimeRunner) acceptContinuousFinality(ctx context.Context, epoch *indexing.RuntimeEpoch, i int, c store.ManagedLiveControl, head store.ManagedLiveHead, known *runtimeLiveHeaders) (bool, error) {
	id := runtimeChainID(r.build.Chains[i])
	prior := r.finalized[id]
	if h, ok := c.Finality[id]; ok {
		prior = h
	}
	at, err := checkpoint.Decode(head.Checkpoint)
	if err != nil {
		return false, err
	}
	old, err := checkpoint.Decode(prior.Checkpoint)
	if err != nil {
		return false, err
	}
	if at.BlockNumber >= old.BlockNumber {
		if at.BlockNumber == old.BlockNumber && head != prior {
			return false, store.ErrManagedLiveDeepReorg
		}
		return true, nil
	}
	current := prior
	for old.BlockNumber > at.BlockNumber {
		raw := known.get(current.Hash)
		if raw == nil {
			raw, err = runtimeHashBlock(ctx, epoch.Requester(i), current.Hash)
			if err != nil {
				return false, err
			}
			known.put(raw)
		}
		actual, err := runtimeHead(id, raw)
		if err != nil || actual != current {
			return false, store.ErrManagedLiveDeepReorg
		}
		parent := known.get(current.ParentHash)
		if parent == nil {
			parent, err = runtimeHashBlock(ctx, epoch.Requester(i), current.ParentHash)
			if err != nil {
				return false, err
			}
			known.put(parent)
		}
		next, err := runtimeHead(id, parent)
		if err != nil {
			return false, err
		}
		cp, err := checkpoint.Decode(next.Checkpoint)
		if err != nil || cp.BlockNumber+1 != old.BlockNumber || cp.BlockTimestamp > old.BlockTimestamp || next.Hash != current.ParentHash {
			return false, store.ErrManagedLiveDeepReorg
		}
		current, old = next, cp
	}
	if current != head {
		return false, store.ErrManagedLiveDeepReorg
	}
	return false, nil
}
func runtimeHeaderScalar(v any) any {
	switch n := v.(type) {
	case *big.Int:
		if n != nil {
			return new(big.Int).Set(n)
		}
	case big.Int:
		return *new(big.Int).Set(&n)
	}
	return v
}
