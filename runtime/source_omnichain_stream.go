package runtime

import (
	"context"
	"fmt"
	"io"
	"iter"
	"slices"
	"strings"
	"sync"

	"github.com/ysh0566/ponder-go/checkpoint"
	protocol "github.com/ysh0566/ponder-go/events"
)

// SourceOmnichainUpdate carries checkpoint-ordered Events for both historical
// ("events") and live ("block") deliveries. Historical Groups also describe
// each chain's progress in the batch. Control updates carry the global
// checkpoint to finalize or revert to. A
// block delivery can contain pending events from other chains. Source is the
// triggering live event, if any, including its acknowledgement and fork data.
type SourceOmnichainUpdate struct {
	Type       string
	Groups     []SourceEventGroup
	Events     []protocol.Object
	Checkpoint string
	ChainID    uint64
	Source     *SourceRealtimeEvent
}

// SourceRealtimeFeed supplies reconciled events for one chain beginning at its
// historical finalized block. A finite feed ends after its configured end block.
type SourceRealtimeFeed func(context.Context, SourceHistoricalChain) iter.Seq2[SourceRealtimeEvent, error]

// StreamSourceOmnichain runs historical extraction and hands its pending
// events to the live streams. With a nil feed it polls the configured RPCs.
// A custom feed supplies reconciled events and must honor cancellation. The
// consumer owns event application, acknowledgements and durable recovery.
// Event maps are read-only and may be delivered again on replay. Breaking
// from the iterator cancels and joins all live inputs.
func StreamSourceOmnichain(ctx context.Context, history SourceHistoricalCoordinatorOptions, open SourceRealtimeFeed) iter.Seq2[SourceOmnichainUpdate, error] {
	if open == nil {
		open = pollOmnichainSource
	}
	return func(yield func(SourceOmnichainUpdate, error) bool) {
		var pending []protocol.Object
		for output, err := range HistoricalSourceOmnichain(ctx, history) {
			if err != nil {
				yield(SourceOmnichainUpdate{}, err)
				return
			}
			if output.Type == "pending" {
				pending = output.Pending
				continue
			}
			// Historical groups retain build order. A group can span several
			// peer blocks, so callers apply the flat, globally ordered events.
			var ordered []omnichainEvent
			for _, group := range output.Groups {
				ordered, err = appendOmnichainEvents(ordered, group.Events)
				if err != nil {
					yield(SourceOmnichainUpdate{}, err)
					return
				}
			}
			orderOmnichainEvents(ordered)
			events := make([]protocol.Object, len(ordered))
			for i, item := range ordered {
				events[i] = item.event
			}
			if !yield(SourceOmnichainUpdate{Type: "events", Groups: output.Groups, Events: events}, nil) {
				return
			}
		}
		streamOmnichainLive(ctx, history.Chains, pending, open, yield)
	}
}

type omnichainEvent struct {
	event protocol.Object
	key   string
	point checkpoint.Checkpoint
}

func appendOmnichainEvents(to []omnichainEvent, events []protocol.Object) ([]omnichainEvent, error) {
	for _, event := range events {
		key, err := sourceEventCheckpoint(event)
		if err != nil {
			return nil, err
		}
		point, err := checkpoint.Decode(key)
		if err != nil {
			return nil, err
		}
		id, err := sourceEventChainID(event)
		if err != nil || point.ChainID != id {
			return nil, fmt.Errorf("omnichain event chain differs from checkpoint %s", key)
		}
		to = append(to, omnichainEvent{event, key, point})
	}
	return to, nil
}

type omnichainBuffer struct {
	pending, executed []omnichainEvent
	safe              string
}

func orderOmnichainEvents(events []omnichainEvent) {
	slices.SortStableFunc(events, func(a, b omnichainEvent) int { return strings.Compare(a.key, b.key) })
}

func (b *omnichainBuffer) release(key string, decoded []protocol.Object) ([]protocol.Object, error) {
	// Only sort the events released by this block. A fast chain's pending
	// events can span many live blocks on a slower chain.
	n := 0
	for n < len(b.pending) && b.pending[n].key <= key {
		n++
	}
	ready := slices.Clone(b.pending[:n])
	clear(b.pending[:n])
	b.pending = b.pending[n:]
	ready, err := appendOmnichainEvents(ready, decoded)
	if err != nil {
		return nil, err
	}
	orderOmnichainEvents(ready)
	out := make([]protocol.Object, 0, len(ready))
	for _, item := range ready {
		out = append(out, item.event)
		if item.key > b.safe {
			b.executed = append(b.executed, item)
		}
	}
	return out, nil
}

func (b *omnichainBuffer) revert(key string, chain, ancestor uint64) {
	b.executed = slices.DeleteFunc(b.executed, func(item omnichainEvent) bool {
		if item.key <= key {
			return false
		}
		b.pending = append(b.pending, item)
		return true
	})
	b.pending = slices.DeleteFunc(b.pending, func(item omnichainEvent) bool {
		return item.point.ChainID == chain && item.point.BlockNumber > ancestor
	})
	orderOmnichainEvents(b.pending)
}

type omnichainInput struct {
	index int
	event SourceRealtimeEvent
	key   string
}

