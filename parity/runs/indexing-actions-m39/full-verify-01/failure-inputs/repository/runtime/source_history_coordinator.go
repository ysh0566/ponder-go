package runtime

import (
	"context"
	"fmt"
	"iter"
	"math"
	"reflect"
	"sync"
	"time"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/finality"
	"ponder.local/ponder-go/rpc"
)

// SourceHistoricalChain binds the actual source chain object to its local sync
// state. Objects are retained, not reconstructed from decoded output fixtures.
type SourceHistoricalChain struct {
	Chain            protocol.Object
	Local            SourceLocalHistoricalOptions
	CacheRPCRequests bool
	ReorgWindow      int
	BlockTimeMS      *float64
	// RefreshRequests registers source block actions synchronously before their
	// asynchronous completion. Nil requires an AsyncRequester on Local.Requester.
	RefreshRequests SourceSyncProgressRequester
}
type SourceCrashRecoveryCheckpoint struct {
	ChainID    uint64
	Checkpoint string
}

// SourceHistoricalCoordinatorOptions is the historical extraction boundary,
// not the outer indexing transaction/realtime coordinator. Chains retains the
// perChainSync insertion order. BuildChains optionally retains a distinct
// indexingBuild order for finality refresh and query sizing.
// The iterator owns all chain progress/maps until it completes or stop returns.
// Clock is the source Date.now boundary; nil uses time.Now.
type SourceHistoricalCoordinatorOptions struct {
	Chains        []SourceHistoricalChain
	BuildChains   []SourceHistoricalChain
	CrashRecovery []SourceCrashRecoveryCheckpoint
	QuerySize     int
	Clock         func() time.Time
}

type SourceHistoricalOutput struct {
	Type    string
	Groups  []SourceEventGroup
	Pending []protocol.Object
}

func sourceHistoryNow(o SourceHistoricalCoordinatorOptions) time.Time {
	if o.Clock != nil {
		return o.Clock()
	}
	return time.Now()
}
func sourceHistoryBuild(o SourceHistoricalCoordinatorOptions) []SourceHistoricalChain {
	if o.BuildChains != nil {
		return o.BuildChains
	}
	return o.Chains
}
func sourceHistoryChainID(c SourceHistoricalChain) (uint64, error) {
	return sourceEventChainID(protocol.Object{"chain": c.Chain})
}
func sourceHistoryValidate(o SourceHistoricalCoordinatorOptions) error {
	seen := map[uint64]bool{}
	for _, c := range o.Chains {
		id, e := sourceHistoryChainID(c)
		if e != nil {
			return e
		}
		if seen[id] {
			return fmt.Errorf("duplicate historical chain %d", id)
		}
		seen[id] = true
		if c.Local.ChainID != id || c.Local.Progress == nil || c.Local.Progress.ChainID != id {
			return fmt.Errorf("historical chain/progress identity mismatch")
		}
		if c.Local.Children == nil {
			return fmt.Errorf("historical child-address state required")
		}
	}
	return nil
}
func sourceHistoryTo(p *SourceSyncProgress) (string, error) {
	f, e := p.GetCheckpoint(SourceSyncFinalized)
	if e != nil {
		return "", e
	}
	end, e := p.GetCheckpoint(SourceSyncEnd)
	if e != nil {
		return "", e
	}
	if end != nil && *end < *f {
		return *end, nil
	}
	return *f, nil
}
func sourceHistoryCrash(o SourceHistoricalCoordinatorOptions, id uint64) string {
	for _, c := range o.CrashRecovery {
		if c.ChainID == id {
			return c.Checkpoint
		}
	}
	return ""
}
func sourceHistoryFrom(ctx context.Context, c SourceHistoricalChain, crash string, isolated bool) (string, error) {
	start, e := c.Local.Progress.GetCheckpoint(SourceSyncStart)
	if e != nil {
		return "", e
	}
	if crash == "" {
		return *start, nil
	}
	cp, e := checkpoint.Decode(crash)
	if e != nil {
		return "", e
	}
	id, e := sourceHistoryChainID(c)
	if e != nil {
		return "", e
	}
	if cp.ChainID == id {
		return crash, nil
	}
	if isolated || !c.CacheRPCRequests {
		return *start, nil
	}
	if sourceLocalStoreMissing(c.Local.Store) {
		return "", fmt.Errorf("historical recovery store required")
	}
	b, e := c.Local.Store.GetSafeCrashRecoveryBlock(ctx, id, int64(cp.BlockTimestamp))
	if e != nil {
		return "", e
	}
	if b == nil {
		return *start, nil
	}
	if !b.Number.IsUint64() || !b.Timestamp.IsUint64() {
		return "", fmt.Errorf("invalid historical recovery block")
	}
	return checkpoint.Encode(checkpoint.Checkpoint{BlockTimestamp: b.Timestamp.Uint64(), ChainID: id, BlockNumber: b.Number.Uint64()})
}
func sourceHistoryNextCursor(cursor string) (string, error) {
	cp, e := checkpoint.Decode(cursor)
	if e != nil {
		return "", e
	}
	return checkpoint.Encode(checkpoint.Checkpoint{BlockTimestamp: cp.BlockTimestamp, ChainID: cp.ChainID, BlockNumber: cp.BlockNumber + 1})
}
func sourceHistoryLocal(o SourceHistoricalCoordinatorOptions, c SourceHistoricalChain, from, to string, catchup bool) SourceLocalHistoricalOptions {
	local := c.Local
	local.From = from
	local.To = to
	local.IsCatchup = catchup
	local.Limit = int(math.Floor(float64(o.QuerySize)/float64(len(sourceHistoryBuild(o))+1)+0.5)) + 6
	return local
}
func sourceHistoryDecode(c SourceHistoricalChain, raw []protocol.RawEvent) ([]protocol.Object, error) {
	decoder := protocol.Decoder{}
	return decoder.DecodeEvents(c.Chain, c.Local.EventCallbacks, raw)
}
func sourceHistoryRaw(ctx context.Context, c SourceHistoricalChain, local SourceLocalHistoricalOptions) iter.Seq2[*SourceRawEventBatch, error] {
	if c.CacheRPCRequests {
		return LocalSourceEvents(ctx, local)
	}
	return LocalSourceInMemoryEvents(ctx, local)
}

