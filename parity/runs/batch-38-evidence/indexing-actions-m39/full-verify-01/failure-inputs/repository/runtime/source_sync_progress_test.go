package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

// Immediate functions are deterministic test providers: registration and
// response creation execute synchronously. They are not goroutine adapters for
// arbitrary blocking requesters. Delayed providers use the explicit Begin type.
type sourceProgressImmediateFunc func(context.Context, string, bool, SourceSyncProgressRequestOptions) (filter.Object, error)

func (f sourceProgressImmediateFunc) BeginBlockByNumber(ctx context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
	block, err := f(ctx, number, transactions, options)
	return sourceProgressResolved(block, err)
}

type sourceProgressBeginFunc func(context.Context, string, bool, SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult

func (f sourceProgressBeginFunc) BeginBlockByNumber(ctx context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
	return f(ctx, number, transactions, options)
}

func sourceProgressResolved(block filter.Object, err error) <-chan SourceSyncProgressBlockResult {
	result := make(chan SourceSyncProgressBlockResult, 1)
	result <- SourceSyncProgressBlockResult{Block: block, Err: err}
	close(result)
	return result
}

type sourceProgressRawFunc func(context.Context, string, any, any) error

func (f sourceProgressRawFunc) BeginCall(ctx context.Context, method string, params, result any) <-chan error {
	err := f(ctx, method, params, result)
	pending := make(chan error, 1)
	pending <- err
	close(pending)
	return pending
}

type sourceProgressRawBeginFunc func(context.Context, string, any, any) <-chan error

func (f sourceProgressRawBeginFunc) BeginCall(ctx context.Context, method string, params, result any) <-chan error {
	return f(ctx, method, params, result)
}

func sourceProgressBlock(number uint64) filter.Object {
	return filter.Object{"number": fmt.Sprintf("0x%x", number), "timestamp": fmt.Sprintf("0x%x", 1000+number), "hash": fmt.Sprintf("hash-%d", number), "parentHash": fmt.Sprintf("parent-%d", number), "logsBloom": "0x", "transactions": []any{}}
}

func sourceProgressFilter(from, to any) filter.Object {
	return filter.Object{"type": "block", "chainId": 1, "sourceId": "Blocks", "interval": 1, "offset": 0, "fromBlock": from, "toBlock": to, "hasTransactionReceipt": false, "include": []string{}}
}

func sourceProgressCache(f filter.Object, intervals ...store.SourceInterval) []store.SourceIntervalEntry {
	return []store.SourceIntervalEntry{{Key: f, Values: []store.SourceFragmentIntervals{{Fragment: filter.Object{}, Intervals: intervals}}}}
}

// These are the eight assertion forms from the two pinned progress cases,
// exercised with deterministic headers rather than the platform-blocked Anvil
// hooks. They are unit checks, not pristine source execution or source credit.
// Complete original setup and provider capture must be verified separately.
func TestSourceLocalSyncProgressAssertions(t *testing.T) {
	for _, c := range []struct {
		name string
		end  any
		want filter.Object
	}{
		{"getLocalSyncProgress()", filter.Undefined, nil},
		{"getLocalSyncProgress() future end block", 12, filter.Object{"hash": "0x", "number": "0xc", "parentHash": "0x", "timestamp": "0x2540be3ff"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := sourceProgressFilter(filter.Undefined, c.end)
			requester := sourceProgressImmediateFunc(func(_ context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) (filter.Object, error) {
				if number != "0x0" || transactions || !options.RetryNullBlockRequest {
					return nil, fmt.Errorf("unexpected progress request: %s %v %+v", number, transactions, options)
				}
				return sourceProgressBlock(0), nil
			})
			progress, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f))
			if err != nil {
				t.Fatal(err)
			}
			if progress.Finalized["number"] != "0x0" {
				t.Fatalf("finalized.number = %v", progress.Finalized["number"])
			}
			if progress.Start["number"] != "0x0" {
				t.Fatalf("start.number = %v", progress.Start["number"])
			}
			if !reflect.DeepEqual(progress.End, c.want) {
				t.Fatalf("end = %#v, want %#v", progress.End, c.want)
			}
			if progress.Current != nil {
				t.Fatalf("current = %#v, want undefined", progress.Current)
			}
		})
	}
}

