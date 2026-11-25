package async

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func awaitStep[T any](t *testing.T, f *Future[Step[T]]) Step[T] {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := f.Await(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A source async generator first awaits each promise and then yields its
// resolved value. The yielded value crosses the generator's own async boundary.
func promiseInput(loop *Loop, inputs ...Resolvers[int]) Pull[int] {
	return func(context.Context) *Future[Step[int]] {
		if len(inputs) == 0 {
			return Resolved(loop, Step[int]{Done: true})
		}
		result := NewPromise[Step[int]](loop)
		input := inputs[0]
		inputs = inputs[1:]
		input.Promise.OnSettled(func(value int, err error) {
			if err != nil {
				result.Reject(err)
				return
			}
			loop.post(reaction, func() { result.Resolve(Step[int]{Value: value}) })
		})
		return result.Promise
	}
}

func finishTurn(t *testing.T, loop *Loop) {
	t.Helper()
	end := NewPromise[Step[int]](loop)
	loop.post(turn, func() { end.Resolve(Step[int]{Done: true}) })
	awaitStep(t, end.Promise)
}

func TestSourceGenerators(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stalled bool
	}{
		{"mergeAsyncGenerators()", false},
		{"mergeAsyncGenerators() yields all results", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loop := NewLoop()
			p1, p2, p3, p4 := NewPromise[int](loop), NewPromise[int](loop), NewPromise[int](loop), NewPromise[int](loop)
			g := MergeGenerators(t.Context(), loop, promiseInput(loop, p1, p2), promiseInput(loop, p3, p4))
			defer g.Close()
			lock := NewPromise[struct{}](loop)
			if !tc.stalled {
				lock.Resolve(struct{}{})
			}
			values := []int{}
			var consume func()
			consume = func() {
				g.Next().OnSettled(func(step Step[int], err error) {
					if err != nil {
						t.Fatal(err)
					}
					if step.Done {
						return
					}
					lock.Promise.OnSettled(func(struct{}, error) {
						values = append(values, step.Value)
						consume()
					})
				})
			}
			consume()
			p1.Resolve(1)
			p2.Resolve(2)
			finishTurn(t, loop)
			p3.Resolve(3)
			p4.Resolve(4)
			if tc.stalled {
				lock.Resolve(struct{}{})
			}
			finishTurn(t, loop)
			if !slices.Equal(values, []int{1, 2, 3, 4}) {
				t.Fatalf("results %v", values)
			}
		})
	}
	t.Run("bufferAsyncGenerator() prefetches results", func(t *testing.T) {
		loop := NewLoop()
		sum := -1
		g := BufferGenerator(t.Context(), loop, func(context.Context) *Future[Step[int]] {
			sum++
			return Resolved(loop, Step[int]{Done: sum == 4})
		}, 2, nil)
		defer g.Close()
		for _, want := range []int{2, 3, 4, 4} {
			if s := awaitStep(t, g.Next()); s.Done || sum != want {
				t.Fatalf("done %v, sum %d, want %d", s.Done, sum, want)
			}
		}
		if !awaitStep(t, g.Next()).Done {
			t.Fatal("missing EOF")
		}
	})
	t.Run("bufferAsyncGenerator() yields all results", func(t *testing.T) {
		loop := NewLoop()
		p := []Resolvers[int]{NewPromise[int](loop), NewPromise[int](loop), NewPromise[int](loop), NewPromise[int](loop)}
		g := BufferGenerator(t.Context(), loop, promiseInput(loop, p...), 2, nil)
		defer g.Close()
		for i, r := range p {
			result := g.Next()
			r.Resolve(i + 1)
			if s := awaitStep(t, result); s.Done || s.Value != i+1 {
				t.Fatalf("step %+v, want %d", s, i+1)
			}
		}
		if !awaitStep(t, g.Next()).Done {
			t.Fatal("missing EOF")
		}
	})
	t.Run("createCallbackGenerator()", func(t *testing.T) {
		loop := NewLoop()
		push, g := CallbackGenerator[int](t.Context(), loop, nil)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			for i := 0; i < 5; i++ {
				push(i)
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
		}()
		var values []int
		for v, err := range g.Values(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
			if v == 4 {
				break
			}
		}
		cancel()
		<-stopped
		if !slices.Equal(values, []int{0, 1, 2, 3, 4}) {
			t.Fatalf("callback values %v", values)
		}
	})
}

func TestGeneratorBoundaries(t *testing.T) {
	t.Run("closing a stalled merge cancels its in-flight inputs", func(t *testing.T) {
		loop := NewLoop()
		wait := NewPromise[Step[int]](loop)
		var owned context.Context
		g := MergeGenerators(t.Context(), loop, func(ctx context.Context) *Future[Step[int]] { owned = ctx; return wait.Promise })
		first, second := g.Next(), g.Next()
		finishTurn(t, loop)
		g.Close()
		if !awaitStep(t, first).Done || !awaitStep(t, second).Done || owned.Err() == nil {
			t.Fatal("in-flight pulls survived close")
		}
		wait.Resolve(Step[int]{Value: 42})
		if !awaitStep(t, g.Next()).Done {
			t.Fatal("late result after close")
		}
	})
	t.Run("buffer drains prior values then rejects once", func(t *testing.T) {
		loop := NewLoop()
		boom := errors.New("source failure")
		calls := 0
		var sizes []int
		g := BufferGenerator(t.Context(), loop, func(context.Context) *Future[Step[int]] {
			calls++
			if calls == 4 {
				return Rejected[Step[int]](loop, boom)
			}
			return Resolved(loop, Step[int]{Value: calls})
		}, 3, func(n int) { sizes = append(sizes, n) })
		defer g.Close()
		for _, want := range []int{1, 2, 3} {
			if step := awaitStep(t, g.Next()); step.Done || step.Value != want {
				t.Fatal(step)
			}
		}
		if _, err := g.Next().Await(t.Context()); !errors.Is(err, boom) {
			t.Fatal(err)
		}
		if !awaitStep(t, g.Next()).Done || len(sizes) != 3 {
			t.Fatal(sizes)
		}
	})
	t.Run("pre-cancelled contexts do not pull", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		g := BufferGenerator(ctx, NewLoop(), func(context.Context) *Future[Step[int]] { t.Fatal("unexpected read"); return nil }, 2, nil)
		if _, err := g.Next().Await(t.Context()); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("ready inputs alternate and each has one pending read", func(t *testing.T) {
		loop := NewLoop()
		input := func(base int) Pull[int] {
			i := 0
			return func(context.Context) *Future[Step[int]] {
				i++
				return Resolved(loop, Step[int]{Value: base + i, Done: i > 2})
			}
		}
		g := MergeGenerators(t.Context(), loop, input(0), input(10))
		var values []int
		for value, err := range g.Values(t.Context()) {
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		if !slices.Equal(values, []int{1, 11, 2, 12}) {
			t.Fatal(values)
		}
	})
	t.Run("callback values submitted before first read retain order", func(t *testing.T) {
		loop := NewLoop()
		var sizes []int
		push, g := CallbackGenerator[int](t.Context(), loop, func(n int) { sizes = append(sizes, n) })
		push(3)
		push(5)
		if awaitStep(t, g.Next()).Value != 3 || awaitStep(t, g.Next()).Value != 5 || !slices.Equal(sizes, []int{1, 2}) {
			t.Fatal(sizes)
		}
		g.Close()
		push(8)
		if !awaitStep(t, g.Next()).Done {
			t.Fatal("push after close")
		}
	})
}
