package runtime

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

func sourceIntervalUnion(input []store.SourceInterval) []store.SourceInterval {
	result := []store.SourceInterval{}
	if len(input) == 0 {
		return result
	}
	spans := append([]store.SourceInterval{}, input...)
	sort.SliceStable(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	current := spans[0]
	for _, next := range spans[1:] {
		if current[1] >= next[0]-1 {
			current[1] = math.Max(current[1], next[1])
		} else {
			result = append(result, current)
			current = next
		}
	}
	return append(result, current)
}
func sourceIntervalIntersection(a, b []store.SourceInterval) []store.SourceInterval {
	result := []store.SourceInterval{}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		from, to := math.Max(a[i][0], b[j][0]), math.Min(a[i][1], b[j][1])
		if from <= to {
			result = append(result, store.SourceInterval{from, to})
		}
		if a[i][1] < b[j][1] {
			i++
		} else {
			j++
		}
	}
	return sourceIntervalUnion(result)
}
func sourceIntervalIntersectionMany(entries []store.SourceFragmentIntervals) []store.SourceInterval {
	if len(entries) == 0 {
		return []store.SourceInterval{}
	}
	result := entries[0].Intervals
	for _, entry := range entries[1:] {
		result = sourceIntervalIntersection(result, entry.Intervals)
	}
	return result
}
func sourceIntervalDifference(initial, remove []store.SourceInterval) []store.SourceInterval {
	spans := append([]store.SourceInterval{}, initial...)
	result := []store.SourceInterval{}
	i, j := 0, 0
	for i < len(spans) && j < len(remove) {
		x, y := &spans[i], remove[j]
		if x[1] < y[0] {
			result = append(result, *x)
			i++
		} else if y[1] < x[0] {
			j++
		} else {
			if x[0] < y[0] {
				result = append(result, store.SourceInterval{x[0], y[0] - 1})
			}
			if x[1] > y[1] {
				x[0] = y[1] + 1
				j++
			} else {
				i++
			}
		}
	}
	return append(result, spans[i:]...)
}
func sourceIntervalBounds(spans []store.SourceInterval) store.SourceInterval {
	result := spans[0]
	for _, span := range spans[1:] {
		result[0] = math.Min(result[0], span[0])
		result[1] = math.Max(result[1], span[1])
	}
	return result
}
func sourceFilterTotalInterval(f filter.Object) (store.SourceInterval, error) {
	result := store.SourceInterval{0, math.Inf(1)}
	for index, key := range []string{"fromBlock", "toBlock"} {
		value := sourceHistoricalProperty(f, key)
		if value == nil || filter.IsUndefined(value) {
			continue
		}
		switch v := value.(type) {
		case float64:
			result[index] = v
		case int:
			result[index] = float64(v)
		case int64:
			result[index] = float64(v)
		case uint64:
			result[index] = float64(v)
		case json.Number:
			number, err := v.Float64()
			if err != nil {
				return result, err
			}
			result[index] = number
		default:
			return result, fmt.Errorf("source filter block must be numeric: %T", value)
		}
	}
	return result, nil
}

// EmptySourceCachedIntervals retains actual filter/factory map keys and source
// fragment order, as used before memory acquisition without a SQL cache.
func EmptySourceCachedIntervals(filters []filter.Object) ([]store.SourceIntervalEntry, error) {
	result := []store.SourceIntervalEntry{}
	indices := map[uintptr]int{}
	set := func(key filter.Object, values []store.SourceFragmentIntervals) {
		identity := reflect.ValueOf(key).Pointer()
		if index, exists := indices[identity]; exists {
			result[index].Values = values
		} else {
			indices[identity] = len(result)
			result = append(result, store.SourceIntervalEntry{Key: key, Values: values})
		}
	}
	for _, f := range filters {
		fragments, err := filter.GetFragments(f)
		if err != nil {
			return nil, err
		}
		entries := []store.SourceFragmentIntervals{}
		for _, fragment := range fragments {
			entries = append(entries, store.SourceFragmentIntervals{Fragment: fragment["fragment"].(filter.Object), Intervals: []store.SourceInterval{}})
		}
		set(f, entries)
		for _, factory := range filter.GetFilterFactories(f) {
			entries := []store.SourceFragmentIntervals{}
			for _, fragment := range filter.FactoryFragments(factory) {
				entries = append(entries, store.SourceFragmentIntervals{Fragment: fragment, Intervals: []store.SourceInterval{}})
			}
			set(factory, entries)
		}
	}
	return result, nil
}