// All controls below are supplementary and add zero original test credit.
// A same-height request must retain its positional response identity even
// though the diagnostic requests overlap. No sleep or scheduler hint is used.
func TestSourceLocalSyncProgressSameHeightIdentity(t *testing.T) {
	for iteration := 0; iteration < 128; iteration++ {
		f := sourceProgressFilter(0, 0)
		first, second, third := sourceProgressBlock(0), sourceProgressBlock(0), sourceProgressBlock(0)
		first["responseIdentity"], second["responseIdentity"], third["responseIdentity"] = "first", "second", "third"
		var calls atomic.Int64
		requester := sourceProgressImmediateFunc(func(_ context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) (filter.Object, error) {
			if number != "0x0" || transactions || !options.RetryNullBlockRequest {
				return nil, errors.New("unexpected same-height request")
			}
			switch calls.Add(1) {
			case 1:
				return first, nil
			case 2:
				return second, nil
			case 3:
				return third, nil
			default:
				return nil, errors.New("extra same-height request")
			}
		})
		p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f, store.SourceInterval{0, 0}))
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		if reflect.ValueOf(p.Start).Pointer() != reflect.ValueOf(first).Pointer() || reflect.ValueOf(p.Current).Pointer() != reflect.ValueOf(second).Pointer() || reflect.ValueOf(p.End).Pointer() != reflect.ValueOf(third).Pointer() {
			t.Fatalf("iteration %d: same-height response identities swapped: start=%v current=%v end=%v; want first/second/third", iteration, p.Start["responseIdentity"], p.Current["responseIdentity"], p.End["responseIdentity"])
		}
		if calls.Load() != 3 {
			t.Fatalf("iteration %d: requests = %d, want 3", iteration, calls.Load())
		}
	}
}

