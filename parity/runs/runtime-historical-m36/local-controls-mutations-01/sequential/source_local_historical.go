package runtime

import (
	"context"
	"fmt"
	"iter"
	"math"
	"math/big"
	"sync"
	"time"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

// SourceLocalHistoricalOptions connects the source-shaped acquisition, progress,
// callback and store APIs. Progress and callback maps retain their identities.
// The iterator exclusively owns Progress and callback/filter/child maps from its
// first Next until completion or stop returns, including time between yields:
// background prefetch and progress publication continue while a yield is consumed.
// Callers may inspect the final shared identities after the iterator has joined.
type SourceLocalHistoricalOptions struct {
	ChainID         uint64
	Requester       rpc.Requester
	Actions         *rpc.Actions
	EventCallbacks  []protocol.Object
	Children        *SourceHistoricalChildren
	Progress        *SourceSyncProgress
	CachedIntervals []store.SourceIntervalEntry
	Store           *store.SourceSyncStore
	From, To        string
	Limit           int
	IsCatchup       bool
	Acquisition     SourceHistoricalOptions
	// Clock and TargetDuration control the same adaptive window estimator as the
	// source. A nil Clock uses time.Now; the default target is production's 10s.
	Clock          func() time.Time
	TargetDuration time.Duration
}

// SourceRawEventBatch is the historical extraction boundary, before decoding
// public handler events. It must not be confused with SourceEventBatch.
type SourceRawEventBatch struct {
	Events     []protocol.RawEvent
	Checkpoint string
	BlockRange [2]float64
}

func sourceLocalFilters(o SourceLocalHistoricalOptions) ([]filter.Object, error) {
	filters := make([]filter.Object, len(o.EventCallbacks))
	for i, callback := range o.EventCallbacks {
		f, ok := callback["filter"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("source historical callback filter required")
		}
		filters[i] = f
	}
	return filters, nil
}
func sourceLocalBounds(o SourceLocalHistoricalOptions) (first, lastNumber float64, last filter.Object, err error) {
	if o.Progress == nil || o.Progress.Start == nil || o.Progress.Finalized == nil {
		err = fmt.Errorf("source historical progress start/finalized required")
		return
	}
	var n uint64
	n, err = sourceHistoricalNumber(o.Progress.Start["number"])
	if err != nil {
		return
	}
	first = float64(n)
	last = o.Progress.Finalized
	if o.Progress.End != nil {
		var end, final uint64
		end, err = sourceHistoricalNumber(o.Progress.End["number"])
		if err != nil {
			return
		}
		final, err = sourceHistoricalNumber(last["number"])
		if err != nil {
			return
		}
		if float64(end) <= float64(final) {
			last = o.Progress.End
		}
	}
	n, err = sourceHistoricalNumber(last["number"])
	lastNumber = float64(n)
	return
}
func sourceLocalFinalizedNumber(o SourceLocalHistoricalOptions) (float64, error) {
	n, e := sourceHistoricalNumber(o.Progress.Finalized["number"])
	return float64(n), e
}
func sourceLocalBlockQuantity(value any) (uint64, error) {
	if n, ok := value.(*big.Int); ok {
		if !n.IsUint64() {
			return 0, fmt.Errorf("source block quantity out of range")
		}
		return n.Uint64(), nil
	}
	return sourceHistoricalNumber(value)
}
func sourceLocalCheckpoint(chain uint64, block filter.Object) (string, error) {
	n, e := sourceLocalBlockQuantity(block["number"])
	if e != nil {
		return "", e
	}
	t, e := sourceLocalBlockQuantity(block["timestamp"])
	if e != nil {
		return "", e
	}
	return checkpoint.Encode(checkpoint.Block(t, chain, n, true))
}
func sourceLocalBuild(o SourceLocalHistoricalOptions, data store.SourceEventData) ([]protocol.RawEvent, error) {
	children := map[string][]filter.ChildAddress{}
	for _, entry := range o.Children.Snapshot() {
		children[entry.ID] = entry.Entries
	}
	return protocol.BuildEvents(protocol.Batch{EventCallbacks: o.EventCallbacks, Blocks: data.Blocks, Transactions: data.Transactions, TransactionReceipts: data.TransactionReceipts, Logs: data.Logs, Traces: data.Traces, ChildAddresses: children, ChainID: o.ChainID})
}

// sourceLocalQueue provides the source callback generator's unbounded progress
// buffer. Cancellation waits for producer cleanup: no transaction outlives Close.
type sourceLocalQueue struct {
	mu     sync.Mutex
	values []float64
	done   bool
	err    error
	wake   chan struct{}
}

func (q *sourceLocalQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *sourceLocalQueue) emit(value float64) {
	q.mu.Lock()
	q.values = append(q.values, value)
	q.mu.Unlock()
	q.signal()
}
func (q *sourceLocalQueue) finish(err error) {
	q.mu.Lock()
	q.done = true
	q.err = err
	q.mu.Unlock()
	q.signal()
}
func (q *sourceLocalQueue) next(ctx context.Context) (float64, error, bool) {
	for {
		q.mu.Lock()
		if err := ctx.Err(); err != nil {
			q.mu.Unlock()
			return 0, err, false
		}
		if len(q.values) > 0 {
			value := q.values[0]
			q.values = q.values[1:]
			q.mu.Unlock()
			return value, nil, true
		}
		if q.done {
			err := q.err
			q.mu.Unlock()
			return 0, err, false
		}
		q.mu.Unlock()
		select {
		case <-q.wake:
		case <-ctx.Done():
			return 0, ctx.Err(), false
		}
	}
}

type sourceLocalPrepared struct {
	span      store.SourceInterval
	required  []store.SourceFilterInterval
	factories []store.SourceFactoryInterval
	data      *sourcePreparedHistoricalRange
	err       error
	started   time.Time
}

func sourceLocalEstimate(span store.SourceInterval, elapsed, target time.Duration, previous float64) float64 {
	ms := float64(elapsed) / float64(time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	density := (span[1] - span[0]) / ms
	// JS Math.round on nonnegative durations, with source's min/maxIncrease caps.
	return math.Min(math.Min(math.Max(25, math.Floor(float64(target)/float64(time.Millisecond)*density+0.5)), math.Floor(previous*1.5+0.5)), 100000)
}

// LocalSourceSync ports getLocalSyncGenerator. Range RPC is prefetched at most
// one interval ahead; persistence of children, blocks and intervals is atomic
// and ordered. Unlike the source's unhandled rejected callback promise, an error
// is delivered to the consumer and cancellation joins in-flight acquisition.
func LocalSourceSync(ctx context.Context, o SourceLocalHistoricalOptions) iter.Seq2[float64, error] {
	return func(yield func(float64, error) bool) {
		fail := func(e error) { yield(0, e) }
		if e := ctx.Err(); e != nil {
			fail(e)
			return
		}
		first, lastNumber, last, e := sourceLocalBounds(o)
		if e != nil {
			fail(e)
			return
		}
		finalized, e := sourceLocalFinalizedNumber(o)
		if e != nil {
			fail(e)
			return
		}
		if first > finalized {
			o.Progress.Current = o.Progress.Finalized
			return
		}
		filters, e := sourceLocalFilters(o)
		if e != nil {
			fail(e)
			return
		}
		if _, e = RequiredSourceIntervalUnion(filters, store.SourceInterval{first, lastNumber}, o.CachedIntervals); e != nil {
			fail(e)
			return
		}
		if o.Progress.Current != nil {
			current, e := sourceHistoricalNumber(o.Progress.Current["number"])
			if e != nil {
				fail(e)
				return
			}
			if !yield(float64(current), nil) {
				return
			}
			if float64(current) == lastNumber {
				return
			}
			first = math.Max(first, float64(current)+1)
		}
		if o.Store == nil {
			fail(fmt.Errorf("source historical sync store required"))
			return
		}
		syncer, e := NewSourceHistoricalSync(o.ChainID, o.Requester, o.Actions, o.Children, o.Acquisition)
		if e != nil {
			fail(e)
			return
		}
		owned, cancel := context.WithCancel(ctx)
		q := &sourceLocalQueue{wake: make(chan struct{}, 1)}
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			q.finish(sourceRunLocalSync(owned, o, filters, syncer, first, lastNumber, last, q.emit))
		}()
		defer func() { cancel(); <-finished }()
		for {
			value, err, ok := q.next(owned)
			if err != nil {
				fail(err)
				return
			}
			if !ok {
				return
			}
			if !yield(value, nil) {
				return
			}
		}
	}
}
func sourceRunLocalSync(ctx context.Context, o SourceLocalHistoricalOptions, filters []filter.Object, syncer *SourceHistoricalSync, first, lastNumber float64, last filter.Object, emit func(float64)) error {
	now := o.Clock
	if now == nil {
		now = time.Now
	}
	target := o.TargetDuration
	if target == 0 {
		target = 10 * time.Second
	}
	owned, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	launch := func(span store.SourceInterval) <-chan sourceLocalPrepared {
		result := make(chan sourceLocalPrepared, 1)
		workers.Add(1)
		go func() {
			defer workers.Done()
			p := sourceLocalPrepared{span: span, started: now()}
			p.required, p.factories, p.err = RequiredSourceIntervals(filters, span, o.CachedIntervals)
			if p.err == nil && (len(p.required) > 0 || len(p.factories) > 0) {
				p.data, p.err = syncer.prepareBlockRangeData(owned, span, p.required, p.factories)
			}
			result <- p
			close(result)
		}()
		return result
	}
	estimate := float64(25)
	pending := launch(store.SourceInterval{first, math.Min(first+estimate, lastNumber)})
	for {
		var p sourceLocalPrepared
		select {
		case p = <-pending:
		case <-owned.Done():
			return owned.Err()
		}
		if p.err != nil {
			return p.err
		}
		if err := owned.Err(); err != nil {
			return err
		}
		isLast := p.span[1] == lastNumber
		// The next RPC acquisition starts before this interval's block work/commit.
		// Its DB writes wait until this interval has successfully committed.
		if p.data != nil {
			err := o.Store.WithTransaction(owned, func(bound *store.SourceSyncStore) error {
				if e := bound.InsertChildAddresses(owned, o.ChainID, p.data.children); e != nil {
					return e
				}
				if _, e := syncer.SyncBlockData(owned, p.span, p.required, p.data.logs, bound); e != nil {
					return e
				}
				return bound.InsertIntervals(owned, o.ChainID, p.required, p.factories)
			})
			if err != nil {
				return err
			}
			estimate = sourceLocalEstimate(p.span, now().Sub(p.started), target, estimate)
		}
		if !isLast {
			pending = launch(store.SourceInterval{math.Min(p.span[1]+1, lastNumber), math.Min(p.span[1]+1+estimate, lastNumber)})
		}
		if isLast {
			o.Progress.Current = last
		}
		if len(p.required) > 0 || isLast {
			emit(p.span[1])
		}
		if isLast {
			return nil
		}
	}
}

// LocalSourceEvents ports the persistent raw event generator. Query pages are
// extracted from real SourceSyncStore rows while synchronization runs ahead.
func LocalSourceEvents(ctx context.Context, o SourceLocalHistoricalOptions) iter.Seq2[*SourceRawEventBatch, error] {
	return func(yield func(*SourceRawEventBatch, error) bool) {
		fail := func(e error) { yield(nil, e) }
		from, e := checkpoint.Decode(o.From)
		if e != nil {
			fail(e)
			return
		}
		to, e := checkpoint.Decode(o.To)
		if e != nil {
			fail(e)
			return
		}
		filters, e := sourceLocalFilters(o)
		if e != nil {
			fail(e)
			return
		}
		if o.Store == nil || o.Children == nil {
			fail(fmt.Errorf("source historical store and children required"))
			return
		}
		cursor := float64(from.BlockNumber)
		toBlock := float64(to.BlockNumber)
		for syncCursor, e := range LocalSourceSync(ctx, o) {
			if e != nil {
				fail(e)
				return
			}
			for cursor <= math.Min(syncCursor, toBlock) {
				data, e := o.Store.GetEventData(ctx, store.SourceEventDataArgs{Filters: filters, FromBlock: cursor, ToBlock: math.Min(syncCursor, toBlock), ChainID: o.ChainID, Limit: o.Limit})
				if e != nil {
					fail(e)
					return
				}
				built, e := sourceLocalBuild(o, data)
				if e != nil {
					fail(e)
					return
				}
				span := [2]float64{cursor, data.Cursor}
				cursor = data.Cursor + 1
				var cp string
				if cursor > toBlock {
					cp = o.To
				} else if len(data.Blocks) > 0 {
					cp, e = sourceLocalCheckpoint(o.ChainID, data.Blocks[len(data.Blocks)-1])
					if e != nil {
						fail(e)
						return
					}
				} else {
					continue
				}
				if !yield(&SourceRawEventBatch{Events: built, Checkpoint: cp, BlockRange: span}, nil) {
					return
				}
			}
		}
	}
}

// LocalSourceInMemoryEvents ports the non-persistent generator, including
// re-fetching the crash-recovery block and retaining final/current object identity.
func LocalSourceInMemoryEvents(ctx context.Context, o SourceLocalHistoricalOptions) iter.Seq2[*SourceRawEventBatch, error] {
	return func(yield func(*SourceRawEventBatch, error) bool) {
		fail := func(e error) { yield(nil, e) }
		if e := ctx.Err(); e != nil {
			fail(e)
			return
		}
		first, lastNumber, last, e := sourceLocalBounds(o)
		if e != nil {
			fail(e)
			return
		}
		finalized, e := sourceLocalFinalizedNumber(o)
		if e != nil {
			fail(e)
			return
		}
		if first > finalized {
			o.Progress.Current = o.Progress.Finalized
			return
		}
		if o.Progress.Current != nil {
			n, e := sourceHistoricalNumber(o.Progress.Current["number"])
			if e != nil {
				fail(e)
				return
			}
			first = math.Max(first, float64(n)+1)
		}
		from, e := checkpoint.Decode(o.From)
		if e != nil {
			fail(e)
			return
		}
		filters, e := sourceLocalFilters(o)
		if e != nil {
			fail(e)
			return
		}
		required, _, e := RequiredSourceIntervals(filters, store.SourceInterval{float64(from.BlockNumber), lastNumber}, o.CachedIntervals)
		if e != nil {
			fail(e)
			return
		}
		_, factories, e := RequiredSourceIntervals(filters, store.SourceInterval{first, lastNumber}, o.CachedIntervals)
		if e != nil {
			fail(e)
			return
		}
		syncer, e := NewSourceInMemoryHistoricalSync(o.Requester, o.Actions, o.Children, o.Acquisition)
		if e != nil {
			fail(e)
			return
		}
		cursor := float64(from.BlockNumber)
		for data, e := range syncer.SyncBlockData(ctx, required, factories) {
			if e != nil {
				fail(e)
				return
			}
			built, e := sourceLocalBuild(o, data)
			if e != nil {
				fail(e)
				return
			}
			if len(data.Blocks) == 0 {
				fail(fmt.Errorf("source memory block data lacks final block"))
				return
			}
			cp, e := sourceLocalCheckpoint(o.ChainID, data.Blocks[len(data.Blocks)-1])
			if e != nil {
				fail(e)
				return
			}
			span := [2]float64{cursor, data.Cursor}
			cursor = data.Cursor + 1
			if !yield(&SourceRawEventBatch{Events: built, Checkpoint: cp, BlockRange: span}, nil) {
				return
			}
		}
		o.Progress.Current = last
		if cursor <= lastNumber {
			yield(&SourceRawEventBatch{Events: []protocol.RawEvent{}, Checkpoint: o.To, BlockRange: [2]float64{cursor, lastNumber}}, nil)
		}
	}
}
