package async_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"ponder.local/ponder-go/async"
)

type unit = struct{}

func await[T any](t *testing.T, f *async.Future[T]) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	v, err := f.Await(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func equal[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func makeQueue[T, R any](t *testing.T, l *async.Loop, c, f int, started bool, worker func(T) *async.Future[R]) *async.Queue[T, R] {
	t.Helper()
	limits := async.Limits{}
	if c != 0 {
		limits.Concurrency = async.AtMost(float64(c))
	}
	if f != 0 {
		limits.Frequency = async.AtMost(float64(f))
	}
	q, err := async.NewQueue(l, async.QueueOptions{Limits: limits, InitialStart: started, NextTick: true}, worker)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func delay[T any](l *async.Loop, d time.Duration, value T, err error) *async.Future[T] {
	r := async.NewPromise[T](l)
	time.AfterFunc(d, func() {
		if err != nil {
			r.Reject(err)
		} else {
			r.Resolve(value)
		}
	})
	return r.Promise
}

// Both originals have the same queue assertions. Each package runs its own
// instances and observers, including its two-queue event-loop ordering case.
func TestSourceQueues(t *testing.T) {
	for _, source := range []string{"core", "utils"} {
		t.Run(source, func(t *testing.T) {
			run := func(name string, test func(*testing.T, *async.Loop)) {
				t.Run(name, func(t *testing.T) { test(t, async.NewLoop()) })
			}
			run("add resolves", func(t *testing.T, l *async.Loop) {
				q := makeQueue(t, l, 1, 1, true, func(unit) *async.Future[int] { return async.Resolved(l, 1) })
				equal(t, await(t, q.Add(unit{})), 1)
			})
			run("add rejects", func(t *testing.T, l *async.Loop) {
				q := makeQueue(t, l, 1, 1, false, func(unit) *async.Future[unit] { return async.Rejected[unit](l, nil) })
				p := q.Add(unit{})
				await(t, q.Start())
				_, err := p.Await(t.Context())
				equal(t, errors.Is(err, async.ErrRejected), true)
			})
			ready := func(t *testing.T, l *async.Loop, c, f int, start bool) *async.Queue[unit, unit] {
				return makeQueue(t, l, c, f, start, func(unit) *async.Future[unit] { return async.Resolved(l, unit{}) })
			}
			run("size", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, false)
				q.Add(unit{})
				equal(t, q.Size(), 1)
				await(t, q.Start())
				equal(t, q.Size(), 0)
			})
			run("pending", func(t *testing.T, l *async.Loop) {
				r := async.NewPromise[unit](l)
				q := makeQueue(t, l, 1, 1, true, func(unit) *async.Future[unit] { return r.Promise })
				q.Add(unit{})
				equal(t, await(t, q.Pending()), 1)
				r.Resolve(unit{})
				equal(t, await(t, q.Pending()), 0)
			})
			run("clear", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, false)
				for range 3 {
					q.Add(unit{})
				}
				q.Clear(nil)
				equal(t, q.Size(), 0)
			})
			run("clear timer", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 0, 1, false)
				for range 3 {
					q.Add(unit{})
				}
				await(t, q.Start())
				q.Clear(nil)
				await(t, q.OnIdle())
				equal(t, q.Size(), 0)
				equal(t, await(t, q.Pending()), 0)
			})
			run("isStarted", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, false)
				equal(t, q.IsStarted(), false)
				await(t, q.Start())
				equal(t, q.IsStarted(), true)
			})
			run("initial start", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, true)
				equal(t, q.IsStarted(), true)
				await(t, q.Add(unit{}))
				equal(t, q.Size(), 0)
			})
			run("start", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, false)
				p := q.Add(unit{})
				equal(t, q.Size(), 1)
				await(t, q.Start())
				equal(t, q.IsStarted(), true)
				await(t, p)
				equal(t, q.Size(), 0)
			})
			run("pause", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, true)
				q.Pause()
				q.Add(unit{})
				equal(t, q.Size(), 1)
			})
			run("onIdle short loop", func(t *testing.T, l *async.Loop) { q := ready(t, l, 1, 1, false); await(t, q.OnIdle()) })
			run("onIdle", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 0, false)
				q.Add(unit{})
				p := q.OnIdle()
				await(t, q.Start())
				await(t, p)
			})
			run("onIdle twice", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, false)
				q.Add(unit{})
				q.OnIdle()
				await(t, q.Start())
				q.Pause()
				q.Add(unit{})
				p := q.OnIdle()
				await(t, q.Start())
				await(t, p)
			})
			run("onEmpty short loop", func(t *testing.T, l *async.Loop) { q := ready(t, l, 1, 1, false); await(t, q.OnEmpty()) })
			run("onEmpty", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, false)
				q.Add(unit{})
				p := q.OnEmpty()
				await(t, q.Start())
				await(t, p)
			})
			run("onEmpty twice", func(t *testing.T, l *async.Loop) {
				q := ready(t, l, 1, 1, false)
				q.Add(unit{})
				q.OnEmpty()
				await(t, q.Start())
				q.Pause()
				q.Add(unit{})
				p := q.OnEmpty()
				await(t, q.Start())
				await(t, p)
			})
			counted := func(t *testing.T, l *async.Loop, c, f int, calls *int) *async.Queue[unit, unit] {
				return makeQueue(t, l, c, f, false, func(unit) *async.Future[unit] {
					*calls += 1
					return async.Resolved(l, unit{})
				})
			}
			run("concurrency", func(t *testing.T, l *async.Loop) {
				calls := 0
				q := counted(t, l, 2, 5, &calls)
				for range 4 {
					q.Add(unit{})
				}
				await(t, q.Start())
				q.Pause()
				equal(t, q.Size(), 2)
				equal(t, calls, 2)
			})
			run("frequency", func(t *testing.T, l *async.Loop) {
				calls := 0
				q := counted(t, l, 5, 2, &calls)
				for range 4 {
					q.Add(unit{})
				}
				await(t, q.Start())
				equal(t, q.Size(), 2)
				equal(t, calls, 2)
				await(t, delay(l, 1010*time.Millisecond, unit{}, nil))
				equal(t, q.Size(), 0)
				equal(t, calls, 4)
			})
			run("event loop", func(t *testing.T, l *async.Loop) {
				var out, want []int
				queue := func(id int) *async.Queue[unit, unit] {
					return makeQueue(t, l, 1, 0, false, func(unit) *async.Future[unit] { out = append(out, id); return async.Resolved(l, unit{}) })
				}
				a, b := queue(1), queue(2)
				for range 10 {
					a.Add(unit{})
					b.Add(unit{})
					want = append(want, 1, 2)
				}
				await(t, async.AllSettledWithThrow(l, a.Start(), b.Start()))
				await(t, async.AllSettledWithThrow(l, a.OnIdle(), b.OnIdle()))
				if !reflect.DeepEqual(out, want) {
					t.Fatalf("worker order: %v", out)
				}
			})
			run("update parameters", func(t *testing.T, l *async.Loop) {
				calls := 0
				q := counted(t, l, 2, 5, &calls)
				for range 6 {
					q.Add(unit{})
				}
				await(t, q.Start())
				q.Pause()
				equal(t, q.Size(), 4)
				equal(t, calls, 2)
				if err := q.SetParameters(async.Limits{Concurrency: async.Unlimited(), Frequency: async.AtMost(8)}); err != nil {
					t.Fatal(err)
				}
				await(t, q.Start())
				q.Pause()
				equal(t, q.Size(), 0)
				equal(t, calls, 6)
			})
		})
	}
}

