package runtime

import (
	"context"
	"fmt"
	"math"
	"reflect"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

func sourceCachedEntries(cached []store.SourceIntervalEntry, key filter.Object) ([]store.SourceFragmentIntervals, error) {
	for _, entry := range cached {
		if reflect.ValueOf(entry.Key).Pointer() == reflect.ValueOf(key).Pointer() {
			return entry.Values, nil
		}
	}
	return nil, fmt.Errorf("source cached interval map key missing")
}

// sourceMissingIntervals computes the parent-dependent sparse coverage without
// changing the cache entries or widening gaps into a bounding interval.
func sourceMissingIntervals(f filter.Object, target *store.SourceInterval, cached []store.SourceIntervalEntry) ([]store.SourceInterval, error) {
	bounds, err := sourceFilterTotalInterval(f)
	if err != nil {
		return nil, err
	}
	total := []store.SourceInterval{bounds}
	end := bounds[1]
	if target != nil {
		total = sourceIntervalIntersection(total, []store.SourceInterval{*target})
		end = target[1]
	}
	entries, err := sourceCachedEntries(cached, f)
	if err != nil {
		return nil, err
	}
	parents := []store.SourceInterval{}
	for _, factory := range filter.GetFilterFactories(f) {
		parentBounds, err := sourceFilterTotalInterval(factory)
		if err != nil {
			return nil, err
		}
		parentTotal := []store.SourceInterval{parentBounds}
		if target != nil {
			parentTotal = sourceIntervalIntersection(parentTotal, []store.SourceInterval{*target})
		}
		parentEntries, err := sourceCachedEntries(cached, factory)
		if err != nil {
			return nil, err
		}
		parents = append(parents, sourceIntervalDifference(parentTotal, sourceIntervalIntersectionMany(parentEntries))...)
	}
	if len(parents) > 0 {
		first := sourceIntervalBounds(parents)[0]
		adjusted := make([]store.SourceFragmentIntervals, len(entries))
		for i, entry := range entries {
			adjusted[i] = store.SourceFragmentIntervals{Fragment: entry.Fragment, Intervals: sourceIntervalDifference(entry.Intervals, []store.SourceInterval{{first, end}})}
		}
		entries = adjusted
	}
	return append(sourceIntervalDifference(total, sourceIntervalIntersectionMany(entries)), parents...), nil
}

// RequiredSourceIntervalUnion implements getRequiredIntervals. Unlike
// RequiredSourceIntervals (getRequiredIntervalsWithFilters), it retains holes.
func RequiredSourceIntervalUnion(filters []filter.Object, target store.SourceInterval, cached []store.SourceIntervalEntry) ([]store.SourceInterval, error) {
	missing := []store.SourceInterval{}
	for _, f := range filters {
		spans, err := sourceMissingIntervals(f, &target, cached)
		if err != nil {
			return nil, err
		}
		missing = append(missing, spans...)
	}
	return sourceIntervalUnion(missing), nil
}

// CachedSourceBlock returns nil for the source's undefined cached block. Empty
// filters yield positive infinity, preserving Math.min's source identity value.
func CachedSourceBlock(filters []filter.Object, cached []store.SourceIntervalEntry) (*float64, error) {
	result := math.Inf(1)
	for _, f := range filters {
		missing, err := sourceMissingIntervals(f, nil, cached)
		if err != nil {
			return nil, err
		}
		block := filter.FilterToBlock(f)
		if len(missing) > 0 {
			first := sourceIntervalBounds(missing)[0]
			if first == 0 {
				return nil, nil
			}
			block = first - 1
		}
		result = math.Min(result, block)
	}
	return &result, nil
}

type SourceIntervalReader interface {
	GetIntervals(context.Context, []filter.Object) (*store.SourceIntervalMap, error)
}
type SourceChildReader interface {
	GetChildAddresses(context.Context, filter.Object) (*store.SourceAddressMap, error)
}

// GetSourceCachedIntervals avoids even reading stale persisted data when the
// chain disables RPC caching; keys retain their original object identity.
func GetSourceCachedIntervals(ctx context.Context, cacheRPC bool, filters []filter.Object, reader SourceIntervalReader) ([]store.SourceIntervalEntry, error) {
	if !cacheRPC {
		return EmptySourceCachedIntervals(filters)
	}
	if reader == nil {
		return nil, fmt.Errorf("source interval reader missing")
	}
	result, err := reader.GetIntervals(ctx, filters)
	if err != nil {
		return nil, err
	}
	return result.Entries(), nil
}

// GetSourceChildAddresses preserves Map.set ordering and replacement semantics,
// including repeated factory reads. It does not merge stale duplicate entries.
func GetSourceChildAddresses(ctx context.Context, cacheRPC bool, filters []filter.Object, reader SourceChildReader) (*SourceHistoricalChildren, error) {
	entries := []SourceFactoryChildren{}
	indices := map[string]int{}
	for _, f := range filters {
		for _, factory := range filter.GetFilterFactories(f) {
			id, ok := factory["id"].(string)
			if !ok {
				return nil, fmt.Errorf("source factory id must be a string")
			}
			entry := SourceFactoryChildren{ID: id, Entries: []filter.ChildAddress{}}
			if cacheRPC {
				if reader == nil {
					return nil, fmt.Errorf("source child reader missing")
				}
				children, err := reader.GetChildAddresses(ctx, factory)
				if err != nil {
					return nil, err
				}
				for _, child := range children.Entries {
					entry.Entries = append(entry.Entries, filter.ChildAddress{Address: child.Address, Block: child.BlockNumber})
				}
			}
			if index, exists := indices[id]; exists {
				entries[index] = entry
			} else {
				indices[id] = len(entries)
				entries = append(entries, entry)
			}
		}
	}
	return NewSourceHistoricalChildren(entries), nil
}
