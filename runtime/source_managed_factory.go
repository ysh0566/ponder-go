package runtime

import (
	"context"
	"errors"
	"fmt"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"sort"
)

// ErrManagedFactoryAcquisition marks transport failures during branch rebuild.
var ErrManagedFactoryAcquisition = errors.New("factory branch acquisition failed")

type managedFactoryRequester struct{ rpc.Requester }

func (r managedFactoryRequester) Call(ctx context.Context, method string, params any, result any) error {
	if err := r.Requester.Call(ctx, method, params, result); err != nil {
		return errors.Join(ErrManagedFactoryAcquisition, err)
	}
	return nil
}

// RestoreManagedFactoryBranch reconstructs children and their birth provenance
// from the supplied authenticated branch. It deliberately ignores disk child
// rows: sync and business commits are separate fault domains. A fresh generation
// must not inherit orphan births or mutable state from its predecessor.
//
// The first implementation scans the factory prefix on each open/handoff. It
// trades acquisition work for a branch-bound reconstruction; it is not a cache
// or performance equivalence claim.
func RestoreManagedFactoryBranch(ctx context.Context, requester rpc.Requester, children *SourceHistoricalChildren, finalized filter.Object, retained []filter.Object, filters []filter.Object, chain SourceRealtimeChain) (*SourceRealtimeSync, error) {
	s, err := NewManagedSourceRealtimeSync(requester, children, finalized, retained, filters, chain)
	if err != nil {
		return nil, err
	}
	factories := []filter.Object{}
	seen := map[string]bool{}
	first := ^uint64(0)
	for _, f := range filters {
		for _, factory := range filter.GetFilterFactories(f) {
			id, _ := factory["id"].(string)
			if seen[id] {
				continue
			}
			seen[id] = true
			factories = append(factories, factory)
			n := uint64(filter.FilterFromBlock(factory))
			if n < first {
				first = n
			}
		}
	}
	if len(factories) == 0 {
		return s, nil
	}
	// Build a discovery-only source synchronizer without dropping factories whose
	// discovery end is now finalized. Their children can still match later events.
	// Bloom filters are derived by the ordinary constructor. A discovery-only
	// filter keeps each factory's own inclusive from/to bounds untouched.
	discoveryFilters := []filter.Object{}
	for _, factory := range factories {
		discoveryFilters = append(discoveryFilters, filter.Object{"type": "log", "address": factory, "fromBlock": uint64(0)})
	}
	discover, err := NewSourceRealtimeSync(managedFactoryRequester{requester}, nil, children, filter.Object{"number": "0x0"}, discoveryFilters, chain, SourceRealtimeOptions{})
	if err != nil {
		return nil, err
	}
	discover.kinds = map[string][]filter.Object{}
	discover.logBlooms = nil
	tip := finalized
	if len(retained) > 0 {
		tip = retained[len(retained)-1]
	}
	n, err := sourceHistoricalNumber(tip["number"])
	if err != nil {
		return nil, err
	}
	if n < first {
		return s, nil
	}
	// Only one full RPC block is retained at a time. Walk authenticated hashes
	// backwards, using earliest-birth upserts rather than the live insert-first
	// rule; provenance is retained only above the finalized boundary.
	finalNumber, _ := sourceHistoricalNumber(finalized["number"])
	fragmentFactory := map[string][]string{}
	for i, factory := range discover.factories {
		id, _ := factory["id"].(string)
		for _, fragment := range discover.factoryFragments[i] {
			fragmentFactory[fragment] = append(fragmentFactory[fragment], id)
		}
	}
	current := sourceRealtimeLight(tip)
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		number, e := sourceHistoricalNumber(current["number"])
		if e != nil {
			return nil, e
		}
		at, e := sourceHistoricalNumber(current["timestamp"])
		if e != nil {
			return nil, e
		}
		raw, e := discover.getBlockByHash(ctx, fmt.Sprint(current["hash"]), true)
		if e != nil {
			return nil, e
		}
		if sourceRealtimeLight(raw)["hash"] != current["hash"] || raw["parentHash"] != current["parentHash"] || raw["number"] != current["number"] || raw["timestamp"] != current["timestamp"] {
			return nil, fmt.Errorf("factory reconstruction header identity differs")
		}
		data, e := discover.fetchBlockEventData(ctx, raw)
		if e != nil {
			return nil, e
		}
		for _, entry := range data.children {
			ids, ok := fragmentFactory[entry.FragmentID]
			if !ok {
				return nil, fmt.Errorf("unknown factory fragment in reconstruction")
			}
			for _, id := range ids {
				for _, address := range entry.Addresses {
					children.Upsert(id, filter.ChildAddress{Address: address, Block: number})
				}
			}
		}
		if number > finalNumber {
			copied := make([]SourceRealtimeFragmentChildren, len(data.children))
			for j, entry := range data.children {
				copied[j] = SourceRealtimeFragmentChildren{FragmentID: entry.FragmentID, Addresses: append([]string(nil), entry.Addresses...)}
			}
			s.childrenPerBlock[number] = copied
		}
		if number == first {
			break
		}
		parent, e := discover.getBlockByHash(ctx, fmt.Sprint(current["parentHash"]), false)
		if e != nil {
			return nil, e
		}
		pn, e := sourceHistoricalNumber(parent["number"])
		if e != nil {
			return nil, e
		}
		pt, e := sourceHistoricalNumber(parent["timestamp"])
		if e != nil || pn+1 != number || pt > at || parent["hash"] != current["parentHash"] {
			return nil, fmt.Errorf("factory reconstruction ancestry differs")
		}
		current = sourceRealtimeLight(parent)
	}
	// Restore chronological insertion order without retaining full RPC blocks.
	children.mu.Lock()
	for id := range seen {
		set := children.values[id]
		sort.SliceStable(set.entries, func(i, j int) bool { return set.entries[i].Block < set.entries[j].Block })
		for i, entry := range set.entries {
			set.indices[entry.Address] = i
		}
	}
	children.mu.Unlock()
	return s, nil
}
