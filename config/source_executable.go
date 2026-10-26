package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
)

// ExecutableSourceIndexingBuild is the single live projection of a source build.
// Unlike Value, it retains executable functions and shared object/array identity.
// The caller owns this graph for a session: do not change its configuration while
// extraction is running. Learned filter includes must use the shared filter.Session.
type ExecutableSourceIndexingBuild struct {
	sessionMu                          sync.Mutex
	sessionActive                      bool
	Chains, FinalizedBlocks, Contracts []filter.Object
	EventCallbacks, SetupCallbacks     [][]filter.Object
	IndexingFunctions                  []filter.Object
	RPCs                               []*rpc.Client
	Ordering                           any
}

// AcquireSession exclusively leases this cached graph to one live owner. Release
// must happen only after extraction, handlers and owner Close have all joined.
// It is idempotent; a stale release cannot revoke a later owner's lease.
func (build *ExecutableSourceIndexingBuild) AcquireSession() (*filter.Session, func(), error) {
	if build == nil {
		return nil, nil, fmt.Errorf("executable source build required")
	}
	build.sessionMu.Lock()
	defer build.sessionMu.Unlock()
	if build.sessionActive {
		return nil, nil, fmt.Errorf("executable source build already has a live session")
	}
	build.sessionActive = true
	var once sync.Once
	release := func() {
		once.Do(func() { build.sessionMu.Lock(); build.sessionActive = false; build.sessionMu.Unlock() })
	}
	return &filter.Session{}, release, nil
}

// Executable returns the same graph on every call, not a fresh JSON projection.
// Function validation is intentionally the consuming indexing service's job;
// config does not import indexing or silently convert legacy runtime handlers.
func (build *SourceIndexingBuild) Executable() *ExecutableSourceIndexingBuild {
	if build == nil {
		return nil
	}
	build.executableOnce.Do(func() {
		projection := &sourceExecutableProjection{objects: map[*sourceObject]filter.Object{}, maps: map[uintptr]filter.Object{}, arrays: map[sourceExecutableArrayKey]any{}}
		out := &ExecutableSourceIndexingBuild{Ordering: build.ordering}
		objects := func(values []*sourceObject) []filter.Object {
			result := make([]filter.Object, len(values))
			for i, value := range values {
				result[i] = projection.value(value).(filter.Object)
			}
			return result
		}
		out.Chains, out.FinalizedBlocks, out.Contracts = objects(build.chains), objects(build.finalized), objects(build.contracts)
		out.EventCallbacks, out.SetupCallbacks = make([][]filter.Object, len(build.callbacks)), make([][]filter.Object, len(build.setup))
		for i := range build.callbacks {
			out.EventCallbacks[i] = objects(build.callbacks[i])
		}
		for i := range build.setup {
			out.SetupCallbacks[i] = objects(build.setup[i])
		}
		for _, fn := range build.functions {
			out.IndexingFunctions = append(out.IndexingFunctions, filter.Object{"name": fn.Name, "fn": fn.Fn})
		}
		if build.base != nil {
			for _, index := range build.chainIndexes {
				out.RPCs = append(out.RPCs, build.base.value.RPCs[index])
			}
		}
		build.executable = out
	})
	return build.executable
}

type sourceExecutableArrayKey struct {
	pointer          uintptr
	length, capacity int
	kind             reflect.Type
}
type sourceExecutableProjection struct {
	objects map[*sourceObject]filter.Object
	maps    map[uintptr]filter.Object
	arrays  map[sourceExecutableArrayKey]any
}

func (p *sourceExecutableProjection) value(value any) any {
	switch v := value.(type) {
	case *sourceObject:
		if out, ok := p.objects[v]; ok {
			return out
		}
		out := filter.Object{}
		p.objects[v] = out
		for _, key := range v.keys {
			out[key] = p.value(v.values[key])
		}
		return out
	case map[string]any:
		id := reflect.ValueOf(v).Pointer()
		if out, ok := p.maps[id]; ok {
			return out
		}
		out := filter.Object{}
		p.maps[id] = out
		for key, value := range v {
			out[key] = p.value(value)
		}
		return out
	case json.Number:
		n, _ := v.Float64()
		return n
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && rv.Kind() == reflect.Slice {
		if rv.IsNil() {
			return value
		}
		key := sourceExecutableArrayKey{rv.Pointer(), rv.Len(), rv.Cap(), rv.Type()}
		if rv.Len() > 0 {
			if out, ok := p.arrays[key]; ok {
				return out
			}
		}
		out := make([]any, rv.Len())
		if rv.Len() > 0 {
			p.arrays[key] = out
		}
		for i := range out {
			out[i] = p.value(rv.Index(i).Interface())
		}
		return out
	}
	// Functions, undefined, clients and opaque native values stay executable.
	return value
}