func TestSourceLocalSyncProgressControls(t *testing.T) {
	t.Run("same_height_reverse_completion_keeps_begin_identity", func(t *testing.T) {
		f := sourceProgressFilter(0, 0)
		first, second, third := sourceProgressBlock(0), sourceProgressBlock(0), sourceProgressBlock(0)
		pendingFirst := make(chan SourceSyncProgressBlockResult, 1)
		begun, completed := []string{}, []string{}
		requester := sourceProgressBeginFunc(func(_ context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
			if number != "0x0" || transactions || !options.RetryNullBlockRequest {
				return sourceProgressResolved(nil, errors.New("unexpected request"))
			}
			switch len(begun) {
			case 0:
				begun = append(begun, "first")
				return pendingFirst
			case 1:
				begun = append(begun, "second")
				pendingSecond := sourceProgressResolved(second, nil)
				completed = append(completed, "second")
				pendingFirst <- SourceSyncProgressBlockResult{Block: first}
				close(pendingFirst)
				completed = append(completed, "first")
				return pendingSecond
			case 2:
				begun = append(begun, "third")
				return sourceProgressResolved(third, nil)
			default:
				return sourceProgressResolved(nil, errors.New("extra request"))
			}
		})
		p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f, store.SourceInterval{0, 0}))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(begun, []string{"first", "second", "third"}) || !reflect.DeepEqual(completed, []string{"second", "first"}) {
			t.Fatalf("begin/completion order: %v / %v", begun, completed)
		}
		if reflect.ValueOf(p.Start).Pointer() != reflect.ValueOf(first).Pointer() || reflect.ValueOf(p.Current).Pointer() != reflect.ValueOf(second).Pointer() || reflect.ValueOf(p.End).Pointer() != reflect.ValueOf(third).Pointer() {
			t.Fatal("reverse completion replaced positional response identity")
		}
	})
	t.Run("first_async_failure_still_begins_second_diagnostic", func(t *testing.T) {
		f := sourceProgressFilter(0, 0)
		originalError := errors.New("first future rejected")
		calls := 0
		requester := sourceProgressImmediateFunc(func(context.Context, string, bool, SourceSyncProgressRequestOptions) (filter.Object, error) {
			calls++
			if calls == 1 {
				return nil, originalError
			}
			return sourceProgressBlock(0), nil
		})
		p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f, store.SourceInterval{0, 0}))
		if p != nil || err != originalError || calls != 2 {
			t.Fatalf("first failure prevented second Begin or caused end: p=%+v err=%v calls=%d", p, err, calls)
		}
	})
	t.Run("concurrent_diagnostics_before_end_and_reference_identity", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		f := sourceProgressFilter(0, 9)
		start, current, end, finalized := sourceProgressBlock(0), sourceProgressBlock(4), sourceProgressBlock(9), sourceProgressBlock(10)
		entered := make(chan string, 3)
		release := make(chan struct{})
		requester := sourceProgressBeginFunc(func(ctx context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
			if transactions || !options.RetryNullBlockRequest {
				return sourceProgressResolved(nil, errors.New("all diagnostics and end requests must use false/true flags"))
			}
			// Registration occurs synchronously, not inside the completion worker.
			entered <- number
			block := map[string]filter.Object{"0x0": start, "0x4": current, "0x9": end}[number]
			if number == "0x9" {
				return sourceProgressResolved(block, nil)
			}
			pending := make(chan SourceSyncProgressBlockResult, 1)
			go func() {
				select {
				case <-release:
					pending <- SourceSyncProgressBlockResult{Block: block}
				case <-ctx.Done():
					pending <- SourceSyncProgressBlockResult{Err: ctx.Err()}
				}
				close(pending)
			}()
			return pending
		})
		type outcome struct {
			progress *SourceSyncProgress
			err      error
		}
		done := make(chan outcome, 1)
		go func() {
			progress, err := GetLocalSourceSyncProgress(ctx, 777, requester, []filter.Object{f}, finalized, sourceProgressCache(f, store.SourceInterval{0, 4}))
			done <- outcome{progress, err}
		}()
		seen := map[string]bool{}
		for range 2 {
			select {
			case number := <-entered:
				seen[number] = true
			case <-ctx.Done():
				t.Fatal("start and current did not both begin before either resolved")
			}
		}
		if !reflect.DeepEqual(seen, map[string]bool{"0x0": true, "0x4": true}) {
			t.Fatalf("diagnostics = %v", seen)
		}
		select {
		case number := <-entered:
			t.Fatalf("end request %s preceded diagnostics", number)
		default:
		}
		close(release)
		var result outcome
		select {
		case result = <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if result.err != nil {
			t.Fatal(result.err)
		}
		if number := <-entered; number != "0x9" {
			t.Fatalf("end request = %s", number)
		}
		p := result.progress
		for _, pair := range [][2]filter.Object{{p.Start, start}, {p.Current, current}, {p.End, end}, {p.Finalized, finalized}} {
			if reflect.ValueOf(pair[0]).Pointer() != reflect.ValueOf(pair[1]).Pointer() {
				t.Fatal("progress copied a source block map")
			}
		}
		if p.ChainID != 777 {
			t.Fatal("chain ID was not retained")
		}
	})
	t.Run("identical_start_current_end_are_three_requests", func(t *testing.T) {
		f := sourceProgressFilter(0, 0)
		var calls atomic.Int64
		requester := sourceProgressImmediateFunc(func(_ context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) (filter.Object, error) {
			calls.Add(1)
			if number != "0x0" || transactions || !options.RetryNullBlockRequest {
				return nil, errors.New("unexpected request")
			}
			return sourceProgressBlock(0), nil
		})
		p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f, store.SourceInterval{0, 0}))
		if err != nil || calls.Load() != 3 || p == nil || p.Current == nil || p.End == nil {
			t.Fatalf("same-height requests were deduplicated: calls=%d progress=%+v err=%v", calls.Load(), p, err)
		}
	})
	t.Run("factory_controls_start_but_end_uses_raw_filter_bound", func(t *testing.T) {
		factory := filter.Object{"id": "factory", "type": "log", "fromBlock": 0, "toBlock": 4}
		f := filter.Object{"type": "log", "address": factory, "fromBlock": 8, "toBlock": 12}
		cached := append(sourceProgressCache(f), sourceProgressCache(factory)...)
		requester := sourceProgressImmediateFunc(func(_ context.Context, number string, _ bool, _ SourceSyncProgressRequestOptions) (filter.Object, error) {
			if number != "0x0" {
				return nil, fmt.Errorf("factory start not used: %s", number)
			}
			return sourceProgressBlock(0), nil
		})
		p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(0), cached)
		if err != nil || p.End["number"] != "0xc" || p.Current != nil {
			t.Fatalf("factory progress: %+v, %v", p, err)
		}
	})
	t.Run("any_undefined_end_keeps_progress_unbounded", func(t *testing.T) {
		for _, absent := range []bool{false, true} {
			f, other := sourceProgressFilter(0, 100), sourceProgressFilter(0, filter.Undefined)
			if absent {
				delete(other, "toBlock")
			}
			cached := append(sourceProgressCache(f), sourceProgressCache(other)...)
			var calls atomic.Int64
			requester := sourceProgressImmediateFunc(func(_ context.Context, number string, _ bool, _ SourceSyncProgressRequestOptions) (filter.Object, error) {
				calls.Add(1)
				return sourceProgressBlock(0), nil
			})
			// Source does not even read finalized.number on this early return.
			p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f, other}, filter.Object{"marker": "unparsed"}, cached)
			if err != nil || p.End != nil || calls.Load() != 1 || p.Finalized["marker"] != "unparsed" {
				t.Fatalf("unbounded result: %+v, %v, %d calls", p, err, calls.Load())
			}
		}
	})
	t.Run("maximum_filter_end_and_null_are_not_factory_defaults", func(t *testing.T) {
		f, other := sourceProgressFilter(0, 2), sourceProgressFilter(0, 5)
		cached := append(sourceProgressCache(f), sourceProgressCache(other)...)
		requester := sourceProgressImmediateFunc(func(_ context.Context, number string, _ bool, _ SourceSyncProgressRequestOptions) (filter.Object, error) {
			if number == "0x0" {
				return sourceProgressBlock(0), nil
			}
			if number == "0x5" {
				return sourceProgressBlock(5), nil
			}
			return nil, fmt.Errorf("wrong maximum end: %s", number)
		})
		p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f, other}, sourceProgressBlock(10), cached)
		if err != nil || p.End["number"] != "0x5" {
			t.Fatalf("maximum end = %+v, %v", p, err)
		}
		f["toBlock"] = nil
		p, err = GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f))
		if err != nil || p.End["number"] != "0x0" {
			t.Fatalf("null end must coerce to zero, not undefined: %+v, %v", p, err)
		}
	})
	t.Run("diagnostic_failure_returns_before_other_request_resolves", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		f := sourceProgressFilter(0, 9)
		originalError := errors.New("current RPC failed")
		pendingStart := make(chan SourceSyncProgressBlockResult, 1)
		startEntered, currentEntered := false, false
		requester := sourceProgressBeginFunc(func(_ context.Context, number string, _ bool, _ SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
			switch number {
			case "0x0":
				startEntered = true
				return pendingStart
			case "0x4":
				if !startEntered {
					return sourceProgressResolved(nil, errors.New("current began before start"))
				}
				currentEntered = true
				return sourceProgressResolved(nil, originalError)
			default:
				return sourceProgressResolved(nil, errors.New("end must not run after a diagnostic failure"))
			}
		})
		p, err := GetLocalSourceSyncProgress(ctx, 1, requester, []filter.Object{f}, sourceProgressBlock(10), sourceProgressCache(f, store.SourceInterval{0, 4}))
		if p != nil || err != originalError || !startEntered || !currentEntered || len(pendingStart) != 0 {
			t.Fatalf("did not propagate the failure before start completed: %+v %v", p, err)
		}
		pendingStart <- SourceSyncProgressBlockResult{Block: sourceProgressBlock(0)}
		close(pendingStart)
	})
	t.Run("diagnostic_wait_obeys_context_cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f := sourceProgressFilter(0, filter.Undefined)
		requester := sourceProgressImmediateFunc(func(ctx context.Context, _ string, _ bool, _ SourceSyncProgressRequestOptions) (filter.Object, error) {
			cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		})
		p, err := GetLocalSourceSyncProgress(ctx, 1, requester, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f))
		if p != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %+v, %v", p, err)
		}
	})
	t.Run("null_action_results_and_end_failures_remain_errors", func(t *testing.T) {
		for _, failNumber := range []string{"0x0", "0x1"} {
			f := sourceProgressFilter(0, 1)
			requester := sourceProgressImmediateFunc(func(_ context.Context, number string, _ bool, _ SourceSyncProgressRequestOptions) (filter.Object, error) {
				if number == failNumber {
					return nil, nil
				}
				return sourceProgressBlock(0), nil
			})
			p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(1), sourceProgressCache(f))
			var missing *SourceSyncProgressBlockNotFoundError
			if p != nil || !errors.As(err, &missing) || missing.Number != failNumber {
				t.Fatalf("null %s action result accepted: %+v %v", failNumber, p, err)
			}
		}
		f := sourceProgressFilter(0, 1)
		originalError := errors.New("end request failed")
		requester := sourceProgressImmediateFunc(func(_ context.Context, number string, _ bool, _ SourceSyncProgressRequestOptions) (filter.Object, error) {
			if number == "0x1" {
				return nil, originalError
			}
			return sourceProgressBlock(0), nil
		})
		p, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(1), sourceProgressCache(f))
		if p != nil || err != originalError {
			t.Fatalf("end error identity changed: %+v %v", p, err)
		}
	})
	t.Run("invalid_diagnostic_numbers_rejected_without_rpc", func(t *testing.T) {
		requester := sourceProgressImmediateFunc(func(context.Context, string, bool, SourceSyncProgressRequestOptions) (filter.Object, error) {
			return nil, errors.New("must not reach RPC")
		})
		for _, number := range []float64{-1, 0.5, math.Inf(1), math.NaN(), 9007199254740992} {
			f := sourceProgressFilter(number, filter.Undefined)
			if _, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f)); err == nil {
				t.Fatalf("accepted invalid block %v", number)
			}
		}
		if _, err := GetLocalSourceSyncProgress(context.Background(), 1, requester, nil, sourceProgressBlock(0), nil); err == nil {
			t.Fatal("empty filters must fail numberToHex(Infinity), not select genesis")
		}
	})
}

