package runtime

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
)

func TestSourceHistoryCompletionMerge(t *testing.T) {
	t.Run("ready rotation and next before yield", func(t *testing.T) {
		calls := []string{}
		makeGen := func(name string) SourceEventGenerator {
			i := 0
			return SourceEventGenerator{Next: func(context.Context) <-chan SourceEventStep {
				calls = append(calls, fmt.Sprintf("%s%d", name, i))
				c := make(chan SourceEventStep, 1)
				if i == 2 {
					c <- SourceEventStep{Done: true}
				} else {
					c <- SourceEventStep{Batch: &SourceEventBatch{Checkpoint: fmt.Sprintf("%s%d", name, i)}}
				}
				i++
				return c
			}}
		}
		a, b := makeGen("a"), makeGen("b")
		got := []string{}
		for v, e := range MergeSourceEventsByCompletion(context.Background(), []SourceEventGenerator{a, b}) {
			if e != nil {
				t.Fatal(e)
			}
			got = append(got, v.Checkpoint)
			if len(got) == 1 && !reflect.DeepEqual(calls, []string{"a0", "b0", "a1"}) {
				t.Fatalf("next must start before yield: %v", calls)
			}
		}
		if !reflect.DeepEqual(got, []string{"a0", "b0", "a1", "b1"}) {
			t.Fatal(got)
		}
	})
	t.Run("completion not checkpoint order", func(t *testing.T) {
		first := make(chan SourceEventStep, 1)
		second := make(chan SourceEventStep, 1)
		second <- SourceEventStep{Batch: &SourceEventBatch{Checkpoint: "later"}}
		n := []int{0, 0}
		gen := func(i int, c chan SourceEventStep) SourceEventGenerator {
			return SourceEventGenerator{Next: func(context.Context) <-chan SourceEventStep {
				n[i]++
				if n[i] == 1 {
					return c
				}
				out := make(chan SourceEventStep, 1)
				out <- SourceEventStep{Done: true}
				return out
			}}
		}
		got := []string{}
		for v, e := range MergeSourceEventsByCompletion(context.Background(), []SourceEventGenerator{gen(0, first), gen(1, second)}) {
			if e != nil {
				t.Fatal(e)
			}
			got = append(got, v.Checkpoint)
			if len(got) == 1 {
				first <- SourceEventStep{Batch: &SourceEventBatch{Checkpoint: "earlier"}}
			}
		}
		if !reflect.DeepEqual(got, []string{"later", "earlier"}) {
			t.Fatal(got)
		}
	})
	t.Run("child error exact", func(t *testing.T) {
		want := errors.New("failed child")
		g := SourceEventGenerator{Next: func(context.Context) <-chan SourceEventStep {
			c := make(chan SourceEventStep, 1)
			c <- SourceEventStep{Err: want}
			return c
		}}
		count := 0
		for _, e := range MergeSourceEventsByCompletion(context.Background(), []SourceEventGenerator{g}) {
			count++
			if e != want {
				t.Fatal(e)
			}
		}
		if count != 1 {
			t.Fatal(count)
		}
	})
	t.Run("cancel waits are interruptible", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		g := SourceEventGenerator{Next: func(context.Context) <-chan SourceEventStep { cancel(); return make(chan SourceEventStep) }}
		for _, e := range MergeSourceEventsByCompletion(ctx, []SourceEventGenerator{g}) {
			if !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
		}
	})
	t.Run("closed future is not done", func(t *testing.T) {
		g := SourceEventGenerator{Next: func(context.Context) <-chan SourceEventStep { c := make(chan SourceEventStep); close(c); return c }}
		for _, e := range MergeSourceEventsByCompletion(context.Background(), []SourceEventGenerator{g}) {
			if e == nil {
				t.Fatal("closed future accepted")
			}
		}
	})
}

func TestSourceHistoryAsyncTermination(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	closed := make(chan struct{})
	seq := func(yield func(*SourceEventBatch, error) bool) {
		defer close(closed)
		if !yield(&SourceEventBatch{Checkpoint: "first"}, nil) {
			return
		}
		<-ctx.Done()
		yield(nil, ctx.Err())
	}
	g, stop := sourceHistoryAsync(ctx, seq)
	first := <-g.Next(ctx)
	if first.Batch == nil {
		t.Fatal(first)
	}
	_ = g.Next(ctx)
	cancel()
	stop()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("child not joined")
	}
}

type sourceHistoryControlRequester struct {
	mu     sync.Mutex
	calls  []string
	blocks map[string]filter.Object
	err    error
}

