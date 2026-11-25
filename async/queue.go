package async

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"
)

// A zero Limit leaves an option unspecified. Unlimited explicitly removes it
// when updating an existing queue.
type Limit struct {
	set, bounded bool
	value        float64
}

func AtMost(value float64) Limit { return Limit{set: true, bounded: true, value: value} }
func Unlimited() Limit           { return Limit{set: true} }

type Limits struct{ Concurrency, Frequency Limit }

func (l Limits) validate() error {
	if !l.Concurrency.bounded && !l.Frequency.bounded {
		return errors.New("Invalid queue configuration, must specify either 'concurrency' or 'frequency'.")
	}
	for _, item := range []struct {
		name  string
		limit Limit
	}{{"concurrency", l.Concurrency}, {"frequency", l.Frequency}} {
		if item.limit.bounded && item.limit.value <= 0 {
			return fmt.Errorf("Invalid value for queue '%s' option. Got %v, expected a number greater than zero.", item.name, item.limit.value)
		}
	}
	return nil
}
func (l Limit) allows(count int) bool { return !l.bounded || float64(count) < l.value }

type QueueOptions struct {
	Limits
	InitialStart bool
	// NextTick admits replacements on the next turn, as in the Node queue.
	// The default admits replacements in the completion reaction.
	NextTick bool
}

// Entry lets Clear's callback settle a removed task's promise.
type Entry[T, R any] struct {
	Task T
	Resolvers[R]
}

type Queue[T, R any] struct {
	mu                   sync.Mutex
	loop                 *Loop
	worker               func(T) *Future[R]
	options              QueueOptions
	entries              []Entry[T, R]
	pending, requests    int
	second               int64
	started, dispatching bool
	timer                *Timer
	empty, idle          *Resolvers[struct{}]
}

func NewQueue[T, R any](loop *Loop, options QueueOptions, worker func(T) *Future[R]) (*Queue[T, R], error) {
	if err := options.Limits.validate(); err != nil {
		return nil, err
	}
	if loop == nil || worker == nil {
		return nil, errors.New("a queue requires a loop and a worker")
	}
	return &Queue[T, R]{loop: loop, options: options, worker: worker, started: options.InitialStart}, nil
}

func (q *Queue[T, R]) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

func (q *Queue[T, R]) IsStarted() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.started
}

func (q *Queue[T, R]) Pause() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.started = false
}

func (q *Queue[T, R]) Pending() *Future[int] {
	result := NewPromise[int](q.loop)
	q.loop.post(turn, func() {
		q.mu.Lock()
		n := q.pending
		q.mu.Unlock()
		result.Resolve(n)
	})
	return result.Promise
}

func (q *Queue[T, R]) Start() *Future[struct{}] {
	result := NewPromise[struct{}](q.loop)
	q.loop.post(turn, func() {
		q.mu.Lock()
		q.started = true
		q.mu.Unlock()
		q.drain()
		result.Resolve(struct{}{})
	})
	return result.Promise
}

// Add admits available work synchronously. The worker owns its returned future.
func (q *Queue[T, R]) Add(task T) *Future[R] {
	r := NewPromise[R](q.loop)
	q.mu.Lock()
	q.entries = append(q.entries, Entry[T, R]{task, r})
	q.mu.Unlock()
	q.drain()
	return r.Promise
}

func (q *Queue[T, R]) SetParameters(l Limits) error {
	if err := l.validate(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if l.Concurrency.set {
		q.options.Concurrency = l.Concurrency
	}
	if l.Frequency.set {
		q.options.Frequency = l.Frequency
	}
	return nil
}

func (q *Queue[T, R]) Clear(callback func(Entry[T, R])) {
	q.mu.Lock()
	entries := q.entries
	q.entries = nil
	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	q.mu.Unlock()
	if callback != nil {
		for _, entry := range entries {
			callback(entry)
		}
	}
}

func (q *Queue[T, R]) OnEmpty() *Future[struct{}] {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.waiter(&q.empty, len(q.entries) == 0)
}
func (q *Queue[T, R]) OnIdle() *Future[struct{}] {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.waiter(&q.idle, len(q.entries) == 0 && q.pending == 0)
}
func (q *Queue[T, R]) waiter(current **Resolvers[struct{}], ready bool) *Future[struct{}] {
	if *current != nil {
		return (*current).Promise
	}
	if ready {
		return Resolved(q.loop, struct{}{})
	}
	r := NewPromise[struct{}](q.loop)
	*current = &r
	return r.Promise
}

func (q *Queue[T, R]) drain() {
	q.mu.Lock()
	if q.dispatching {
		q.mu.Unlock()
		return
	}
	q.dispatching = true
	defer func() {
		q.dispatching = false
		q.mu.Unlock()
	}()
	for q.started {
		now := time.Now()
		if q.second != now.Unix() {
			q.second = now.Unix()
			q.requests = 0
		}
		if q.timer != nil {
			return
		}
		if !q.options.Frequency.allows(q.requests) {
			// Match a fixed wall-clock second. A cleared timer may have already
			// posted its callback; it must not release a replacement timer.
			if !math.IsNaN(q.options.Frequency.value) {
				var timer *Timer
				timer = q.loop.AfterFunc(now.Truncate(time.Second).Add(time.Second).Sub(now), func() {
					q.mu.Lock()
					if q.timer != timer {
						q.mu.Unlock()
						return
					}
					q.timer = nil
					q.mu.Unlock()
					q.drain()
				})
				q.timer = timer
			}
			return
		}
		if len(q.entries) == 0 || !q.options.Concurrency.allows(q.pending) {
			return
		}
		entry := q.entries[0]
		q.entries[0] = Entry[T, R]{}
		q.entries = q.entries[1:]
		q.pending++
		q.requests++
		q.mu.Unlock()
		workerResult := q.invoke(entry.Task)
		q.mu.Lock()
		workerResult.observe(q.loop, func(value R, err error) { q.finish(entry, value, err) })
		if q.empty != nil && len(q.entries) == 0 {
			q.empty.Resolve(struct{}{})
			q.empty = nil
		}
	}
}

func (q *Queue[T, R]) invoke(task T) (result *Future[R]) {
	defer func() {
		if v := recover(); v != nil {
			result = Rejected[R](q.loop, fmt.Errorf("queue worker panicked: %v", v))
		}
	}()
	result = q.worker(task)
	if result == nil {
		return Rejected[R](q.loop, errors.New("queue worker returned a nil future"))
	}
	return result
}

func (q *Queue[T, R]) finish(entry Entry[T, R], value R, err error) {
	q.mu.Lock()
	q.pending--
	if err != nil {
		entry.Reject(err)
	} else {
		entry.Resolve(value)
	}
	if q.idle != nil && len(q.entries) == 0 && q.pending == 0 {
		q.idle.Resolve(struct{}{})
		q.idle = nil
	}
	nextTick := q.options.NextTick
	q.mu.Unlock()
	if nextTick {
		q.loop.post(turn, q.drain)
	} else {
		q.drain()
	}
}

// Cancel removes a waiting task. A running worker retains ownership of its result.
func (q *Queue[T, R]) Cancel(future *Future[R], err error) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, entry := range q.entries {
		if entry.Promise != future {
			continue
		}
		q.entries = slices.Delete(q.entries, i, i+1)
		entry.Reject(err)
		if len(q.entries) == 0 {
			if q.empty != nil {
				q.empty.Resolve(struct{}{})
				q.empty = nil
			}
			if q.pending == 0 && q.idle != nil {
				q.idle.Resolve(struct{}{})
				q.idle = nil
			}
		}
		return true
	}
	return false
}