// sourceHistoryAsync gives a pull iterator exactly one sequential next worker.
// The outer owner cancels its context before closing with pending work. Closing
// cancels scheduling and joins the worker; it does not independently cancel a
// sequence that closes over a different context. All coordinator abort paths
// cancel the shared iterator context before joining their children.
func sourceHistoryAsync(ctx context.Context, seq iter.Seq2[*SourceEventBatch, error]) (SourceEventGenerator, func()) {
	child, cancel := context.WithCancel(ctx)
	requests := make(chan chan SourceEventStep, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		next, stop := iter.Pull2(seq)
		defer stop()
		for {
			select {
			case <-child.Done():
				return
			case out := <-requests:
				if e := child.Err(); e != nil {
					out <- SourceEventStep{Err: e}
					continue
				}
				batch, e, ok := next()
				out <- SourceEventStep{Batch: batch, Err: e, Done: !ok}
			}
		}
	}()
	return SourceEventGenerator{Next: func(call context.Context) <-chan SourceEventStep {
		out := make(chan SourceEventStep, 1)
		select {
		case <-child.Done():
			out <- SourceEventStep{Err: child.Err()}
		case <-call.Done():
			out <- SourceEventStep{Err: call.Err()}
		case requests <- out:
		}
		return out
	}}, func() { cancel(); <-done }
}

