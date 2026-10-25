package runtime

import (
	"context"
	"fmt"
	"iter"
	"math"

	"ponder.local/ponder-go/store"
)

// SourceIntervalStep is one completed next() operation. Done distinguishes
// exhaustion from a zero-valued interval, as on the original async generator.
type SourceIntervalStep struct {
	Interval store.SourceInterval
	Done     bool
	Err      error
}

// SourceIntervalGenerator starts each next() synchronously and returns its
// pending result. Implementations send at most once to a buffered channel, so
// already-started work can finish after consumer early termination.
type SourceIntervalGenerator struct {
	Interval store.SourceInterval
	Next     func(context.Context) <-chan SourceIntervalStep
}

func sourceAwaitIntervalSteps(ctx context.Context, pending []<-chan SourceIntervalStep) ([]SourceIntervalStep, error) {
	type indexed struct {
		index int
		step  SourceIntervalStep
	}
	waiting, cancel := context.WithCancel(ctx)
	defer cancel()
	completed := make(chan indexed, len(pending))
	for index, task := range pending {
		if task == nil {
			return nil, fmt.Errorf("source interval next returned no task")
		}
		go func(index int, task <-chan SourceIntervalStep) {
			select {
			case step, ok := <-task:
				if !ok {
					step = SourceIntervalStep{Err: fmt.Errorf("source interval task closed without a result")}
				}
				completed <- indexed{index, step}
			case <-waiting.Done():
			}
		}(index, task)
	}
	results := make([]SourceIntervalStep, len(pending))
	for range pending {
		select {
		case result := <-completed:
			if result.step.Err != nil {
				return nil, result.step.Err
			}
			results[result.index] = result.step
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return results, nil
}

// MergeSourceGeneratorIntervals preserves insertion order, concurrent head
// acquisition and next-before-yield prefetch. Generator ownership/closure stays
// with the caller, matching the source helper's retained child generators.
func MergeSourceGeneratorIntervals(ctx context.Context, generators []SourceIntervalGenerator) iter.Seq2[store.SourceInterval, error] {
	return func(yield func(store.SourceInterval, error) bool) {
		fail := func(err error) { yield(store.SourceInterval{}, err) }
		pending := make([]<-chan SourceIntervalStep, len(generators))
		cursor := math.Inf(1)
		for i, generator := range generators {
			if generator.Next == nil {
				fail(fmt.Errorf("source interval generator has no next operation"))
				return
			}
			cursor = math.Min(cursor, generator.Interval[0])
			pending[i] = generator.Next(ctx)
		}
		results, err := sourceAwaitIntervalSteps(ctx, pending)
		if err != nil {
			fail(err)
			return
		}
		for {
			supremum := math.Inf(1)
			active := false
			for _, result := range results {
				if !result.Done {
					active = true
					supremum = math.Min(supremum, result.Interval[1])
				}
			}
			if !active {
				return
			}
			indices := []int{}
			pending = []<-chan SourceIntervalStep{}
			for i, result := range results {
				if !result.Done && result.Interval[1] == supremum {
					indices = append(indices, i)
					pending = append(pending, generators[i].Next(ctx))
				}
			}
			if cursor <= supremum {
				if !yield(store.SourceInterval{cursor, supremum}, nil) {
					return
				}
				cursor = supremum + 1
			}
			next, err := sourceAwaitIntervalSteps(ctx, pending)
			if err != nil {
				fail(err)
				return
			}
			for i, result := range next {
				results[indices[i]] = result
			}
		}
	}
}
