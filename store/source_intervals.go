package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/ysh0566/ponder-go/filter"
)

// SourceSyncStore is the explicit source-shaped SyncStore path on the exact
// owned versioned tables. ProtocolCache native completion/cache APIs stay separate.
// It implements source intervals, children, protocol data, event pages and RPC
// results; the runtime scheduler owns acquisition and finality.
type SourceSyncStore struct {
	cache              *ProtocolCache
	maxQueryParameters int
	bound              *sourceSyncTransaction
}

// SourceQueryParameterBudget returns the effective limit for batched sync writes.
// Zero selects Ponder's default. PostgreSQL's Bind message permits 65,535 parameters.
func SourceQueryParameterBudget(limit int) (int, error) {
	if limit == 0 {
		return 16000, nil
	}
	if limit < 4 {
		return 0, fmt.Errorf("source sync query parameter budget must be at least 4")
	}
	return min(limit, 65535), nil
}

func (c *ProtocolCache) SourceSyncStore(maxQueryParameters int) (*SourceSyncStore, error) {
	budget, err := SourceQueryParameterBudget(maxQueryParameters)
	if err != nil {
		return nil, err
	}
	return &SourceSyncStore{cache: c, maxQueryParameters: budget}, nil
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

// SourceCacheEntry is a filter or factory paired with its cached fragments.
type SourceCacheEntry interface {
	sourceCacheEntry()
}

type filterCacheEntry SourceIntervalEntry
type factoryCacheEntry SourceIntervalEntry

func (filterCacheEntry) sourceCacheEntry()  {}
func (factoryCacheEntry) sourceCacheEntry() {}

// FilterCacheEntry pairs a source filter with its fragments without copying its key.
func FilterCacheEntry(key filter.Object, values []SourceFragmentIntervals) filterCacheEntry {
	return filterCacheEntry{Key: key, Values: values}
}

// FactoryCacheEntry pairs a source factory with its fragments without copying its key.
func FactoryCacheEntry(key filter.Object, values []SourceFragmentIntervals) factoryCacheEntry {
	return factoryCacheEntry{Key: key, Values: values}
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

// NewSourceIntervalMap retains the first insertion position of each key.
// Use SourceCacheEntry as the type argument to combine filters and factories.
func NewSourceIntervalMap[E SourceCacheEntry](entries ...E) (*SourceIntervalMap, error) {
	result := new(SourceIntervalMap)
	for _, entry := range entries {
		if err := result.Set(entry); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Set stores fragments under the original object's identity. The zero map is ready for use.
func (m *SourceIntervalMap) Set(entry SourceCacheEntry) error {
	var pair SourceIntervalEntry
	switch e := entry.(type) {
	case filterCacheEntry:
		switch e.Key["type"] {
		case "block", "log", "transaction", "trace", "transfer":
		default:
			return fmt.Errorf("unknown source interval filter type %v", e.Key["type"])
		}
		if _, factory := e.Key["childAddressLocation"]; factory {
			return fmt.Errorf("expected a filter cache key, got a factory")
		}
		pair = SourceIntervalEntry(e)
	case factoryCacheEntry:
		// Source factory fragments can omit the original type and ID. They
		// retain the event selector and the child's location in the log.
		_, selector := e.Key["eventSelector"]
		_, location := e.Key["childAddressLocation"]
		_, id := e.Key["id"]
		kind := e.Key["type"]
		if kind != nil && kind != "log" || !(selector && location || id && kind == "log") {
			return fmt.Errorf("expected a log factory cache key")
		}
		pair = SourceIntervalEntry(e)
	default:
		return fmt.Errorf("expected a filter or factory cache entry")
	}
	if m.indices == nil {
		m.indices = make(map[uintptr]int)
	}
	id := reflect.ValueOf(pair.Key).Pointer()
	if i, ok := m.indices[id]; ok {
		m.entries[i].Values = pair.Values
	} else {
		m.indices[id] = len(m.entries)
		m.entries = append(m.entries, pair)
	}
	return nil
}

func (s *SourceSyncStore) InsertIntervals(ctx context.Context, chain uint64, intervals []SourceFilterInterval, factories []SourceFactoryInterval) error {
	if err := s.scopeActive(ctx); err != nil {
		return err
	}
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
		// An unbound source store commits each batch separately. Only an explicit
		// WithTransaction scope combines these writes with its surrounding work.
		if e := s.transaction(ctx, func(tx Transaction) error {
			return tx.Exec(ctx, "INSERT INTO "+s.cache.table("intervals")+" AS intervals(fragment_id,chain_id,blocks) VALUES"+strings.Join(values, ",")+" ON CONFLICT(fragment_id) DO UPDATE SET blocks=intervals.blocks+excluded.blocks", args...)
		}); e != nil {
			return e
		}
	}
	return nil
}
func (s *SourceSyncStore) GetIntervals(ctx context.Context, filters []filter.Object) (*SourceIntervalMap, error) {
	if err := s.scopeActive(ctx); err != nil {
		return nil, err
	}
	type part struct {
		value *SourceFragmentIntervals
		ids   []string
	}
	var parts []part
	result := new(SourceIntervalMap)
	for _, f := range filters {
		fragments, e := filter.GetFragments(f)
		if e != nil {
			return nil, e
		}
		values := make([]SourceFragmentIntervals, len(fragments))
		for i, fragment := range fragments {
			values[i].Fragment = fragment["fragment"].(filter.Object)
			ids := []string{}
			for _, id := range fragment["adjacentIds"].([]any) {
				ids = append(ids, fmt.Sprint(id))
			}
			parts = append(parts, part{&values[i], ids})
		}
		if err := result.Set(FilterCacheEntry(f, values)); err != nil {
			return nil, err
		}
		for _, factory := range filter.GetFilterFactories(f) {
			fragments := filter.FactoryFragments(factory)
			values := make([]SourceFragmentIntervals, len(fragments))
			for i, fragment := range fragments {
				values[i].Fragment = fragment
				parts = append(parts, part{&values[i], []string{filter.EncodeFragment(fragment)}})
			}
			if err := result.Set(FactoryCacheEntry(factory, values)); err != nil {
				return nil, err
			}
		}
	}
	// Pinned source's empty-query branch dereferences queries[0]. This is an
	// error occurrence, not an invented successful empty result.
	if len(parts) == 0 {
		return nil, fmt.Errorf("source interval query has no executable query")
	}
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
		if e := s.transaction(ctx, func(tx Transaction) error {
			rows, e := tx.Query(ctx, strings.Join(queries, " UNION ALL "), args...)
			if e != nil {
				return e
			}
			for _, row := range rows.Rows {
				i, e := strconv.Atoi(fmt.Sprint(row["fragment"]))
				if e != nil {
					return e
				}
				parts[i].value.Intervals = []SourceInterval{}
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
					parts[i].value.Intervals = append(parts[i].value.Intervals, span)
				}
			}
			return nil
		}); e != nil {
			return nil, e
		}
	}
	return result, nil
}
