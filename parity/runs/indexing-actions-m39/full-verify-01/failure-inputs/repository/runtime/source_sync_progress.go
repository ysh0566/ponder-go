package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

// SourceSyncProgressRequestOptions makes the source's per-request context an
// explicit part of the block-action boundary, rather than a discarded flag.
type SourceSyncProgressRequestOptions struct {
	RetryNullBlockRequest bool
}

// SourceSyncProgressBlockResult is the one completion of a registered block
// request. The map is retained by reference; Err is never wrapped.
type SourceSyncProgressBlockResult struct {
	Block filter.Object
	Err   error
}

// SourceSyncProgressRequester has the source's synchronous request-admission,
// asynchronous completion boundary. Begin must register and bind the request's
// identity before returning its future. Moving a synchronous Call into a
// goroutine does not satisfy this contract. Each future delivers exactly one
// result; block maps are standardized and retained by reference.
type SourceSyncProgressRequester interface {
	BeginBlockByNumber(context.Context, string, bool, SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult
}

// SourceSyncProgressRPC adapts an explicitly asynchronous production requester.
// A synchronous rpc.Requester cannot provide ordered admission while requests
// overlap, so there is deliberately no implicit synchronous fallback.
// Transport errors retain the requester's own retry policy; null blocks also
// receive the pinned source's nine retries and 125 ms exponential wait.
type SourceSyncProgressRPC struct {
	requester rpc.AsyncRequester
	wait      func(context.Context, time.Duration) error
}

func NewSourceSyncProgressRPC(requester rpc.AsyncRequester) *SourceSyncProgressRPC {
	return &SourceSyncProgressRPC{requester: requester, wait: sourceSyncProgressWait}
}

func sourceSyncProgressWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type SourceSyncProgressBlockNotFoundError struct{ Number string }

func (e *SourceSyncProgressBlockNotFoundError) Error() string {
	return fmt.Sprintf("Block at number %q could not be found.", e.Number)
}
func (*SourceSyncProgressBlockNotFoundError) SourceErrorName() string { return "BlockNotFoundError" }

func (r *SourceSyncProgressRPC) BeginBlockByNumber(ctx context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
	result := make(chan SourceSyncProgressBlockResult, 1)
	finish := func(block filter.Object, err error) {
		result <- SourceSyncProgressBlockResult{Block: block, Err: err}
		close(result)
	}
	if r == nil || r.requester == nil {
		finish(nil, fmt.Errorf("source sync progress async RPC requester required"))
		return result
	}
	if err := ctx.Err(); err != nil {
		finish(nil, err)
		return result
	}
	params := []any{number, transactions}
	var block filter.Object
	// This call is intentionally in the caller's synchronous Begin segment.
	// The raw requester has bound this request before another Begin can occur.
	pending := r.requester.BeginCall(ctx, "eth_getBlockByNumber", params, &block)
	go func() {
		for attempt := 0; ; attempt++ {
			if pending == nil {
				finish(nil, fmt.Errorf("source sync progress async RPC future required"))
				return
			}
			select {
			case <-ctx.Done():
				finish(nil, ctx.Err())
				return
			case err, ok := <-pending:
				if !ok {
					finish(nil, fmt.Errorf("source sync progress async RPC future closed without result"))
					return
				}
				if err != nil {
					finish(nil, err)
					return
				}
			}
			if block != nil {
				standardized, err := rpc.StandardizeSourceBlock(block, rpc.ActionRequest{Method: "eth_getBlockByNumber", Params: params})
				finish(standardized, err)
				return
			}
			if !options.RetryNullBlockRequest || attempt == 9 {
				finish(nil, &SourceSyncProgressBlockNotFoundError{Number: number})
				return
			}
			wait := r.wait
			if wait == nil {
				wait = sourceSyncProgressWait
			}
			if err := wait(ctx, (125*time.Millisecond)<<attempt); err != nil {
				finish(nil, err)
				return
			}
			if err := ctx.Err(); err != nil {
				finish(nil, err)
				return
			}
			block = nil
			pending = r.requester.BeginCall(ctx, "eth_getBlockByNumber", params, &block)
		}
	}()
	return result
}

// GetBlockByNumber is a synchronous convenience around the explicit Begin
// boundary. Progress itself always begins both diagnostics before awaiting.
func (r *SourceSyncProgressRPC) GetBlockByNumber(ctx context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) (filter.Object, error) {
	pending := r.BeginBlockByNumber(ctx, number, transactions, options)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-pending:
		return result.Block, result.Err
	}
}

