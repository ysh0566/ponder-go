package store

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"ponder.local/ponder-go/schema"
)

type countedPattern struct {
	pattern ProfilePattern
	count   uint64
}

// CacheProfile shares access predictions across sessions, never business rows
// or pending writes. Each event has its own transaction-local row cache.
type CacheProfile struct {
	mu       sync.Mutex
	patterns map[string]map[string]map[string]countedPattern
	counts   map[string]uint64
}

func NewCacheProfile() *CacheProfile {
	return &CacheProfile{patterns: map[string]map[string]map[string]countedPattern{}, counts: map[string]uint64{}}
}
func (cache *IndexingCache) SetProfileEvent(event ProfileEvent) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if e := cache.db.active(); e != nil {
		return e
	}
	if event.Callback == "" {
		return fmt.Errorf("profile callback name is required")
	}
	event.Data = cloneRow(event.Data)
	order := map[string][]string{}
	for key, value := range event.FieldOrder {
		order[key] = append([]string{}, value...)
	}
	event.FieldOrder = order
	cache.profileEvent = &event
	cache.profileState.mu.Lock()
	defer cache.profileState.mu.Unlock()
	if cache.profileState.counts == nil {
		cache.profileState.counts = map[string]uint64{}
	}
	if cache.profileState.patterns == nil {
		cache.profileState.patterns = map[string]map[string]map[string]countedPattern{}
	}
	cache.profileState.counts[event.Callback] = event.Count
	return nil
}
func (cache *IndexingCache) recordProfile(table schema.Table, key Row) error {
	event := cache.profileEvent
	if event == nil || event.Count%10 != 1 {
		return nil
	}
	state := cache.profileState
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.patterns[event.Callback] == nil {
		state.patterns[event.Callback] = map[string]map[string]countedPattern{}
	}
	if state.patterns[event.Callback][table.Name] == nil {
		state.patterns[event.Callback][table.Name] = map[string]countedPattern{}
	}
	patterns := state.patterns[event.Callback][table.Name]
	hints := []ProfilePattern{}
	for _, entry := range patterns {
		hints = append(hints, entry.pattern)
	}
	pattern, e := RecordProfilePattern(*event, table, key, hints)
	if e != nil || pattern == nil {
		return e
	}
	patternKey, e := ProfilePatternKey(pattern)
	if e != nil {
		return e
	}
	entry := patterns[patternKey]
	entry.pattern = pattern
	entry.count++
	patterns[patternKey] = entry
	return nil
}
func (cache *IndexingCache) Has(name string, key Row) (bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if e := cache.db.active(); e != nil {
		return false, e
	}
	table, c, e := cache.db.table(name)
	if e != nil {
		return false, e
	}
	if c.complete {
		return true, nil
	}
	ck, e := cacheKey(table, key)
	if e != nil {
		return false, e
	}
	// Upstream uses cache.has() ?? buffer.has(), whose boolean left side is
	// never nullish. Preserve that observable behavior for incomplete caches.
	_, exists := c.rows[ck]
	return exists, nil
}
func cacheValueBytes(value any) int64 {
	if value == nil || isUndefined(value) {
		return 8
	}
	if _, ok := value.(*big.Int); ok {
		return 48
	}
	if _, ok := value.(*time.Time); ok {
		return 24
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Bool:
		return 4
	case reflect.String:
		return int64(2 * len(utf16.Encode([]rune(v.String()))))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return 8
	case reflect.Slice, reflect.Array:
		size := int64(24)
		for i := 0; i < v.Len(); i++ {
			size += cacheValueBytes(v.Index(i).Interface()) + 8
		}
		return size
	case reflect.Map:
		size := int64(24)
		for _, key := range v.MapKeys() {
			size += cacheValueBytes(v.MapIndex(key).Interface()) + 8
		}
		return size
	}
	return 24
}