// MergeSourceEventsByCompletion ports mergeAsyncGenerators, not the ordered
// event merge. Already-ready ties use current list order; the winner is rotated
// to the tail and its next is started before yielding its current batch.
func MergeSourceEventsByCompletion(ctx context.Context, generators []SourceEventGenerator) iter.Seq2[*SourceEventBatch, error] {
	return func(yield func(*SourceEventBatch, error) bool) {
		gens := append([]SourceEventGenerator{}, generators...)
		pending := make([]<-chan SourceEventStep, len(gens))
		for i, g := range gens {
			if g.Next == nil {
				yield(nil, fmt.Errorf("source generator next required"))
				return
			}
			pending[i] = g.Next(ctx)
		}
		for len(pending) > 0 {
			if e := ctx.Err(); e != nil {
				yield(nil, e)
				return
			}
			index := -1
			var step SourceEventStep
			ok := false
			for i, p := range pending {
				if p == nil {
					yield(nil, fmt.Errorf("source generator future required"))
					return
				}
				select {
				case step, ok = <-p:
					index = i
				default:
				}
				if index >= 0 {
					break
				}
			}
			if index < 0 {
				cases := make([]reflect.SelectCase, len(pending)+1)
				for i, p := range pending {
					cases[i] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(p)}
				}
				cases[len(pending)] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}
				var v reflect.Value
				index, v, ok = reflect.Select(cases)
				if index == len(pending) {
					yield(nil, ctx.Err())
					return
				}
				if ok {
					step = v.Interface().(SourceEventStep)
				}
			}
			if !ok {
				yield(nil, fmt.Errorf("source generator closed without result"))
				return
			}
			if step.Err != nil {
				yield(nil, step.Err)
				return
			}
			g := gens[index]
			gens = append(gens[:index], gens[index+1:]...)
			pending = append(pending[:index], pending[index+1:]...)
			if step.Done {
				continue
			}
			if step.Batch == nil {
				yield(nil, fmt.Errorf("source generator batch required"))
				return
			}
			task := g.Next(ctx)
			gens = append(gens, g)
			pending = append(pending, task)
			if !yield(step.Batch, nil) {
				return
			}
		}
	}
}

func sourceHistoryRefreshDue(o SourceHistoricalCoordinatorOptions, last time.Time) bool {
	for _, c := range sourceHistoryBuild(o) {
		threshold := max(time.Duration(c.ReorgWindow)*time.Second, 30*time.Second)
		if sourceHistoryNow(o).Sub(last) >= threshold {
			return true
		}
	}
	return false
}
func sourceHistoryRequests(c SourceHistoricalChain) (SourceSyncProgressRequester, error) {
	if c.RefreshRequests != nil {
		return c.RefreshRequests, nil
	}
	requester, ok := c.Local.Requester.(rpc.AsyncRequester)
	if !ok {
		return nil, fmt.Errorf("historical refresh requires ordered async block admission")
	}
	return NewSourceSyncProgressRPC(requester), nil
}
func sourceHistoryAwaitBlock(ctx context.Context, task <-chan SourceSyncProgressBlockResult) (filter.Object, error) {
	if task == nil {
		return nil, fmt.Errorf("historical block future required")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result, ok := <-task:
		if !ok {
			return nil, fmt.Errorf("historical block future closed without result")
		}
		return result.Block, result.Err
	}
}
func sourceHistoryFinalized(ctx context.Context, c SourceHistoricalChain, requests SourceSyncProgressRequester, latest filter.Object) (filter.Object, error) {
	lower := c.Local.Progress.Finalized
	objects := map[*finality.Block]filter.Object{}
	convert := func(o filter.Object) (*finality.Block, error) {
		if o == nil {
			return nil, nil
		}
		n, e := sourceHistoricalNumber(o["number"])
		if e != nil {
			return nil, e
		}
		ts, e := sourceHistoricalNumber(o["timestamp"])
		if e != nil {
			return nil, e
		}
		b := &finality.Block{Number: fmt.Sprintf("0x%x", n), Timestamp: fmt.Sprintf("0x%x", ts)}
		objects[b] = o
		return b, nil
	}
	last, e := convert(latest)
	if e != nil {
		return nil, e
	}
	low, e := convert(lower)
	if e != nil {
		return nil, e
	}
	result, e := finality.GetFinalizedBlock(ctx, finality.Chain{ReorgWindow: c.ReorgWindow, BlockTimeMS: c.BlockTimeMS}, func(ctx context.Context, r finality.Request) (*finality.Block, error) {
		o, e := sourceHistoryAwaitBlock(ctx, requests.BeginBlockByNumber(ctx, fmt.Sprintf("0x%x", r.Number), false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: r.RetryNullBlockRequest}))
		if e != nil {
			return nil, e
		}
		return convert(o)
	}, last, low)
	if e != nil {
		return nil, e
	}
	return objects[result], nil
}

