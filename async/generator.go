package async

import (
	"context"
	"errors"
	"iter"
)

// Step is one result of an asynchronous pull.
type Step[T any] struct {
	Value T
	Done  bool
}

// Pull starts one nonblocking read. Closing the consuming generator cancels ctx.
type Pull[T any] func(ctx context.Context) *Future[Step[T]]

// Generator serializes reads on its loop. Next can be called from several
// goroutines. Close it when stopping early, or range over Values.
type Generator[T any] struct {
	loop   *Loop
	ctx    context.Context
	cancel context.CancelCauseFunc
	stop   func() bool

	// The loop owns all fields below.
	readers  []Resolvers[Step[T]]
	pump     func()
	release  func()
	finished bool
	endErr   error
}

var errGeneratorClosed = errors.New("generator closed")

func newGenerator[T any](ctx context.Context, loop *Loop) *Generator[T] {
	ctx, cancel := context.WithCancelCause(ctx)
	return &Generator[T]{loop: loop, ctx: ctx, cancel: cancel}
}

// Install cancellation after the operator's fields are initialized.
func (g *Generator[T]) watchCancellation() {
	// Register on the loop so an already-cancelled context cannot race setup.
	g.loop.post(reaction, func() {
		g.stop = context.AfterFunc(g.ctx, func() {
			g.loop.post(reaction, func() { g.finish(context.Cause(g.ctx)) })
		})
	})
}

func (g *Generator[T]) Next() *Future[Step[T]] {
	r := NewPromise[Step[T]](g.loop)
	g.loop.post(reaction, func() {
		g.readers = append(g.readers, r)
		g.advance()
	})
	return r.Promise
}

func (g *Generator[T]) advance() {
	switch {
	case g.finished:
		g.settleEnd()
	case g.ctx.Err() != nil:
		g.finish(context.Cause(g.ctx))
	default:
		g.pump()
	}
}

func (g *Generator[T]) emit(value T) {
	r := g.readers[0]
	g.readers[0] = Resolvers[Step[T]]{}
	g.readers = g.readers[1:]
	r.Resolve(Step[T]{Value: value})
}

func (g *Generator[T]) finish(err error) {
	if g.finished {
		return
	}
	g.finished = true
	if !errors.Is(err, errGeneratorClosed) {
		g.endErr = err
	}
	if g.stop != nil {
		g.stop()
	}
	g.cancel(errGeneratorClosed)
	if g.release != nil {
		g.release()
	}
	g.pump, g.release = nil, nil
	g.settleEnd()
}

func (g *Generator[T]) settleEnd() {
	for _, r := range g.readers {
		if g.endErr != nil {
			r.Reject(g.endErr)
			g.endErr = nil
		} else {
			r.Resolve(Step[T]{Done: true})
		}
	}
	g.readers = nil
}

func (g *Generator[T]) Close() { g.cancel(errGeneratorClosed) }

func (g *Generator[T]) Values(ctx context.Context) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		defer g.Close()
		for {
			step, err := g.Next().Await(ctx)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			if step.Done || !yield(step.Value, nil) {
				return
			}
		}
	}
}

// A pending input may outlive the consumer. Detaching its callback releases
// references to the generator and its buffered values even for an unresolved future.
type stepListener[T any] struct {
	callback func(Step[T], error)
}

func (s *stepListener[T]) watch(loop *Loop, pull Pull[T], ctx context.Context, cb func(Step[T], error)) {
	s.callback = cb
	var f *Future[Step[T]]
	if pull != nil {
		f = pull(ctx)
	}
	if f == nil {
		f = Rejected[Step[T]](loop, errors.New("generator pull returned a nil future"))
	}
	f.observe(loop, s.deliver)
}

func (s *stepListener[T]) deliver(step Step[T], err error) {
	if cb := s.callback; cb != nil {
		s.callback = nil
		cb(step, err)
	}
}

// MergeGenerators keeps one read outstanding on each input. After yielding a
// value it moves that input to the end of the priority order for ready reads.
func MergeGenerators[T any](ctx context.Context, loop *Loop, inputs ...Pull[T]) *Generator[T] {
	type input struct {
		pull     Pull[T]
		listener stepListener[T]
		ready    bool
		step     Step[T]
		err      error
	}
	g := newGenerator[T](ctx, loop)
	active := make([]*input, len(inputs))
	for i, pull := range inputs {
		active[i] = &input{pull: pull}
	}
	read := func(in *input) {
		in.ready = false
		in.listener.watch(loop, in.pull, g.ctx, func(step Step[T], err error) {
			in.step, in.err, in.ready = step, err, true
			g.advance()
		})
	}
	started := false
	g.pump = func() {
		if !started {
			started = true
			for _, in := range active {
				read(in)
			}
		}
		for len(g.readers) > 0 {
			if len(active) == 0 {
				g.finish(nil)
				return
			}
			index := -1
			for i, in := range active {
				if in.ready {
					index = i
					break
				}
			}
			if index < 0 {
				return
			}
			in := active[index]
			copy(active[index:], active[index+1:])
			active[len(active)-1] = nil
			active = active[:len(active)-1]
			if in.err != nil {
				g.finish(in.err)
				return
			}
			if !in.step.Done {
				value := in.step.Value
				in.step = Step[T]{}
				active = append(active, in)
				read(in)
				g.emit(value)
			}
		}
	}
	g.release = func() {
		for _, in := range active {
			in.listener.callback = nil
		}
		active = nil
	}
	g.watchCancellation()
	return g
}

// BufferGenerator prefetches up to size unconsumed values, and reports the
// buffered count after each insertion. At most one read is pending on the input.
func BufferGenerator[T any](ctx context.Context, loop *Loop, pull Pull[T], size int, onBuffer func(int)) *Generator[T] {
	g := newGenerator[T](ctx, loop)
	size = max(1, size)
	var buffer []T
	var listener stepListener[T]
	var done bool
	var endErr error
	g.pump = func() {
		for len(g.readers) > 0 && len(buffer) > 0 {
			g.emit(buffer[0])
			var zero T
			buffer[0] = zero
			buffer = buffer[1:]
		}
		if done {
			if len(buffer) == 0 {
				g.finish(endErr)
			}
			return
		}
		if len(buffer) >= size || listener.callback != nil {
			return
		}
		listener.watch(loop, pull, g.ctx, func(step Step[T], err error) {
			done, endErr = step.Done || err != nil, err
			if !done {
				buffer = append(buffer, step.Value)
				if onBuffer != nil {
					onBuffer(len(buffer))
				}
			}
			g.advance()
		})
	}
	g.release = func() { listener.callback = nil; buffer = nil }
	g.watchCancellation()
	return g
}

// CallbackGenerator turns callback deliveries into FIFO reads. Push is safe
// before the first read and from other goroutines. Close releases queued values.
func CallbackGenerator[T any](ctx context.Context, loop *Loop, onBuffer func(int)) (push func(T), generator *Generator[T]) {
	g := newGenerator[T](ctx, loop)
	var buffer []T
	g.pump = func() {
		for len(g.readers) > 0 && len(buffer) > 0 {
			g.emit(buffer[0])
			var zero T
			buffer[0] = zero
			buffer = buffer[1:]
		}
	}
	g.release = func() { buffer = nil }
	g.watchCancellation()
	return func(value T) {
		if g.ctx.Err() != nil {
			return
		}
		loop.post(reaction, func() {
			if g.finished || g.ctx.Err() != nil {
				return
			}
			buffer = append(buffer, value)
			if onBuffer != nil {
				onBuffer(len(buffer))
			}
			g.advance()
		})
	}, g
}