func TestSourceSyncProgressMethods(t *testing.T) {
	t.Run("closed_checkpoint_rounding_and_mutable_references", func(t *testing.T) {
		block := sourceProgressBlock(3)
		p := &SourceSyncProgress{Start: block, End: block, Current: block, Finalized: block, ChainID: 77}
		for _, tag := range []SourceSyncProgressTag{SourceSyncStart, SourceSyncEnd, SourceSyncCurrent, SourceSyncFinalized} {
			encoded, err := p.GetCheckpoint(tag)
			if err != nil || encoded == nil {
				t.Fatalf("checkpoint %s: %v", tag, err)
			}
			got, err := checkpoint.Decode(*encoded)
			want := checkpoint.Checkpoint{BlockTimestamp: 1003, ChainID: 77, BlockNumber: 3}
			if tag != SourceSyncStart {
				want.TransactionIndex, want.EventType, want.EventIndex = 9999999999999999, 9, 9999999999999999
			}
			if err != nil || got != want {
				t.Fatalf("closed %s checkpoint = %+v, want %+v: %v", tag, got, want, err)
			}
		}
		block["timestamp"] = "0x4d2"
		encoded, err := p.GetCheckpoint(SourceSyncStart)
		if err != nil || encoded == nil || (*encoded)[:10] != "0000001234" {
			t.Fatal("getCheckpoint must read the current shared map")
		}
		p.Current = sourceProgressBlock(2)
		if got, err := p.IsEnd(); err != nil || got {
			t.Fatalf("before end: %v %v", got, err)
		}
		if got, err := p.IsFinalized(); err != nil || got {
			t.Fatalf("before finalized: %v %v", got, err)
		}
		for _, number := range []uint64{3, 4} {
			p.Current = sourceProgressBlock(number)
			if got, err := p.IsEnd(); err != nil || !got {
				t.Fatalf("at/after end: %v %v", got, err)
			}
			if got, err := p.IsFinalized(); err != nil || !got {
				t.Fatalf("at/after finalized: %v %v", got, err)
			}
		}
	})
	t.Run("undefined_end_and_current_are_distinct", func(t *testing.T) {
		p := &SourceSyncProgress{Start: sourceProgressBlock(0), Finalized: sourceProgressBlock(0), ChainID: 1}
		if value, err := p.GetCheckpoint(SourceSyncEnd); value != nil || err != nil {
			t.Fatalf("undefined end checkpoint: %v %v", value, err)
		}
		if value, err := p.GetCheckpoint(SourceSyncCurrent); value != nil || err == nil {
			t.Fatalf("undefined current must be an error: %v %v", value, err)
		}
		if got, err := p.IsEnd(); got || err != nil {
			t.Fatal("undefined current cannot be at end")
		}
		if got, err := p.IsFinalized(); got || err != nil {
			t.Fatal("undefined current cannot be finalized")
		}
		p.Current = sourceProgressBlock(0)
		if got, err := p.IsEnd(); got || err != nil {
			t.Fatal("undefined end cannot be reached")
		}
		if got, err := p.IsFinalized(); !got || err != nil {
			t.Fatal("genesis current equals genesis finalized")
		}
		if _, err := p.GetCheckpoint("unknown"); err == nil {
			t.Fatal("unknown checkpoint tag accepted")
		}
	})
	t.Run("comparisons_preserve_number_rounding_and_invalid_quantities_error", func(t *testing.T) {
		p := &SourceSyncProgress{Current: sourceProgressBlock(9007199254740992), End: sourceProgressBlock(9007199254740993), Finalized: sourceProgressBlock(9007199254740993)}
		if got, err := p.IsEnd(); !got || err != nil {
			t.Fatalf("hexToNumber rounding changed: %v %v", got, err)
		}
		if got, err := p.IsFinalized(); !got || err != nil {
			t.Fatalf("finality hexToNumber rounding changed: %v %v", got, err)
		}
		p.Current["number"] = "bad"
		if _, err := p.IsEnd(); err == nil {
			t.Fatal("invalid current quantity accepted")
		}
		if _, err := p.GetCheckpoint(SourceSyncCurrent); err == nil {
			t.Fatal("invalid checkpoint quantity accepted")
		}
	})
}

