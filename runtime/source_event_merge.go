package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"math"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
)

// SourceEventBatch is an owned batch of decoded public events. Merge replaces
// Events with its unconsumed suffix, matching the source generator's mutation.
// The event maps themselves are never copied; callbacks and chain identity stay
// attached to the actual objects returned by the public event decoder.
type SourceEventBatch struct {
	Events     []protocol.Object `json:"events"`
	Checkpoint string            `json:"checkpoint"`
	BlockRange [2]float64        `json:"blockRange"`
}

type SourceEventStep struct {
	Batch *SourceEventBatch
	Done  bool
	Err   error
}

// SourceEventGenerator starts a next operation synchronously. The returned
// channel must be buffered and receive exactly one result. A pending next can
// finish after consumer termination; ownership of the child remains with the
// caller, as in the source helper (which does not call child.return()).
type SourceEventGenerator struct {
	Next func(context.Context) <-chan SourceEventStep
}

type SourceEventGroup struct {
	ChainID    uint64            `json:"chainId"`
	Events     []protocol.Object `json:"events"`
	Checkpoint string            `json:"checkpoint"`
	BlockRange [2]float64        `json:"blockRange"`
}

func sourceAwaitEventSteps(ctx context.Context, pending []<-chan SourceEventStep) ([]SourceEventStep, error) {
	type indexed struct {
		index int
		step  SourceEventStep
	}
	waiting, cancel := context.WithCancel(ctx)
	defer cancel()
	completed := make(chan indexed, len(pending))
	for index, task := range pending {
		if task == nil {
			return nil, fmt.Errorf("source event next returned no task")
		}
		go func(index int, task <-chan SourceEventStep) {
			select {
			case step, ok := <-task:
				if !ok {
					step = SourceEventStep{Err: fmt.Errorf("source event task closed without a result")}
				}
				if step.Err == nil && !step.Done && step.Batch == nil {
					step.Err = fmt.Errorf("source event next returned no batch")
				}
				completed <- indexed{index, step}
			case <-waiting.Done():
			}
		}(index, task)
	}
	results := make([]SourceEventStep, len(pending))
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

func sourceEventCheckpoint(event protocol.Object) (string, error) {
	value, ok := event["checkpoint"].(string)
	if !ok {
		return "", fmt.Errorf("source event checkpoint must be a string")
	}
	return value, nil
}
func sourceEventChainID(event protocol.Object) (uint64, error) {
	chain, ok := event["chain"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("source event chain object required")
	}
	var n float64
	switch value := chain["id"].(type) {
	case uint64:
		return value, nil
	case uint:
		return uint64(value), nil
	case int:
		if value >= 0 {
			return uint64(value), nil
		}
		return 0, fmt.Errorf("source event chain id must be nonnegative")
	case int64:
		if value >= 0 {
			return uint64(value), nil
		}
		return 0, fmt.Errorf("source event chain id must be nonnegative")
	case float64:
		n = value
	case json.Number:
		var err error
		n, err = value.Float64()
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("source event chain id must be numeric")
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
		return 0, fmt.Errorf("source event chain id must be a nonnegative safe integer")
	}
	return uint64(n), nil
}

// sourcePartitionEvents mirrors the source binary partition, including its
// sorted-input contract. Do not sort input or silently filter nonmonotone data.
func sourcePartitionEvents(events []protocol.Object, supremum string) ([]protocol.Object, []protocol.Object, error) {
	empty := []protocol.Object{}
	if len(events) == 0 {
		return empty, empty, nil
	}
	first, err := sourceEventCheckpoint(events[0])
	if err != nil {
		return nil, nil, err
	}
	if first > supremum {
		return empty, events, nil
	}
	last, err := sourceEventCheckpoint(events[len(events)-1])
	if err != nil {
		return nil, nil, err
	}
	if last <= supremum {
		return events, empty, nil
	}
	low, high := 0, len(events)
	for low < high {
		middle := (low + high) / 2
		cursor, err := sourceEventCheckpoint(events[middle])
		if err != nil {
			return nil, nil, err
		}
		if cursor <= supremum {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return append([]protocol.Object{}, events[:low]...), append([]protocol.Object{}, events[low:]...), nil
}

// MergeSourceEventsWithEventOrder ports mergeAsyncGeneratorsWithEventOrder.
// It concurrently acquires the initial heads, emits per-chain groups in input
// order, and starts the first minimum-checkpoint generator's next before yield.
// BlockRange keeps the batch's original lower bound, even after partial output.
func MergeSourceEventsWithEventOrder(ctx context.Context, generators []SourceEventGenerator) iter.Seq2[[]SourceEventGroup, error] {
	return func(yield func([]SourceEventGroup, error) bool) {
		fail := func(err error) { yield(nil, err) }
		if err := ctx.Err(); err != nil {
			fail(err)
			return
		}
		pending := make([]<-chan SourceEventStep, len(generators))
		for i, generator := range generators {
			if generator.Next == nil {
				fail(fmt.Errorf("source event generator has no next operation"))
				return
			}
			pending[i] = generator.Next(ctx)
		}
		results, err := sourceAwaitEventSteps(ctx, pending)
		if err != nil {
			fail(err)
			return
		}
		for {
			index := -1
			supremum := ""
			for i, result := range results {
				if !result.Done && (index < 0 || result.Batch.Checkpoint < supremum) {
					index = i
					supremum = result.Batch.Checkpoint
				}
			}
			if index < 0 {
				return
			}
			groups := []SourceEventGroup{}
			for _, result := range results {
				if result.Done {
					continue
				}
				left, right, err := sourcePartitionEvents(result.Batch.Events, supremum)
				if err != nil {
					fail(err)
					return
				}
				if len(left) > 0 {
					event := left[len(left)-1]
					chainID, err := sourceEventChainID(event)
					if err != nil {
						fail(err)
						return
					}
					span := result.Batch.BlockRange
					cursor := result.Batch.Checkpoint
					if len(right) > 0 {
						cursor, err = sourceEventCheckpoint(event)
						if err != nil {
							fail(err)
							return
						}
						cp, err := checkpoint.Decode(cursor)
						if err != nil {
							fail(err)
							return
						}
						span[1] = float64(cp.BlockNumber)
					}
					groups = append(groups, SourceEventGroup{ChainID: chainID, Events: left, Checkpoint: cursor, BlockRange: span})
				}
				result.Batch.Events = right
			}
			task := generators[index].Next(ctx)
			if len(groups) > 0 && !yield(groups, nil) {
				return
			}
			next, err := sourceAwaitEventSteps(ctx, []<-chan SourceEventStep{task})
			if err != nil {
				fail(err)
				return
			}
			results[index] = next[0]
		}
	}
}