func sourceHistoryRefresh(ctx context.Context, o SourceHistoricalCoordinatorOptions, mode string) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chains := sourceHistoryBuild(o)
	blocks := make([]filter.Object, len(chains))
	requests := make([]SourceSyncProgressRequester, len(chains))
	pending := make([]<-chan SourceSyncProgressBlockResult, len(chains))
	// Promise.all(map(...)): all initial actions register synchronously in build
	// order; their network completions and subsequent finality lookups overlap.
	for i, c := range chains {
		var e error
		requests[i], e = sourceHistoryRequests(c)
		if e != nil {
			return false, e
		}
	}
	for i := range chains {
		pending[i] = requests[i].BeginBlockByNumber(ctx, "latest", false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: mode != "isolated"})
	}
	type completion struct {
		index int
		block filter.Object
		err   error
	}
	completed := make(chan completion, len(chains))
	var wg sync.WaitGroup
	for i, c := range chains {
		wg.Add(1)
		go func(i int, c SourceHistoricalChain) {
			defer wg.Done()
			latest, e := sourceHistoryAwaitBlock(ctx, pending[i])
			var block filter.Object
			if e == nil {
				block, e = sourceHistoryFinalized(ctx, c, requests[i], latest)
			}
			completed <- completion{i, block, e}
		}(i, c)
	}
	// Promise.all rejects on the first observed rejection, not the first
	// chain in build order. Cancel pending waits and join our workers before
	// returning; successful progress is applied only after every child succeeds.
	for range chains {
		result := <-completed
		if result.err != nil {
			cancel()
			wg.Wait()
			return false, result.err
		}
		blocks[result.index] = result.block
	}
	wg.Wait()
	for i, c := range chains {
		old, e := sourceHistoricalNumber(c.Local.Progress.Finalized["number"])
		if e != nil {
			return false, e
		}
		n, e := sourceHistoricalNumber(blocks[i]["number"])
		if e != nil {
			return false, e
		}
		if float64(n) < float64(old) {
			switch mode {
			case "omnichain":
				return false, fmt.Errorf("Finalized block for chain %q cannot move backwards", c.Chain["name"])
			case "isolated":
				return false, nil
			default:
				continue
			}
		}
		c.Local.Progress.Finalized = blocks[i]
	}
	return true, nil
}

