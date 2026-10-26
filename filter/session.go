package filter

import (
	"reflect"
	"sync"
)

// Session serializes learned filter updates with whole-graph snapshots. The
// canonical maps remain the callbacks' maps, while asynchronous SQL/RPC work
// reads snapshots. Its lock must never cover SQL, RPC, handlers or iterator I/O.
// All other filter configuration is immutable for the session lifetime.
type Session struct{ mu sync.RWMutex }

// Update is for short in-memory mutations only. A nil session preserves the
// existing exclusive-caller contract for standalone APIs.
func (s *Session) Update(update func()) {
	if s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	update()
}

// Snapshot copies all map keys and nested arrays/maps in one critical section.
// Shared factory/filter aliases are preserved within the returned graph.
func (s *Session) Snapshot(objects []Object) []Object {
	if s == nil {
		return objects
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return snapshotObjects(objects)
}

// Callbacks snapshots filters, not callback/chain/function identity. Callers
// that decode with the temporary wrappers must restore the canonical callback
// references in emitted events; acquisition never emits these wrappers.
func (s *Session) Callbacks(callbacks []Object) []Object {
	if s == nil {
		return callbacks
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	filters := make([]Object, len(callbacks))
	for i, callback := range callbacks {
		filters[i], _ = callback["filter"].(Object)
	}
	copied := snapshotObjects(filters)
	result := make([]Object, len(callbacks))
	for i, callback := range callbacks {
		result[i] = Object{}
		for key, value := range callback {
			result[i][key] = value
		}
		result[i]["filter"] = copied[i]
	}
	return result
}

type snapshotArrayKey struct {
	pointer          uintptr
	length, capacity int
	kind             reflect.Type
}

func snapshotObjects(objects []Object) []Object {
	maps := map[uintptr]Object{}
	arrays := map[snapshotArrayKey]any{}
	var copyValue func(any) any
	copyValue = func(value any) any {
		if object, ok := value.(Object); ok {
			if object == nil {
				return Object(nil)
			}
			id := reflect.ValueOf(object).Pointer()
			if result, ok := maps[id]; ok {
				return result
			}
			result := Object{}
			maps[id] = result
			for key, value := range object {
				result[key] = copyValue(value)
			}
			return result
		}
		rv := reflect.ValueOf(value)
		if rv.IsValid() && rv.Kind() == reflect.Slice && !rv.IsNil() {
			key := snapshotArrayKey{rv.Pointer(), rv.Len(), rv.Cap(), rv.Type()}
			if rv.Len() > 0 {
				if result, ok := arrays[key]; ok {
					return result
				}
			}
			result := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
			if rv.Len() > 0 {
				arrays[key] = result.Interface()
			}
			for i := 0; i < rv.Len(); i++ {
				copied := copyValue(rv.Index(i).Interface())
				if copied != nil {
					result.Index(i).Set(reflect.ValueOf(copied))
				}
			}
			return result.Interface()
		}
		return value
	}
	result := make([]Object, len(objects))
	for i, object := range objects {
		result[i] = copyValue(object).(Object)
	}
	return result
}