func (cache *IndexingCache) Prefetch(ctx context.Context, events []ProfileEvent) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if e := cache.db.active(); e != nil {
		return e
	}
	tables := append([]schema.Table{}, cache.db.writer.s.Schema.Tables...)
	total := int64(0)
	for _, c := range cache.tables {
		total += c.bytes
	}
	if total > cache.maxBytes {
		sort.SliceStable(tables, func(i, j int) bool {
			return cache.tables[tables[i].Name].diskReads < cache.tables[tables[j].Name].diskReads
		})
		for _, table := range tables {
			c := cache.tables[table.Name]
			if !c.complete {
				continue
			}
			total -= c.bytes
			c.bytes = 0
			c.rows = map[string]Row{}
			c.complete = false
			if total < cache.maxBytes {
				break
			}
		}
	}
	allComplete := true
	for _, c := range cache.tables {
		allComplete = allComplete && c.complete
	}
	if allComplete {
		return nil
	}
	predictions := map[string]map[string]Row{}
	for _, table := range tables {
		predictions[table.Name] = map[string]Row{}
	}
	state := cache.profileState
	state.mu.Lock()
	for _, event := range events {
		if cache.db.writer.s.Ordering == schema.Isolated && event.ChainID != cache.db.writer.event.Checkpoint.ChainID {
			continue
		}
		count := state.counts[event.Callback]
		if count == 0 {
			continue
		}
		for _, table := range tables {
			if cache.tables[table.Name].complete {
				continue
			}
			for _, entry := range state.patterns[event.Callback][table.Name] {
				if float64(entry.count)*10/float64(count) <= 0.25 {
					continue
				}
				row, e := RecoverProfilePattern(entry.pattern, event)
				if e != nil {
					state.mu.Unlock()
					return e
				}
				key, e := cacheKey(table, row)
				if e != nil {
					state.mu.Unlock()
					return e
				}
				predictions[table.Name][key] = row
			}
		}
	}
	state.mu.Unlock()
	for _, table := range tables {
		c := cache.tables[table.Name]
		if c.complete {
			continue
		}
		for key := range c.rows {
			if c.spillover[key] || predictions[table.Name][key] != nil {
				delete(predictions[table.Name], key)
			} else {
				delete(c.rows, key)
			}
		}
	}
	for _, c := range cache.tables {
		c.spillover = map[string]bool{}
		c.prefetched = map[string]bool{}
	}
	loaded := map[string]map[string]Row{}
	for _, table := range tables {
		prediction := predictions[table.Name]
		if len(prediction) == 0 {
			continue
		}
		loaded[table.Name] = map[string]Row{}
		type item struct {
			key string
			row Row
		}
		items := []item{}
		for key, row := range prediction {
			items = append(items, item{key, row})
		}
		for len(items) > 0 {
			limit := 250
			if per := 60000 / len(table.Keys()); per < limit {
				limit = per
			}
			if len(items) < limit {
				limit = len(items)
			}
			args := []any{}
			conditions := []string{}
			for _, item := range items[:limit] {
				if e := cache.db.checkAccess(table, "find", item.row); e != nil {
					return e
				}
				physical := Row{}
				for _, column := range table.Columns {
					if value, present := item.row[property(column)]; present {
						normalized, e := NormalizeColumn(column, value, false)
						if e != nil {
							return e
						}
						physical[column.Name] = normalized
					}
				}
				where, values, e := keyWhere(table, physical, len(args)+1)
				if e != nil {
					return e
				}
				args = append(args, values...)
				conditions = append(conditions, "("+where+")")
				loaded[table.Name][item.key] = nil
			}
			result, e := cache.db.writer.tx.Query(ctx, "SELECT row_to_json(t)::text AS row FROM "+cache.db.writer.s.table(table.Name)+" AS t WHERE "+strings.Join(conditions, " OR "), args...)
			if e != nil {
				return e
			}
			for _, raw := range result.Rows {
				physical, e := decodeSnapshot(table, fmt.Sprint(raw["row"]))
				if e != nil {
					return e
				}
				row := Row{}
				for _, column := range table.Columns {
					row[property(column)] = physical[column.Name]
				}
				key, e := cacheKey(table, row)
				if e != nil {
					return e
				}
				loaded[table.Name][key] = row
			}
			items = items[limit:]
		}
	}
	for name, rows := range loaded {
		for key, row := range rows {
			cache.tables[name].rows[key] = row
			cache.tables[name].prefetched[key] = true
		}
	}
	return nil
}