func (r *sourceHistoryControlRequester) Call(ctx context.Context, method string, params any, out any) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := params.([]any)
	number := p[0].(string)
	r.calls = append(r.calls, number)
	if r.err != nil {
		return r.err
	}
	*(out.(*filter.Object)) = r.blocks[number]
	return nil
}

// Immediate recorded-control admission is synchronous; no goroutine substitutes
// for binding the request/output before Begin returns.
func (r *sourceHistoryControlRequester) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	done := make(chan error, 1)
	done <- r.Call(ctx, method, params, out)
	return done
}

var _ rpc.Requester = (*sourceHistoryControlRequester)(nil)

func sourceHistoryControlChain(id uint64, current, latest uint64) SourceHistoricalChain {
	block := func(n uint64) filter.Object {
		return filter.Object{"number": fmt.Sprintf("0x%x", n), "timestamp": fmt.Sprintf("0x%x", n+100), "hash": "0x01", "parentHash": "0x00", "logsBloom": "0x00"}
	}
	p := &SourceSyncProgress{ChainID: id, Start: block(0), Finalized: block(current)}
	r := &sourceHistoryControlRequester{blocks: map[string]filter.Object{"latest": block(latest)}}
	return SourceHistoricalChain{Chain: protocol.Object{"id": id, "name": fmt.Sprintf("chain%d", id)}, Local: SourceLocalHistoricalOptions{ChainID: id, Progress: p, Requester: r, Children: NewSourceHistoricalChildren(nil)}, CacheRPCRequests: true}
}
func TestSourceHistoryFinalizedModeBoundaries(t *testing.T) {
	for _, mode := range []string{"omnichain", "multichain", "isolated"} {
		t.Run(mode, func(t *testing.T) {
			c := sourceHistoryControlChain(1, 10, 9)
			old := c.Local.Progress.Finalized
			ok, e := sourceHistoryRefresh(context.Background(), SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{c}}, mode)
			switch mode {
			case "omnichain":
				if e == nil || ok {
					t.Fatal("backwards accepted")
				}
			case "multichain":
				if e != nil || !ok {
					t.Fatal(e)
				}
			case "isolated":
				if e != nil || ok {
					t.Fatal(e)
				}
			}
			if reflect.ValueOf(old).Pointer() != reflect.ValueOf(c.Local.Progress.Finalized).Pointer() {
				t.Fatal("backwards replaced original")
			}
		})
	}
	t.Run("forward preserves actual block object", func(t *testing.T) {
		c := sourceHistoryControlChain(1, 10, 11)
		r := c.Local.Requester.(*sourceHistoryControlRequester)
		want := r.blocks["latest"]
		ok, e := sourceHistoryRefresh(context.Background(), SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{c}}, "multichain")
		if e != nil || !ok {
			t.Fatal(e)
		}
		if reflect.ValueOf(want).Pointer() != reflect.ValueOf(c.Local.Progress.Finalized).Pointer() {
			t.Fatal("finalized identity lost")
		}
	})
}
func TestSourceHistoryCursorAndRefreshThreshold(t *testing.T) {
	cursor := checkpoint.MustEncode(checkpoint.Block(25, 4, 20, true))
	got, e := sourceHistoryNextCursor(cursor)
	if e != nil {
		t.Fatal(e)
	}
	want := checkpoint.MustEncode(checkpoint.Checkpoint{BlockTimestamp: 25, ChainID: 4, BlockNumber: 21})
	if got != want {
		t.Fatal(got)
	}
	now := time.Unix(1000, 0)
	o := SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{{ReorgWindow: 60}, {ReorgWindow: 1}}, Clock: func() time.Time { return now }}
	if sourceHistoryRefreshDue(o, now.Add(-29*time.Second)) {
		t.Fatal("early refresh")
	}
	if !sourceHistoryRefreshDue(o, now.Add(-30*time.Second)) {
		t.Fatal("any eligible chain required")
	}
}

// Ensure APIs are genuine pullable iterators, not precomputed expected results.
var _ iter.Seq2[SourceEventGroup, error] = HistoricalSourceMultichain(context.Background(), SourceHistoricalCoordinatorOptions{})