func streamOmnichainLive(ctx context.Context, chains []SourceHistoricalChain, pending []protocol.Object, open SourceRealtimeFeed, yield func(SourceOmnichainUpdate, error) bool) {
	fail := func(err error) { yield(SourceOmnichainUpdate{}, err) }
	progress := make([]*SourceSyncProgress, len(chains))
	for i, chain := range chains {
		p := *chain.Local.Progress
		progress[i] = &p
	}
	safe, err := GetSourceOmnichainCheckpoint(progress, SourceSyncFinalized)
	if err != nil {
		fail(err)
		return
	}
	buffer := omnichainBuffer{safe: *safe}
	buffer.pending, err = appendOmnichainEvents(nil, pending)
	if err != nil {
		fail(err)
		return
	}
	orderOmnichainEvents(buffer.pending)
	for input, err := range mergeOmnichainInputs(ctx, chains, progress, open) {
		if err != nil {
			fail(err)
			return
		}
		update, emit, err := buffer.apply(chains[input.index], progress, input)
		if err != nil {
			fail(err)
			return
		}
		if emit && !yield(update, nil) {
			return
		}
	}
	// All ranges ended. A completed chain can have historical events past
	// the timestamp of the last live source. Emit those in block order.
	for len(buffer.pending) > 0 {
		point := buffer.pending[0].point
		key := checkpoint.MustEncode(checkpoint.Block(point.BlockTimestamp, point.ChainID, point.BlockNumber, true))
		events, err := buffer.release(key, nil)
		if err != nil {
			fail(err)
			return
		}
		if !yield(SourceOmnichainUpdate{Type: "block", Events: events, Checkpoint: key, ChainID: point.ChainID}, nil) {
			return
		}
	}
}

type omnichainLiveChain struct {
	next func() (SourceRealtimeEvent, error, bool)
	stop func()
	head *omnichainInput
}

func (s *omnichainLiveChain) read(ctx context.Context, index int, p *SourceSyncProgress) error {
	event, err, ok := s.next()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ok {
		s.head = nil
		end, err := p.IsEnd()
		if err == nil && !end {
			err = fmt.Errorf("live stream ended before chain %d completed: %w", p.ChainID, io.ErrUnexpectedEOF)
		}
		return err
	}
	key, err := sourceLocalCheckpoint(p.ChainID, event.Block)
	if err == nil {
		s.head = &omnichainInput{index: index, event: event, key: key}
	}
	return err
}

func mergeOmnichainInputs(ctx context.Context, chains []SourceHistoricalChain, progress []*SourceSyncProgress, open SourceRealtimeFeed) iter.Seq2[*omnichainInput, error] {
	return func(yield func(*omnichainInput, error) bool) {
		run, cancel := context.WithCancel(ctx)
		streams := make([]omnichainLiveChain, len(chains))
		var opening sync.WaitGroup
		defer func() {
			cancel()
			opening.Wait()
			for _, s := range streams {
				if s.stop != nil {
					s.stop()
				}
			}
		}()

		// Admit every first read before waiting. A provider error cancels
		// other chains even when they are still waiting for their first head.
		results := make(chan error, len(chains))
		active := 0
		for i, chain := range chains {
			end, err := progress[i].IsEnd()
			if err != nil {
				yield(nil, err)
				return
			}
			if end {
				continue
			}
			active++
			opening.Add(1)
			go func() {
				defer opening.Done()
				feed := open(run, chain)
				if feed == nil {
					results <- fmt.Errorf("nil live stream for chain %d", chain.Local.ChainID)
					return
				}
				s := &streams[i]
				s.next, s.stop = iter.Pull2(feed)
				results <- s.read(run, i, progress[i])
			}()
		}
		for range active {
			select {
			case err := <-results:
				if err != nil {
					yield(nil, err)
					return
				}
			case <-run.Done():
				yield(nil, run.Err())
				return
			}
		}

		for {
			if err := run.Err(); err != nil {
				yield(nil, err)
				return
			}
			var first *omnichainInput
			for _, stream := range streams {
				if head := stream.head; head != nil {
					if head.event.Type != "block" {
						first = head
						break
					}
					if first == nil || head.key < first.key {
						first = head
					}
				}
			}
			if first == nil || !yield(first, nil) {
				return
			}
			// Each source is parked at yield during decoding, including its
			// branch-specific factory child map. Resume only the chosen input.
			i := first.index
			if err := streams[i].read(run, i, progress[i]); err != nil {
				yield(nil, err)
				return
			}
		}
	}
}

func (b *omnichainBuffer) apply(chain SourceHistoricalChain, progress []*SourceSyncProgress, input *omnichainInput) (SourceOmnichainUpdate, bool, error) {
	e := &input.event
	p := progress[input.index]
	out := SourceOmnichainUpdate{Type: e.Type, ChainID: p.ChainID, Source: e, Checkpoint: input.key}
	switch e.Type {
	case "block":
		decoded, err := DecodeSourceRealtimeEvent(chain.Chain, chain.Local.EventCallbacks, chain.Local.FilterSession, chain.Local.Children, *e)
		if err != nil {
			return out, false, err
		}
		p.Current = e.Block
		out.Events, err = b.release(input.key, decoded)
		if err != nil {
			return out, false, err
		}
	case "finalize":
		p.Finalized = e.Block
		key, err := GetSourceOmnichainCheckpoint(progress, SourceSyncFinalized)
		if err != nil || *key <= b.safe {
			return out, false, err
		}
		b.safe, out.Checkpoint = *key, *key
		b.executed = slices.DeleteFunc(b.executed, func(item omnichainEvent) bool { return item.key <= b.safe })
	case "reorg":
		p.Current = e.Block
		key, err := GetSourceOmnichainCheckpoint(progress, SourceSyncCurrent)
		if err != nil {
			return out, false, err
		}
		if *key < b.safe {
			return out, false, ErrManagedRealtimeFinality
		}
		out.Checkpoint = *key
		number, err := sourceHistoricalNumber(e.Block["number"])
		if err != nil {
			return out, false, err
		}
		b.revert(*key, p.ChainID, number)
	default:
		return out, false, fmt.Errorf("unknown realtime event %q", e.Type)
	}
	return out, true, nil
}
