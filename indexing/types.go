package indexing

import (
	"context"
	"reflect"
	"sync"
	"time"

	"ponder.local/ponder-go/filter"
)

// Handler is synchronous with respect to service dispatch. Retained event
// property views are live bindings, just like the source's reused proxies.
type Handler func(context.Context, *HandlerArgs) error
type HandlerArgs struct {
	Event   EventView
	Context *Context
}
type Context struct {
	Chain     Object
	Contracts Object
	Client    *Client
	DB        *Database
}
type Build struct {
	FilterSession     *filter.Session
	Chains            []Object
	Contracts         []Object
	EventCallbacks    [][]Object
	SetupCallbacks    [][]Object
	IndexingFunctions []Object
}

// EventView retains its underlying object's identity. Historical views contain
// PropertyView values for intercepted components. Get("transaction") followed
// by PropertyView.Get("gas") replaces JS event.transaction.gas; an ordinary Go
// map read is NOT an equivalent intercepted read. Realtime fields remain raw.
type EventView Object

func (v EventView) Get(key string) any {
	if x, ok := v[key]; ok {
		return x
	}
	return filter.Undefined
}
func (v EventView) Set(key string, value any) { v[key] = value }
func (v EventView) Delete(key string)         { delete(v, key) }
func (v EventView) Has(key string) bool       { _, ok := v[key]; return ok }
func (v EventView) Property(component string) (*PropertyView, bool) {
	p, ok := v[component].(*PropertyView)
	return p, ok
}

// ErrorState is shared by the service, database and property views. The latest
// caught retryable error takes precedence over a callback's returned error.
type ErrorState struct {
	mu  sync.Mutex
	err error
}

func (s *ErrorState) GetRetryableError() error  { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *ErrorState) SetRetryableError(e error) { s.mu.Lock(); defer s.mu.Unlock(); s.err = e }
func (s *ErrorState) ClearRetryableError()      { s.mu.Lock(); defer s.mu.Unlock(); s.err = nil }

type IndexingFunctionError struct{ Message string }

func (e *IndexingFunctionError) Error() string     { return e.Message }
func (e *IndexingFunctionError) ErrorName() string { return "IndexingFunctionError" }

type ShutdownError struct{}

func (e *ShutdownError) Error() string     { return "" }
func (e *ShutdownError) ErrorName() string { return "ShutdownError" }

type DurationObservation struct {
	Event   string
	Seconds float64
}

// Metrics preserves source observation order and labels; durations use the real
// monotonic clock unless an explicit clock is supplied for deterministic tests.
type Metrics struct {
	Durations []DurationObservation
	Completed map[string]uint64
	HasError  bool
}
type ErrorObservation struct {
	Event Object
	Err   error
}
type Options struct {
	Build               Build
	Database            *Database
	Clients             *ClientSet
	Errors              RetryableErrorHandler
	ColumnAccessPattern ColumnAccessPattern
	EventCount          map[string]uint64
	Metrics             *Metrics
	Now                 func() time.Time
	Yield               func(context.Context) error
	IsKilled            func() bool
	OnError             func(ErrorObservation)
	OnResolved          func(map[string]int)
	// FieldOrder carries JS insertion order for decoded args/results into the real cache profiler.
	FieldOrder func(Object) map[string][]string
}

func NewEventCount(names []string) map[string]uint64 {
	m := map[string]uint64{}
	for _, n := range names {
		m[n] = 0
	}
	return m
}

// SameIdentity compares the underlying event object, not its contents.
func (v EventView) SameIdentity(other EventView) bool {
	return reflect.ValueOf(v).Pointer() == reflect.ValueOf(other).Pointer()
}

// BaseError is the explicit counterpart of source BaseError inheritance. A
// display ErrorName alone never opts an ordinary user error into this domain.
// User-defined known errors may embed BaseError, just as JS extends BaseError.
type BaseError struct{ Message string }

func (e *BaseError) Error() string                  { return e.Message }
func (e *BaseError) ErrorName() string              { return "BaseError" }
func (e *BaseError) sourceBaseError()               {}
func (e *IndexingFunctionError) sourceBaseError()   {}
func (e *ShutdownError) sourceBaseError()           {}
func (e *InvalidEventAccessError) sourceBaseError() {}
