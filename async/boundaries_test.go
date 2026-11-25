package async_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/async"
)

func TestQueueBoundaries(t *testing.T) {
	t.Run("clear callback sees only waiting work in FIFO order", func(t *testing.T) {
		l := async.NewLoop()
		gate := async.NewPromise[int](l)
		q := makeQueue(t, l, 1, 0, true, func(int) *async.Future[int] { return gate.Promise })
		running := q.Add(1)
		a, b := q.Add(2), q.Add(3)
		var removed []int
		cause := errors.New("cleared")
		q.Clear(func(e async.Entry[int, int]) { removed = append(removed, e.Task); e.Reject(cause) })
		if !reflect.DeepEqual(removed, []int{2, 3}) {
			t.Fatal(removed)
		}
		for _, f := range []*async.Future[int]{a, b} {
			_, err := f.Await(t.Context())
			equal(t, err, cause)
		}
		equal(t, await(t, q.Pending()), 1)
		gate.Resolve(7)
		equal(t, await(t, running), 7)
		await(t, q.OnIdle())
	})
	t.Run("empty observes admission and idle observes completion", func(t *testing.T) {
		l := async.NewLoop()
		gate := async.NewPromise[int](l)
		q := makeQueue(t, l, 1, 0, false, func(int) *async.Future[int] { return gate.Promise })
		q.Add(2)
		e, i := q.OnEmpty(), q.OnIdle()
		equal(t, e, q.OnEmpty())
		equal(t, i, q.OnIdle())
		await(t, q.Start())
		await(t, e)
		equal(t, await(t, q.Pending()), 1)
		gate.Resolve(7)
		await(t, i)
		equal(t, await(t, q.Pending()), 0)
	})
	t.Run("limits and partial update", func(t *testing.T) {
		l := async.NewLoop()
		worker := func(string) *async.Future[int] { return async.Resolved(l, 1) }
		for _, test := range []struct {
			limits async.Limits
			want   string
		}{
			{async.Limits{}, "Invalid queue configuration, must specify either 'concurrency' or 'frequency'."},
			{async.Limits{Concurrency: async.AtMost(0)}, "Invalid value for queue 'concurrency' option. Got 0, expected a number greater than zero."},
			{async.Limits{Frequency: async.AtMost(-2)}, "Invalid value for queue 'frequency' option. Got -2, expected a number greater than zero."},
		} {
			_, err := async.NewQueue(l, async.QueueOptions{Limits: test.limits}, worker)
			if err == nil || err.Error() != test.want {
				t.Fatal(err)
			}
		}
		q := makeQueue(t, l, 1, 10, false, worker)
		if err := q.SetParameters(async.Limits{Frequency: async.AtMost(20)}); err != nil {
			t.Fatal(err)
		}
		q.Add("a")
		q.Add("b")
		await(t, q.Start())
		q.Pause()
		equal(t, q.Size(), 1)
	})
	t.Run("multiple waiters and first settlement", func(t *testing.T) {
		l := async.NewLoop()
		r := async.NewPromise[int](l)
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := r.Promise.Await(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for range 24 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if v, err := r.Promise.Await(t.Context()); v != 8 || err != nil {
					t.Errorf("value %d, error %v", v, err)
				}
			}()
		}
		r.Resolve(8)
		r.Reject(errors.New("late"))
		r.Resolve(9)
		wg.Wait()
	})
	t.Run("all settles before delivering the first rejection", func(t *testing.T) {
		l := async.NewLoop()
		a, b := async.NewPromise[int](l), async.NewPromise[int](l)
		first := errors.New("first")
		all := async.AllSettledWithThrow(l, a.Promise, b.Promise)
		b.Reject(first)
		a.Reject(errors.New("later"))
		_, err := all.Await(t.Context())
		equal(t, err, first)
		v := await(t, async.AllSettledWithThrow(l, async.Resolved(l, 3), async.Resolved(l, 2)))
		if !reflect.DeepEqual(v, []int{3, 2}) {
			t.Fatal(v)
		}
	})
}

func TestLoopBackgroundDriver(t *testing.T) {
	l := async.NewLoop()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- l.Run(ctx) }()
	// A running loop processes completions even without a caller pumping Await.
	results := make(chan int, 1)
	q, err := async.NewQueue(l, async.QueueOptions{Limits: async.Limits{Concurrency: async.AtMost(1)}, InitialStart: true},
		func(n int) *async.Future[int] { return async.Resolved(l, n+1) })
	if err != nil {
		t.Fatal(err)
	}
	q.Add(2).OnSettled(func(n int, err error) {
		if err != nil {
			t.Error(err)
		}
		results <- n
	})
	select {
	case got := <-results:
		equal(t, got, 3)
	case <-time.After(2 * time.Second):
		t.Fatal("background loop did not dispatch")
	}
	await(t, q.OnIdle())
	cancel()
	equal(t, <-stopped, context.Canceled)
}