type SourceSyncProgressTag string

const (
	SourceSyncStart     SourceSyncProgressTag = "start"
	SourceSyncEnd       SourceSyncProgressTag = "end"
	SourceSyncCurrent   SourceSyncProgressTag = "current"
	SourceSyncFinalized SourceSyncProgressTag = "finalized"
)

// SourceSyncProgress retains the actual supplied/requested block maps. Current
// and End are nil for the source's undefined values. The runtime may replace
// these references as synchronization progresses; methods read their latest
// values. As with the source's mutable object, the owner serializes mutations.
type SourceSyncProgress struct {
	Start     filter.Object
	End       filter.Object
	Current   filter.Object
	Finalized filter.Object
	ChainID   uint64
}

func (p *SourceSyncProgress) IsEnd() (bool, error) {
	if p.End == nil || p.Current == nil {
		return false, nil
	}
	return sourceSyncProgressAtOrAfter(p.Current, p.End)
}

func (p *SourceSyncProgress) IsFinalized() (bool, error) {
	if p.Current == nil {
		return false, nil
	}
	return sourceSyncProgressAtOrAfter(p.Current, p.Finalized)
}

func sourceSyncProgressAtOrAfter(current, target filter.Object) (bool, error) {
	a, err := sourceHistoricalNumber(current["number"])
	if err != nil {
		return false, err
	}
	b, err := sourceHistoricalNumber(target["number"])
	if err != nil {
		return false, err
	}
	// hexToNumber uses JavaScript Number, including its rounding above 2^53.
	return float64(a) >= float64(b), nil
}

// GetCheckpoint rounds the start down for a closed interval and every other
// tag up. Only an absent end yields nil; reading an absent current is an error,
// matching the source's requirement to wait until the backfill is complete.
func (p *SourceSyncProgress) GetCheckpoint(tag SourceSyncProgressTag) (*string, error) {
	var block filter.Object
	switch tag {
	case SourceSyncStart:
		block = p.Start
	case SourceSyncEnd:
		if p.End == nil {
			return nil, nil
		}
		block = p.End
	case SourceSyncCurrent:
		block = p.Current
	case SourceSyncFinalized:
		block = p.Finalized
	default:
		return nil, fmt.Errorf("source sync progress checkpoint tag is invalid: %q", tag)
	}
	if block == nil {
		return nil, fmt.Errorf("source sync progress %s block is undefined", tag)
	}
	number, err := sourceHistoricalNumber(block["number"])
	if err != nil {
		return nil, err
	}
	timestamp, err := sourceHistoricalNumber(block["timestamp"])
	if err != nil {
		return nil, err
	}
	encoded, err := checkpoint.Encode(checkpoint.Block(timestamp, p.ChainID, number, tag != SourceSyncStart))
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}

func sourceSyncProgressHex(number float64) (string, error) {
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 9007199254740991 || math.Trunc(number) != number {
		return "", fmt.Errorf("source sync progress block must be a nonnegative safe integer: %v", number)
	}
	return fmt.Sprintf("0x%x", uint64(number)), nil
}

func sourceSyncProgressEnd(value any) (float64, error) {
	// This is the raw toBlock, not FilterToBlock: factory bounds do not extend
	// the source's end. Explicit null is distinct from undefined and coerces to 0.
	switch n := value.(type) {
	case nil:
		return 0, nil
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case json.Number:
		return n.Float64()
	default:
		return 0, fmt.Errorf("source sync progress toBlock must be numeric: %T", value)
	}
}