func TestSourceHistoryRefreshAdmission(t *testing.T) {
	t.Run("ordered begin reversed completion", func(t *testing.T) {
		var mu sync.Mutex
		started := []uint64{}
		future := []chan SourceSyncProgressBlockResult{make(chan SourceSyncProgressBlockResult, 1), make(chan SourceSyncProgressBlockResult, 1)}
		registered := make(chan struct{}, 2)
		chains := []SourceHistoricalChain{sourceHistoryControlChain(1, 4, 5), sourceHistoryControlChain(2, 4, 5)}
		for i := range chains {
			index := i
			chains[i].RefreshRequests = sourceProgressBeginFunc(func(ctx context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
				if number != "latest" || transactions || !options.RetryNullBlockRequest {
					t.Errorf("wrong latest context %s %v %+v", number, transactions, options)
				}
				mu.Lock()
				started = append(started, uint64(index+1))
				mu.Unlock()
				registered <- struct{}{}
				return future[index]
			})
		}
		result := make(chan error, 1)
		go func() {
			_, e := sourceHistoryRefresh(context.Background(), SourceHistoricalCoordinatorOptions{Chains: chains}, "multichain")
			result <- e
		}()
		<-registered
		<-registered
		mu.Lock()
		if !reflect.DeepEqual(started, []uint64{1, 2}) {
			t.Errorf("admission order %v", started)
		}
		mu.Unlock()
		b := func() filter.Object { return filter.Object{"number": "0x5", "timestamp": "0x69"} }
		future[1] <- SourceSyncProgressBlockResult{Block: b()}
		future[0] <- SourceSyncProgressBlockResult{Block: b()}
		if e := <-result; e != nil {
			t.Fatal(e)
		}
	})
	t.Run("standardizes actual response identity", func(t *testing.T) {
		c := sourceHistoryControlChain(1, 4, 5)
		raw := c.Local.Requester.(*sourceHistoryControlRequester).blocks["latest"]
		raw["transactions"] = []any{"ignored light transactions"}
		if _, e := sourceHistoryRefresh(context.Background(), SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{c}}, "multichain"); e != nil {
			t.Fatal(e)
		}
		if c.Local.Progress.Finalized["gasUsed"] != "0x0" || !filter.IsUndefined(raw["transactions"]) {
			t.Fatalf("not standardized %+v", raw)
		}
		if reflect.ValueOf(raw).Pointer() != reflect.ValueOf(c.Local.Progress.Finalized).Pointer() {
			t.Fatal("standardizer copied actual map")
		}
	})
	t.Run("invalid required block is rejected", func(t *testing.T) {
		c := sourceHistoryControlChain(1, 4, 5)
		delete(c.Local.Requester.(*sourceHistoryControlRequester).blocks["latest"], "hash")
		if _, e := sourceHistoryRefresh(context.Background(), SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{c}}, "multichain"); e == nil {
			t.Fatal("invalid RPC block accepted")
		}
	})
	t.Run("isolated null policy differs", func(t *testing.T) {
		c := sourceHistoryControlChain(1, 4, 5)
		c.RefreshRequests = sourceProgressBeginFunc(func(ctx context.Context, n string, tx bool, o SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
			if o.RetryNullBlockRequest {
				t.Error("isolated enabled retry")
			}
			out := make(chan SourceSyncProgressBlockResult, 1)
			out <- SourceSyncProgressBlockResult{Err: errors.New("null block")}
			return out
		})
		if _, e := sourceHistoryRefresh(context.Background(), SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{c}}, "isolated"); e == nil {
			t.Fatal("error lost")
		}
	})
}

func TestSourceHistoryRefreshFirstError(t *testing.T) {
	for _, pendingSibling := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending_sibling_%t", pendingSibling), func(t *testing.T) {
			firstError := errors.New("second chain rejects first")
			chains := []SourceHistoricalChain{sourceHistoryControlChain(1, 4, 5), sourceHistoryControlChain(2, 4, 5)}
			registered := make(chan struct{}, 2)
			futures := []chan SourceSyncProgressBlockResult{make(chan SourceSyncProgressBlockResult, 1), make(chan SourceSyncProgressBlockResult, 1)}
			cancelled := make(chan struct{})
			for i := range chains {
				index := i
				chains[i].RefreshRequests = sourceProgressBeginFunc(func(ctx context.Context, n string, tx bool, o SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
					registered <- struct{}{}
					if index == 0 {
						go func() {
							<-ctx.Done()
							if !pendingSibling {
								futures[0] <- SourceSyncProgressBlockResult{Err: errors.New("first chain rejects later")}
							}
							close(cancelled)
						}()
					}
					return futures[index]
				})
			}
			result := make(chan error, 1)
			go func() {
				_, e := sourceHistoryRefresh(context.Background(), SourceHistoricalCoordinatorOptions{Chains: chains}, "multichain")
				result <- e
			}()
			<-registered
			<-registered
			futures[1] <- SourceSyncProgressBlockResult{Err: firstError}
			select {
			case e := <-result:
				if !errors.Is(e, firstError) {
					t.Fatalf("first rejection lost: %v", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("refresh blocked on pending sibling")
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("request context not cancelled")
			}
			for _, c := range chains {
				if c.Local.Progress.Finalized["number"] != "0x4" {
					t.Fatal("failed refresh applied partial progress")
				}
			}
		})
	}
}
