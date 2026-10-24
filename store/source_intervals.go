package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"ponder.local/ponder-go/filter"
)

// SourceSyncStore is the explicit source-shaped SyncStore path on the exact
// owned versioned tables. ProtocolCache native completion/cache APIs stay separate.
// It implements source intervals, children, protocol data, event pages and RPC
// results; the runtime scheduler owns acquisition and finality.
type SourceSyncStore struct {
	cache              *ProtocolCache
	maxQueryParameters int
}

func (c *ProtocolCache) SourceSyncStore(maxQueryParameters int) (*SourceSyncStore, error) {
	if maxQueryParameters == 0 {
		maxQueryParameters = 16000
	}
	if maxQueryParameters < 4 {
		return nil, fmt.Errorf("source sync query parameter budget must be at least4")
	}
	return &SourceSyncStore{c, maxQueryParameters}, nil
}

type SourceInterval [2]float64
type SourceFilterInterval struct {
	Filter   filter.Object
	Interval SourceInterval
}
type SourceFactoryInterval struct {
	Factory  filter.Object
	Interval SourceInterval
}
type SourceFragmentIntervals struct {
	Fragment  filter.Object
	Intervals []SourceInterval
}
type SourceIntervalEntry struct {
	Key    filter.Object
	Values []SourceFragmentIntervals
}

// SourceIntervalMap retains the actual filter/factory map identities and Map
// insertion order. Key maps stay strongly reachable while pointer indices live.
type SourceIntervalMap struct {
	entries []SourceIntervalEntry
	indices map[uintptr]int
}

func (m *SourceIntervalMap) Len() int { return len(m.entries) }
func (m *SourceIntervalMap) Entries() []SourceIntervalEntry {
	return append([]SourceIntervalEntry{}, m.entries...)
}
func (m *SourceIntervalMap) Get(key filter.Object) ([]SourceFragmentIntervals, bool) {
	i, ok := m.indices[reflect.ValueOf(key).Pointer()]
	if !ok {
		return nil, false
	}
	return m.entries[i].Values, true
}
func (m *SourceIntervalMap) set(key filter.Object, values []SourceFragmentIntervals) {
	id := reflect.ValueOf(key).Pointer()
	if i, ok := m.indices[id]; ok {
		m.entries[i].Values = values
	} else {
		m.indices[id] = len(m.entries)
		m.entries = append(m.entries, SourceIntervalEntry{key, values})
	}
}
func (m *SourceIntervalMap) append(key filter.Object, value SourceFragmentIntervals) {
	id := reflect.ValueOf(key).Pointer()
	i := m.indices[id]
	m.entries[i].Values = append(m.entries[i].Values, value)
}