func TestSourceSyncProgressRPC(t *testing.T) {
	t.Run("raw_begin_identity_is_bound_synchronously_before_await", func(t *testing.T) {
		f := sourceProgressFilter(0, 0)
		first, second, third := sourceProgressBlock(0), sourceProgressBlock(0), sourceProgressBlock(0)
		firstDone := make(chan error, 1)
		calls := 0
		adapter := NewSourceSyncProgressRPC(sourceProgressRawBeginFunc(func(_ context.Context, method string, params, out any) <-chan error {
			calls++
			done := make(chan error, 1)
			if method != "eth_getBlockByNumber" || !reflect.DeepEqual(params, []any{"0x0", false}) {
				done <- errors.New("unexpected raw Begin request")
				return done
			}
			switch calls {
			case 1:
				*out.(*filter.Object) = first
				return firstDone
			case 2:
				*out.(*filter.Object) = second
				done <- nil
				firstDone <- nil
				close(firstDone)
			case 3:
				*out.(*filter.Object) = third
				done <- nil
			default:
				done <- errors.New("extra raw Begin request")
			}
			close(done)
			return done
		}))
		p, err := GetLocalSourceSyncProgress(context.Background(), 1, adapter, []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f, store.SourceInterval{0, 0}))
		if err != nil {
			t.Fatal(err)
		}
		if calls != 3 || reflect.ValueOf(p.Start).Pointer() != reflect.ValueOf(first).Pointer() || reflect.ValueOf(p.Current).Pointer() != reflect.ValueOf(second).Pointer() || reflect.ValueOf(p.End).Pointer() != reflect.ValueOf(third).Pointer() {
			t.Fatal("adapter delayed raw Begin until a goroutine or swapped response identity")
		}
	})

	t.Run("null_retries_then_standardizes_original_block_map", func(t *testing.T) {
		calls := 0
		block := sourceProgressBlock(2)
		adapter := NewSourceSyncProgressRPC(sourceProgressRawFunc(func(_ context.Context, method string, params, out any) error {
			calls++
			if method != "eth_getBlockByNumber" || !reflect.DeepEqual(params, []any{"0x2", false}) {
				return errors.New("incorrect raw request")
			}
			if calls > 1 {
				*out.(*filter.Object) = block
			}
			return nil
		}))
		delays := []time.Duration{}
		adapter.wait = func(_ context.Context, duration time.Duration) error { delays = append(delays, duration); return nil }
		got, err := adapter.GetBlockByNumber(context.Background(), "0x2", false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		if err != nil || calls != 2 || !reflect.DeepEqual(delays, []time.Duration{125 * time.Millisecond}) {
			t.Fatalf("null retry lost: calls=%d delays=%v err=%v", calls, delays, err)
		}
		if reflect.ValueOf(got).Pointer() != reflect.ValueOf(block).Pointer() || !filter.IsUndefined(block["transactions"]) || block["gasUsed"] != "0x0" {
			t.Fatal("adapter must standardize the original response map")
		}
	})
	t.Run("nine_retries_exhaust_then_block_not_found", func(t *testing.T) {
		calls := 0
		adapter := NewSourceSyncProgressRPC(sourceProgressRawFunc(func(context.Context, string, any, any) error { calls++; return nil }))
		delays := []time.Duration{}
		adapter.wait = func(_ context.Context, duration time.Duration) error { delays = append(delays, duration); return nil }
		got, err := adapter.GetBlockByNumber(context.Background(), "0x2", false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		var missing *SourceSyncProgressBlockNotFoundError
		if got != nil || !errors.As(err, &missing) || missing.Number != "0x2" || calls != 10 || len(delays) != 9 {
			t.Fatalf("retry exhaustion: calls=%d delays=%v block=%v err=%v", calls, delays, got, err)
		}
		for i, delay := range delays {
			if delay != (125*time.Millisecond)<<i {
				t.Fatalf("retry %d has wrong delay: %s", i, delay)
			}
		}
	})
	t.Run("disabled_null_retry_does_not_retry", func(t *testing.T) {
		calls := 0
		adapter := NewSourceSyncProgressRPC(sourceProgressRawFunc(func(context.Context, string, any, any) error { calls++; return nil }))
		adapter.wait = func(context.Context, time.Duration) error { return errors.New("must not wait") }
		_, err := adapter.GetBlockByNumber(context.Background(), "0x0", false, SourceSyncProgressRequestOptions{})
		var missing *SourceSyncProgressBlockNotFoundError
		if !errors.As(err, &missing) || calls != 1 {
			t.Fatalf("disabled retry: calls=%d err=%v", calls, err)
		}
	})
	t.Run("transport_and_standardization_failures_not_retried", func(t *testing.T) {
		original := errors.New("transport already exhausted its retry policy")
		calls := 0
		adapter := NewSourceSyncProgressRPC(sourceProgressRawFunc(func(context.Context, string, any, any) error { calls++; return original }))
		_, err := adapter.GetBlockByNumber(context.Background(), "0x0", false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		if err != original || calls != 1 {
			t.Fatal("transport error identity or ownership changed")
		}
		adapter = NewSourceSyncProgressRPC(sourceProgressRawFunc(func(_ context.Context, _ string, _, out any) error {
			calls++
			*out.(*filter.Object) = filter.Object{}
			return nil
		}))
		_, err = adapter.GetBlockByNumber(context.Background(), "0x0", false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		if err == nil || calls != 2 {
			t.Fatal("invalid nonnull block must fail without retry")
		}
	})
	t.Run("cancellation_interrupts_real_retry_wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int64
		adapter := NewSourceSyncProgressRPC(sourceProgressRawFunc(func(context.Context, string, any, any) error { calls.Add(1); return nil }))
		waiting := make(chan struct{})
		adapter.wait = func(ctx context.Context, _ time.Duration) error {
			close(waiting)
			// The schedule is tested above. A long timer makes this cancellation
			// check independent of whether a loaded test runner is descheduled.
			return sourceSyncProgressWait(ctx, time.Hour)
		}
		done := make(chan error, 1)
		go func() {
			_, err := adapter.GetBlockByNumber(ctx, "0x0", false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
			done <- err
		}()
		<-waiting
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatalf("retry wait ignored cancellation: calls=%d err=%v", calls.Load(), err)
		}
		_, err := adapter.GetBlockByNumber(ctx, "0x0", false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatal("already canceled call reached transport")
		}
	})
	t.Run("real_http_same_height_admission_ids_and_overlapping_requests", func(t *testing.T) {
		firstEntered, secondEntered := make(chan struct{}), make(chan struct{})
		var calls atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				ID     uint64 `json:"id"`
				Method string `json:"method"`
				Params []any  `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			calls.Add(1)
			if request.Method != "eth_getBlockByNumber" || !reflect.DeepEqual(request.Params, []any{"0x0", false}) {
				t.Errorf("unexpected HTTP request: %+v", request)
			}
			switch request.ID {
			case 1:
				close(firstEntered)
				select {
				case <-secondEntered:
				case <-r.Context().Done():
					return
				}
			case 2:
				select {
				case <-firstEntered:
				case <-r.Context().Done():
					return
				}
				close(secondEntered)
			case 3:
			default:
				t.Errorf("unexpected request ID: %d", request.ID)
			}
			block := sourceProgressBlock(0)
			block["responseIdentity"] = fmt.Sprintf("request-%d", request.ID)
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": block}); err != nil {
				t.Error(err)
			}
		}))
		defer server.Close()
		client := rpc.New(server.URL)
		client.Retries, client.RequestsPerSecond = 0, 0
		f := sourceProgressFilter(0, 0)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		p, err := GetLocalSourceSyncProgress(ctx, 1, NewSourceSyncProgressRPC(client), []filter.Object{f}, sourceProgressBlock(0), sourceProgressCache(f, store.SourceInterval{0, 0}))
		if err != nil {
			t.Fatal(err)
		}
		if p.Start["responseIdentity"] != "request-1" || p.Current["responseIdentity"] != "request-2" || p.End["responseIdentity"] != "request-3" || calls.Load() != 3 {
			t.Fatalf("HTTP admission identity: start=%v current=%v end=%v calls=%d", p.Start["responseIdentity"], p.Current["responseIdentity"], p.End["responseIdentity"], calls.Load())
		}
	})
	t.Run("real_http_requester_null_retry_and_header_standardization", func(t *testing.T) {
		var calls atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				ID     uint64 `json:"id"`
				Method string `json:"method"`
				Params []any  `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.Method != "eth_getBlockByNumber" || !reflect.DeepEqual(request.Params, []any{"0x3", false}) {
				t.Errorf("unexpected HTTP request: %+v", request)
			}
			var result any
			if calls.Add(1) > 1 {
				result = sourceProgressBlock(3)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
				t.Error(err)
			}
		}))
		defer server.Close()
		client := rpc.New(server.URL)
		client.Retries, client.RequestsPerSecond = 0, 0
		adapter := NewSourceSyncProgressRPC(client)
		adapter.wait = func(context.Context, time.Duration) error { return nil }
		block, err := adapter.GetBlockByNumber(context.Background(), "0x3", false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		if err != nil || calls.Load() != 2 || block["number"] != "0x3" || !filter.IsUndefined(block["transactions"]) || block["gasUsed"] != "0x0" {
			t.Fatalf("production HTTP adapter: calls=%d block=%v err=%v", calls.Load(), block, err)
		}
	})
}