// GetLocalSourceSyncProgress implements runtime/index.ts getLocalSyncProgress.
// It acquires start and cached-current blocks concurrently, and only then
// resolves a finite end. A future end uses the source's maximum timestamp and
// is not fetched from an RPC provider. All requests opt into null-block retries.
func GetLocalSourceSyncProgress(ctx context.Context, chainID uint64, requests SourceSyncProgressRequester, filters []filter.Object, finalized filter.Object, cached []store.SourceIntervalEntry) (*SourceSyncProgress, error) {
	if requests == nil {
		return nil, fmt.Errorf("source sync progress block requester required")
	}
	start := math.Inf(1)
	for _, f := range filters {
		start = math.Min(start, filter.FilterFromBlock(f))
	}
	cachedBlock, err := CachedSourceBlock(filters, cached)
	if err != nil {
		return nil, err
	}
	startHex, err := sourceSyncProgressHex(start)
	if err != nil {
		return nil, err
	}
	numbers := []string{startHex}
	pending := [2]<-chan SourceSyncProgressBlockResult{
		requests.BeginBlockByNumber(ctx, startHex, false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true}),
	}
	if cachedBlock != nil {
		cachedHex, err := sourceSyncProgressHex(*cachedBlock)
		if err != nil {
			return nil, err
		}
		numbers = append(numbers, cachedHex)
		pending[1] = requests.BeginBlockByNumber(ctx, cachedHex, false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
	}
	for index := range numbers {
		if pending[index] == nil {
			return nil, fmt.Errorf("source sync progress block future required")
		}
	}
	blocks := make([]filter.Object, len(numbers))
	for range numbers {
		var result SourceSyncProgressBlockResult
		var index int
		var ok bool
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result, ok = <-pending[0]:
			index = 0
		case result, ok = <-pending[1]:
			index = 1
		}
		if !ok {
			return nil, fmt.Errorf("source sync progress block future closed without result")
		}
		if result.Err != nil {
			return nil, result.Err
		}
		if result.Block == nil {
			return nil, &SourceSyncProgressBlockNotFoundError{Number: numbers[index]}
		}
		blocks[index] = result.Block
		pending[index] = nil
	}
	progress := &SourceSyncProgress{Start: blocks[0], Finalized: finalized, ChainID: chainID}
	if len(blocks) == 2 {
		progress.Current = blocks[1]
	}
	for _, f := range filters {
		if filter.IsUndefined(sourceHistoricalProperty(f, "toBlock")) {
			return progress, nil
		}
	}
	end := math.Inf(-1)
	for _, f := range filters {
		number, err := sourceSyncProgressEnd(f["toBlock"])
		if err != nil {
			return nil, err
		}
		end = math.Max(end, number)
	}
	finalizedNumber, err := sourceHistoricalNumber(finalized["number"])
	if err != nil {
		return nil, err
	}
	endHex, err := sourceSyncProgressHex(end)
	if err != nil {
		return nil, err
	}
	if end > float64(finalizedNumber) {
		progress.End = filter.Object{"number": endHex, "hash": "0x", "parentHash": "0x", "timestamp": fmt.Sprintf("0x%x", checkpoint.Max.BlockTimestamp)}
	} else {
		pending := requests.BeginBlockByNumber(ctx, endHex, false, SourceSyncProgressRequestOptions{RetryNullBlockRequest: true})
		if pending == nil {
			return nil, fmt.Errorf("source sync progress block future required")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result, ok := <-pending:
			if !ok {
				return nil, fmt.Errorf("source sync progress block future closed without result")
			}
			if result.Err != nil {
				return nil, result.Err
			}
			if result.Block == nil {
				return nil, &SourceSyncProgressBlockNotFoundError{Number: endHex}
			}
			progress.End = result.Block
		}

	}
	return progress, nil
}