// RequiredSourceIntervals recovers minimal filters from missing source cache
// fragments. A missing factory range invalidates dependent cached data from its
// first missing block, matching the source's dependency boundary.
func RequiredSourceIntervals(filters []filter.Object, target store.SourceInterval, cached []store.SourceIntervalEntry) ([]store.SourceFilterInterval, []store.SourceFactoryInterval, error) {
	required := []store.SourceFilterInterval{}
	requiredFactories := []store.SourceFactoryInterval{}
	lookup := func(key filter.Object) ([]store.SourceFragmentIntervals, error) {
		identity := reflect.ValueOf(key).Pointer()
		for _, entry := range cached {
			if reflect.ValueOf(entry.Key).Pointer() == identity {
				return entry.Values, nil
			}
		}
		return nil, fmt.Errorf("source cached interval map key missing")
	}
	missingFragments := func(entries []store.SourceFragmentIntervals, total []store.SourceInterval) ([]filter.Object, []store.SourceInterval) {
		fragments := []filter.Object{}
		missing := []store.SourceInterval{}
		for _, entry := range entries {
			spans := sourceIntervalDifference(total, entry.Intervals)
			if len(spans) > 0 {
				fragments = append(fragments, entry.Fragment)
				missing = append(missing, spans...)
			}
		}
		return fragments, missing
	}
	for _, f := range filters {
		total, err := sourceFilterTotalInterval(f)
		if err != nil {
			return nil, nil, err
		}
		filterTotal := sourceIntervalIntersection([]store.SourceInterval{target}, []store.SourceInterval{total})
		entries, err := lookup(f)
		if err != nil {
			return nil, nil, err
		}
		factories := filter.GetFilterFactories(f)
		missingFactories := []store.SourceInterval{}
		for _, factory := range factories {
			bounds, err := sourceFilterTotalInterval(factory)
			if err != nil {
				return nil, nil, err
			}
			factoryTotal := sourceIntervalIntersection([]store.SourceInterval{target}, []store.SourceInterval{bounds})
			factoryEntries, err := lookup(factory)
			if err != nil {
				return nil, nil, err
			}
			missingFactories = append(missingFactories, sourceIntervalDifference(factoryTotal, sourceIntervalIntersectionMany(factoryEntries))...)
		}
		if len(missingFactories) > 0 {
			first := missingFactories[0][0]
			for _, span := range missingFactories[1:] {
				first = math.Min(first, span[0])
			}
			copyEntries := make([]store.SourceFragmentIntervals, len(entries))
			for i, entry := range entries {
				entry.Intervals = sourceIntervalDifference(entry.Intervals, []store.SourceInterval{{first, target[1]}})
				copyEntries[i] = entry
			}
			entries = copyEntries
		}
		fragments, missing := missingFragments(entries, filterTotal)
		if len(missing) > 0 {
			required = append(required, store.SourceFilterInterval{Filter: filter.RecoverFilter(f, fragments), Interval: sourceIntervalBounds(missing)})
		}
		for _, factory := range factories {
			bounds, err := sourceFilterTotalInterval(factory)
			if err != nil {
				return nil, nil, err
			}
			factoryTotal := sourceIntervalIntersection([]store.SourceInterval{target}, []store.SourceInterval{bounds})
			factoryEntries, err := lookup(factory)
			if err != nil {
				return nil, nil, err
			}
			fragments, missing := missingFragments(factoryEntries, factoryTotal)
			if len(missing) > 0 {
				requiredFactories = append(requiredFactories, store.SourceFactoryInterval{Factory: filter.RecoverFactory(factory, fragments), Interval: sourceIntervalBounds(missing)})
			}
		}
	}
	return required, requiredFactories, nil
}