func TestSourcePromises(t *testing.T) {
	for _, source := range []string{"core", "utils"} {
		t.Run(source, func(t *testing.T) {
			t.Run("resolves", func(t *testing.T) {
				l := async.NewLoop()
				r := async.NewPromise[float64](l)
				r.Resolve(1)
				equal(t, await(t, r.Promise), float64(1))
			})
			t.Run("rejects", func(t *testing.T) {
				l := async.NewLoop()
				r := async.NewPromise[unit](l)
				rejected := false
				r.Promise.OnSettled(func(_ unit, err error) { rejected = err != nil })
				caught := async.NewPromise[unit](l)
				async.Rejected[unit](l, nil).OnSettled(func(_ unit, err error) { r.Reject(err); caught.Resolve(unit{}) })
				await(t, caught.Promise)
				equal(t, rejected, true)
			})
		})
	}
	t.Run("promiseAllSettledWithThrow", func(t *testing.T) {
		l := async.NewLoop()
		first := errors.New("1")
		last := errors.New("2")
		all := async.AllSettledWithThrow(l, async.Resolved(l, 1), delay(l, 10*time.Millisecond, 2, nil), delay(l, 10*time.Millisecond, 0, first), delay(l, 20*time.Millisecond, 0, last))
		_, err := all.Await(t.Context())
		equal(t, err, first)
	})
}

// The native finite task domain models the source's three literal alternatives.
type letter interface{ queueLetter() }
type a struct{}
type b struct{}
type c struct{}

func (a) queueLetter() {}
func (b) queueLetter() {}
func (c) queueLetter() {}

func TestSourcePromiseTypes(t *testing.T) {
	for _, source := range []string{"core", "utils"} {
		t.Run(source, func(t *testing.T) {
			t.Run("resolve type", func(t *testing.T) {
				r := async.NewPromise[float64](async.NewLoop())
				var resolve func(float64) = r.Resolve
				resolve(1)
				equal(t, await(t, r.Promise), float64(1))
			})
			t.Run("promise type", func(t *testing.T) {
				r := async.NewPromise[float64](async.NewLoop())
				var promise *async.Future[float64] = r.Promise
				r.Resolve(2)
				equal(t, await(t, promise), float64(2))
			})
			t.Run("add type", func(t *testing.T) {
				l := async.NewLoop()
				q := makeQueue(t, l, 1, 0, false, func(letter) *async.Future[unit] { return async.Resolved(l, unit{}) })
				var add func(letter) *async.Future[unit] = q.Add
				values := []*async.Future[unit]{add(a{}), add(b{}), add(c{})}
				await(t, q.Start())
				await(t, async.AllSettledWithThrow(l, values...))
			})
		})
	}
}
