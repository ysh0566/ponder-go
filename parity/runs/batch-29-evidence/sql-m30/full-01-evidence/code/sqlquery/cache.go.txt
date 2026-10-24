package sqlquery

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"sync"
)

// RelationSet preserves insertion order and the identity of a cached source Set.
// Methods allow shared sets to be inspected and changed by concurrent callers.
type RelationSet struct {
	mu    sync.RWMutex
	names []string
}

func (s *RelationSet) Values() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{}, s.names...)
}

func (s *RelationSet) Has(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return contains(s.names, name)
}

func (s *RelationSet) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.names)
}

func (s *RelationSet) Add(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !contains(s.names, name) {
		s.names = append(s.names, name)
	}
}

func (s *RelationSet) Delete(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, v := range s.names {
		if v == name {
			s.names = append(s.names[:i], s.names[i+1:]...)
			return true
		}
	}
	return false
}

func (s *RelationSet) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.names = nil
}

type cacheEntry[T any] struct {
	key   [32]byte
	value T
}

// The validator's mutex protects both cache maps and recency lists.
type queryCache[T any] struct {
	entries map[[32]byte]*list.Element
	order   *list.List
}

func newQueryCache[T any]() queryCache[T] {
	return queryCache[T]{entries: make(map[[32]byte]*list.Element), order: list.New()}
}

func (c *queryCache[T]) get(key [32]byte) (T, bool) {
	if e := c.entries[key]; e != nil {
		c.order.MoveToBack(e)
		return e.Value.(cacheEntry[T]).value, true
	}
	var zero T
	return zero, false
}

func (c *queryCache[T]) put(key [32]byte, value T) {
	if e := c.entries[key]; e != nil {
		e.Value = cacheEntry[T]{key, value}
		return
	}
	c.entries[key] = c.order.PushBack(cacheEntry[T]{key, value})
}

func (c *queryCache[T]) evict(limit int) {
	if c.order.Len() > limit {
		e := c.order.Front()
		delete(c.entries, e.Value.(cacheEntry[T]).key)
		c.order.Remove(e)
	}
}

// Validator owns the two independent caches used by the source SQL helpers.
// A new instance is useful when separate applications share the same process.
type Validator struct {
	mu        sync.Mutex
	allow     queryCache[bool]
	relations queryCache[*RelationSet]
	limit     int
}

func NewValidator() *Validator { return newValidator(1_000_000) }

func newValidator(limit int) *Validator {
	return &Validator{allow: newQueryCache[bool](), relations: newQueryCache[*RelationSet](), limit: limit}
}

var defaultValidator = NewValidator()

// Validate rejects syntax and AST nodes forbidden by the pinned source rules.
// As in the source, the first rejection carries its specific diagnostic and a
// cached rejection returns "Invalid query". Both use the complete SHA-256 key.
func (v *Validator) Validate(sql string) error {
	key := sha256.Sum256([]byte(sql))
	v.mu.Lock()
	allowed, hit := v.allow.get(key)
	v.mu.Unlock()
	if hit {
		if allowed {
			return nil
		}
		return fmt.Errorf("Invalid query")
	}
	tree, err := Parse(sql)
	if err == nil {
		err = walk(tree, validateAllowable, nil)
	}
	v.mu.Lock()
	v.allow.put(key, err == nil)
	// The source prunes only the successful-validation insertion path.
	if err == nil {
		v.allow.evict(v.limit)
	}
	v.mu.Unlock()
	return err
}

// Relations returns the cached ordered set itself, including caller mutations.
func (v *Validator) Relations(sql string) (*RelationSet, error) {
	if sourceStringLength(sql) > 5_000 {
		return nil, fmt.Errorf("Invalid query")
	}
	key := sha256.Sum256([]byte(sql))
	v.mu.Lock()
	set, hit := v.relations.get(key)
	v.mu.Unlock()
	if hit {
		return set, nil
	}
	tree, err := Parse(sql)
	if err != nil {
		return nil, err
	}
	set = &RelationSet{}
	err = walk(tree, findRelations, func(kind string, node map[string]any) {
		if kind == "RangeVar" {
			if name, ok := node["relname"].(string); ok && name != "" {
				set.Add(name)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.relations.put(key, set)
	v.relations.evict(v.limit)
	v.mu.Unlock()
	return set, nil
}