func (s *SourceSyncStore) InsertIntervals(ctx context.Context, chain uint64, intervals []SourceFilterInterval, factories []SourceFactoryInterval) error {
	if len(intervals) == 0 && len(factories) == 0 {
		return nil
	}
	order := []string{}
	grouped := map[string][]SourceInterval{}
	add := func(fragment filter.Object, span SourceInterval) {
		id := filter.EncodeFragment(fragment)
		if _, ok := grouped[id]; !ok {
			order = append(order, id)
		}
		grouped[id] = append(grouped[id], span)
	}
	for _, item := range intervals {
		fragments, e := filter.GetFragments(item.Filter)
		if e != nil {
			return e
		}
		for _, fragment := range fragments {
			add(fragment["fragment"].(filter.Object), item.Interval)
		}
	}
	for _, item := range factories {
		for _, fragment := range filter.FactoryFragments(item.Factory) {
			add(fragment, item.Interval)
		}
	}
	size := s.maxQueryParameters / 3
	for offset := 0; offset < len(order); offset += size {
		end := min(offset+size, len(order))
		values := []string{}
		args := []any{}
		for _, id := range order[offset:end] {
			ranges := []string{}
			for _, span := range grouped[id] {
				ranges = append(ranges, "numrange("+strconv.FormatFloat(span[0], 'g', -1, 64)+","+strconv.FormatFloat(span[1]+1, 'g', -1, 64)+",'[]')")
			}
			n := len(args)
			values = append(values, fmt.Sprintf("($%d,$%d,nummultirange(%s))", n+1, n+2, strings.Join(ranges, ",")))
			args = append(args, id, chain)
		}
		// The source commits each batch separately; do not wrap all batches in one
		// native transaction and hide earlier committed batches on a later failure.
		if e := s.cache.transaction(ctx, func(tx Transaction) error {
			return tx.Exec(ctx, "INSERT INTO "+s.cache.table("intervals")+" AS intervals(fragment_id,chain_id,blocks) VALUES"+strings.Join(values, ",")+" ON CONFLICT(fragment_id) DO UPDATE SET blocks=intervals.blocks+excluded.blocks", args...)
		}); e != nil {
			return e
		}
	}
	return nil
}
func (s *SourceSyncStore) GetIntervals(ctx context.Context, filters []filter.Object) (*SourceIntervalMap, error) {
	type part struct {
		key, fragment filter.Object
		ids           []string
		factory       bool
	}
	parts := []part{}
	for _, f := range filters {
		fragments, e := filter.GetFragments(f)
		if e != nil {
			return nil, e
		}
		for _, fragment := range fragments {
			ids := []string{}
			for _, id := range fragment["adjacentIds"].([]any) {
				ids = append(ids, fmt.Sprint(id))
			}
			parts = append(parts, part{f, fragment["fragment"].(filter.Object), ids, false})
		}
		for _, factory := range filter.GetFilterFactories(f) {
			for _, fragment := range filter.FactoryFragments(factory) {
				parts = append(parts, part{factory, fragment, []string{filter.EncodeFragment(fragment)}, true})
			}
		}
	}
	// Pinned source's empty-query branch dereferences queries[0]. This is an
	// error occurrence, not an invented successful empty result.
	if len(parts) == 0 {
		return nil, fmt.Errorf("source interval query has no executable query")
	}
	ranges := make([][]SourceInterval, len(parts))
	for offset := 0; offset < len(parts); offset += 200 {
		end := min(offset+200, len(parts))
		queries := []string{}
		args := []any{}
		for i := offset; i < end; i++ {
			slots := []string{}
			for _, id := range parts[i].ids {
				args = append(args, id)
				slots = append(slots, "$"+strconv.Itoa(len(args)))
			}
			queries = append(queries, fmt.Sprintf("SELECT range_agg(unnested.blocks)::text AS merged_blocks,'%d' AS fragment FROM (SELECT unnest(blocks) AS blocks FROM %s WHERE fragment_id IN (%s)) AS unnested", i, s.cache.table("intervals"), strings.Join(slots, ",")))
		}
		if e := s.cache.transaction(ctx, func(tx Transaction) error {
			rows, e := tx.Query(ctx, strings.Join(queries, " UNION ALL "), args...)
			if e != nil {
				return e
			}
			for _, row := range rows.Rows {
				i, e := strconv.Atoi(fmt.Sprint(row["fragment"]))
				if e != nil {
					return e
				}
				ranges[i] = []SourceInterval{}
				if row["merged_blocks"] == nil {
					continue
				}
				text := fmt.Sprint(row["merged_blocks"])
				if len(text) < 2 {
					return fmt.Errorf("invalid source interval multirange")
				}
				var spans []SourceInterval
				if e = json.Unmarshal([]byte("["+text[1:len(text)-1]+"]"), &spans); e != nil {
					return e
				}
				for _, span := range spans {
					span[1] -= 1
					ranges[i] = append(ranges[i], span)
				}
			}
			return nil
		}); e != nil {
			return nil, e
		}
	}
	result := &SourceIntervalMap{entries: []SourceIntervalEntry{}, indices: map[uintptr]int{}}
	index := 0
	for _, f := range filters {
		result.set(f, []SourceFragmentIntervals{})
		fragments, e := filter.GetFragments(f)
		if e != nil {
			return nil, e
		}
		for range fragments {
			p := parts[index]
			result.append(f, SourceFragmentIntervals{p.fragment, ranges[index]})
			index++
		}
		for _, factory := range filter.GetFilterFactories(f) {
			result.set(factory, []SourceFragmentIntervals{})
			for range filter.FactoryFragments(factory) {
				p := parts[index]
				result.append(factory, SourceFragmentIntervals{p.fragment, ranges[index]})
				index++
			}
		}
	}
	return result, nil
}