func HistoricalSourceMultichain(ctx context.Context, o SourceHistoricalCoordinatorOptions) iter.Seq2[SourceEventGroup, error] {
	return func(yield func(SourceEventGroup, error) bool) {
		fail := func(e error) { yield(SourceEventGroup{}, e) }
		if e := sourceHistoryValidate(o); e != nil {
			fail(e)
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		last := sourceHistoryNow(o)
		catchup := false
		cursors := make([]string, len(o.Chains))
		for {
			gens := make([]SourceEventGenerator, len(o.Chains))
			stops := make([]func(), len(gens))
			for i, c := range o.Chains {
				seq := func(emit func(*SourceEventBatch, error) bool) {
					to, e := sourceHistoryTo(c.Local.Progress)
					if e != nil {
						emit(nil, e)
						return
					}
					crash := sourceHistoryCrash(o, c.Local.ChainID)
					var from string
					if catchup {
						from, e = sourceHistoryNextCursor(cursors[i])
						if e == nil && from > to {
							return
						}
					} else {
						from, e = sourceHistoryFrom(ctx, c, crash, false)
					}
					if e != nil {
						emit(nil, e)
						return
					}
					local := sourceHistoryLocal(o, c, from, to, catchup)
					for b, e := range sourceHistoryRaw(ctx, c, local) {
						if e != nil {
							emit(nil, e)
							return
						}
						events, e := sourceHistoryDecode(c, b.Events)
						if e != nil {
							emit(nil, e)
							return
						}
						if crash != "" {
							_, events, e = sourcePartitionEvents(events, crash)
							if e != nil {
								emit(nil, e)
								return
							}
						}
						if !emit(&SourceEventBatch{Events: events, Checkpoint: b.Checkpoint, BlockRange: b.BlockRange}, nil) {
							return
						}
					}
					cursors[i] = to
				}
				gens[i], stops[i] = sourceHistoryAsync(ctx, seq)
			}
			stopAll := func() {
				for _, s := range stops {
					s()
				}
			}
			stopped := false
			for b, e := range MergeSourceEventsByCompletion(ctx, gens) {
				if e != nil {
					cancel()
					stopAll()
					fail(e)
					return
				}
				cp, e := checkpoint.Decode(b.Checkpoint)
				if e != nil {
					cancel()
					stopAll()
					fail(e)
					return
				}
				if !yield(SourceEventGroup{ChainID: cp.ChainID, Events: b.Events, Checkpoint: b.Checkpoint, BlockRange: b.BlockRange}, nil) {
					cancel()
					stopAll()
					stopped = true
					break
				}
			}
			if stopped {
				return
			}
			stopAll()
			if !sourceHistoryRefreshDue(o, last) {
				return
			}
			last = sourceHistoryNow(o)
			if _, e := sourceHistoryRefresh(ctx, o, "multichain"); e != nil {
				fail(e)
				return
			}
			catchup = true
		}
	}
}

func HistoricalSourceIsolated(ctx context.Context, o SourceHistoricalCoordinatorOptions, chainID uint64) iter.Seq2[SourceEventGroup, error] {
	return func(yield func(SourceEventGroup, error) bool) {
		fail := func(e error) { yield(SourceEventGroup{}, e) }
		if e := sourceHistoryValidate(o); e != nil {
			fail(e)
			return
		}
		var chain *SourceHistoricalChain
		for i := range o.Chains {
			if o.Chains[i].Local.ChainID == chainID {
				chain = &o.Chains[i]
				break
			}
		}
		if chain == nil {
			fail(fmt.Errorf("isolated chain not found"))
			return
		}
		c := *chain
		last := sourceHistoryNow(o)
		catchup := false
		cursor := ""
		crash := sourceHistoryCrash(o, chainID)
		for {
			to, e := sourceHistoryTo(c.Local.Progress)
			if e != nil {
				fail(e)
				return
			}
			var from string
			if catchup {
				from, e = sourceHistoryNextCursor(cursor)
				if e == nil && from > to {
					return
				}
			} else {
				from, e = sourceHistoryFrom(ctx, c, crash, true)
			}
			if e != nil {
				fail(e)
				return
			}
			local := sourceHistoryLocal(o, c, from, to, catchup)
			for b, e := range sourceHistoryRaw(ctx, c, local) {
				if e != nil {
					fail(e)
					return
				}
				events, e := sourceHistoryDecode(c, b.Events)
				if e != nil {
					fail(e)
					return
				}
				if crash != "" {
					_, events, e = sourcePartitionEvents(events, crash)
					if e != nil {
						fail(e)
						return
					}
				}
				if !yield(SourceEventGroup{ChainID: chainID, Events: events, Checkpoint: b.Checkpoint, BlockRange: b.BlockRange}, nil) {
					return
				}
			}
			cursor = to
			if !sourceHistoryRefreshDue(o, last) {
				return
			}
			last = sourceHistoryNow(o)
			refresh := o
			refresh.Chains = []SourceHistoricalChain{c}
			refresh.BuildChains = refresh.Chains
			ok, e := sourceHistoryRefresh(ctx, refresh, "isolated")
			if e != nil {
				fail(e)
				return
			}
			if !ok {
				return
			}
			catchup = true
		}
	}
}

func HistoricalSourceOmnichain(ctx context.Context, o SourceHistoricalCoordinatorOptions) iter.Seq2[SourceHistoricalOutput, error] {
	return func(yield func(SourceHistoricalOutput, error) bool) {
		fail := func(e error) { yield(SourceHistoricalOutput{}, e) }
		if e := sourceHistoryValidate(o); e != nil {
			fail(e)
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		last := sourceHistoryNow(o)
		catchup := false
		cursors := make([]string, len(o.Chains))
		pending := []protocol.Object{}
		var pendingMu sync.Mutex
		for {
			progress := make([]*SourceSyncProgress, len(o.Chains))
			for i, c := range o.Chains {
				progress[i] = c.Local.Progress
			}
			final, e := GetSourceOmnichainCheckpoint(progress, SourceSyncFinalized)
			if e != nil {
				fail(e)
				return
			}
			end, e := GetSourceOmnichainCheckpoint(progress, SourceSyncEnd)
			if e != nil {
				fail(e)
				return
			}
			omniTo := *final
			if end != nil && *end < omniTo {
				omniTo = *end
			}
			gens := make([]SourceEventGenerator, len(o.Chains))
			stops := make([]func(), len(gens))
			for i, c := range o.Chains {
				seq := func(emit func(*SourceEventBatch, error) bool) {
					to, e := sourceHistoryTo(c.Local.Progress)
					if e != nil {
						emit(nil, e)
						return
					}
					crash := sourceHistoryCrash(o, c.Local.ChainID)
					var from string
					if catchup {
						cursor := cursors[i]
						pendingMu.Lock()
						events := []protocol.Object{}
						rest := []protocol.Object{}
						for _, ev := range pending {
							id, e := sourceEventChainID(ev)
							if e != nil {
								pendingMu.Unlock()
								emit(nil, e)
								return
							}
							cp, e := sourceEventCheckpoint(ev)
							if e != nil {
								pendingMu.Unlock()
								emit(nil, e)
								return
							}
							if id == c.Local.ChainID && cp <= omniTo {
								events = append(events, ev)
							} else {
								rest = append(rest, ev)
							}
						}
						pending = rest
						pendingMu.Unlock()
						if len(events) > 0 {
							cp := cursor
							if omniTo < cursor {
								cp, _ = sourceEventCheckpoint(events[len(events)-1])
							}
							first, _ := sourceEventCheckpoint(events[0])
							a, e := checkpoint.Decode(first)
							if e != nil {
								emit(nil, e)
								return
							}
							b, e := checkpoint.Decode(cp)
							if e != nil {
								emit(nil, e)
								return
							}
							if !emit(&SourceEventBatch{Events: events, Checkpoint: cp, BlockRange: [2]float64{float64(a.BlockNumber), float64(b.BlockNumber)}}, nil) {
								return
							}
						}
						from, e = sourceHistoryNextCursor(cursor)
						if e == nil && from > to {
							return
						}
					} else {
						from, e = sourceHistoryFrom(ctx, c, crash, false)
					}
					if e != nil {
						emit(nil, e)
						return
					}
					local := sourceHistoryLocal(o, c, from, to, catchup)
					for b, e := range sourceHistoryRaw(ctx, c, local) {
						if e != nil {
							emit(nil, e)
							return
						}
						events, e := sourceHistoryDecode(c, b.Events)
						if e != nil {
							emit(nil, e)
							return
						}
						if crash != "" {
							_, events, e = sourcePartitionEvents(events, crash)
							if e != nil {
								emit(nil, e)
								return
							}
						}
						cp := b.Checkpoint
						span := b.BlockRange
						if cp > omniTo {
							left, right, e := sourcePartitionEvents(events, omniTo)
							if e != nil {
								emit(nil, e)
								return
							}
							pendingMu.Lock()
							pending = append(pending, right...)
							pendingMu.Unlock()
							events = left
							if len(left) == 0 {
								continue
							}
							cp, _ = sourceEventCheckpoint(left[len(left)-1])
							p, e := checkpoint.Decode(cp)
							if e != nil {
								emit(nil, e)
								return
							}
							span[1] = float64(p.BlockNumber)
						}
						if !emit(&SourceEventBatch{Events: events, Checkpoint: cp, BlockRange: span}, nil) {
							return
						}
					}
					cursors[i] = to
				}
				gens[i], stops[i] = sourceHistoryAsync(ctx, seq)
			}
			stopAll := func() {
				for _, s := range stops {
					s()
				}
			}
			for groups, e := range MergeSourceEventsWithEventOrder(ctx, gens) {
				if e != nil {
					cancel()
					stopAll()
					fail(e)
					return
				}
				if !yield(SourceHistoricalOutput{Type: "events", Groups: groups}, nil) {
					cancel()
					stopAll()
					return
				}
			}
			stopAll()
			if !sourceHistoryRefreshDue(o, last) {
				break
			}
			last = sourceHistoryNow(o)
			if _, e := sourceHistoryRefresh(ctx, o, "omnichain"); e != nil {
				fail(e)
				return
			}
			catchup = true
		}
		yield(SourceHistoricalOutput{Type: "pending", Pending: pending}, nil)
	}
}
