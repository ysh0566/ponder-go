// Package async implements typed futures and FIFO task queues on a cooperative
// event loop. Await drives the loop until its result is ready. Run can instead
// drive it continuously. Worker callbacks return futures without blocking.
package async

import (
	"context"
	"errors"
	"sync"
)

// Loop runs one callback at a time. Promise reactions run before the next turn.
// Several goroutines may await results; only one drives the loop at a time.
type Loop struct {
	mu               sync.Mutex
	reactions, turns []func()
	timers           []*Timer
	wake             chan struct{}
	driver           chan struct{}
}

func NewLoop() *Loop {
	return &Loop{wake: make(chan struct{}, 1), driver: make(chan struct{}, 1)}
}

type workKind uint8

const (
	reaction workKind = iota
	turn
)

func (l *Loop) post(kind workKind, f func()) {
	l.mu.Lock()
	if kind == reaction {
		l.reactions = append(l.reactions, f)
	} else {
		l.enqueueTimers()
		l.turns = append(l.turns, f)
	}
	l.mu.Unlock()
	l.signal()
}

func (l *Loop) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *Loop) take() func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enqueueTimers()
	queue := &l.reactions
	if len(*queue) == 0 {
		queue = &l.turns
	}
	if len(*queue) == 0 {
		return nil
	}
	fn := (*queue)[0]
	(*queue)[0] = nil
	*queue = (*queue)[1:]
	return fn
}

// Run drives callbacks until ctx ends. Calling Await is sufficient when the
// caller only needs a particular result.
func (l *Loop) Run(ctx context.Context) error { return l.until(ctx, nil) }

func (l *Loop) until(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case l.driver <- struct{}{}:
	}
	defer func() { <-l.driver }()
	for {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if fn := l.take(); fn != nil {
			fn()
			continue
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-l.wake:
		}
	}
}

// Future is a single result which can be observed by multiple callers.
type Future[T any] struct {
	loop      *Loop
	done      chan struct{}
	mu        sync.Mutex
	value     T
	err       error
	settled   bool
	observers []observer[T]
}

type observer[T any] struct {
	loop     *Loop
	callback func(T, error)
}

type Resolvers[T any] struct {
	Promise *Future[T]
	Resolve func(T)
	Reject  func(error)
}

// ErrRejected represents rejection without an error value.
var ErrRejected = errors.New("promise rejected")

func NewPromise[T any](loop *Loop) Resolvers[T] {
	f := &Future[T]{loop: loop, done: make(chan struct{})}
	return Resolvers[T]{
		Promise: f,
		Resolve: func(value T) { f.complete(value, nil) },
		Reject: func(err error) {
			if err == nil {
				err = ErrRejected
			}
			var zero T
			f.complete(zero, err)
		},
	}
}

func Resolved[T any](loop *Loop, value T) *Future[T] {
	r := NewPromise[T](loop)
	r.Resolve(value)
	return r.Promise
}

func Rejected[T any](loop *Loop, err error) *Future[T] {
	r := NewPromise[T](loop)
	r.Reject(err)
	return r.Promise
}

func (f *Future[T]) complete(value T, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settled {
		return
	}
	f.value, f.err, f.settled = value, err, true
	for _, o := range f.observers {
		o.loop.post(reaction, func() { o.callback(value, err) })
	}
	f.observers = nil
	close(f.done)
}

func (f *Future[T]) observe(loop *Loop, cb func(T, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.settled {
		f.observers = append(f.observers, observer[T]{loop, cb})
		return
	}
	loop.post(reaction, func() { cb(f.value, f.err) })
}

// OnSettled schedules a callback, including when the result is already known.
func (f *Future[T]) OnSettled(cb func(T, error)) { f.observe(f.loop, cb) }

func (f *Future[T]) Await(ctx context.Context) (T, error) {
	if err := f.loop.until(ctx, f.done); err != nil {
		var zero T
		return zero, err
	}
	// Await resumes after reactions that were already registered on settlement.
	// Do not attach a waiter to an unsettled future that could outlive ctx.
	resume := make(chan struct{})
	f.loop.post(reaction, func() { close(resume) })
	if err := f.loop.until(ctx, resume); err != nil {
		var zero T
		return zero, err
	}
	return f.value, f.err
}

// AllSettledWithThrow visits all results, preserving input order on success.
// It reports the first rejection observed on the loop after all have settled.
func AllSettledWithThrow[T any](loop *Loop, inputs ...*Future[T]) *Future[[]T] {
	out := NewPromise[[]T](loop)
	values := make([]T, len(inputs))
	remaining := len(inputs)
	if remaining == 0 {
		out.Resolve(values)
		return out.Promise
	}
	var firstError error
	for i, f := range inputs {
		f.observe(loop, func(value T, err error) {
			values[i] = value
			if firstError == nil {
				firstError = err
			}
			remaining--
			if remaining == 0 {
				if firstError != nil {
					out.Reject(firstError)
				} else {
					out.Resolve(values)
				}
			}
		})
	}
	return out.Promise
}
