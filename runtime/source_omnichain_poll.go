package runtime

import (
	"context"
	"fmt"
	"iter"
	"math"
	"strconv"
	"time"

	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/rpc"
)

// Poll canonical heads using the same requester and child addresses as the
// historical scan. SyncManaged fills gaps and emits fork and finality events.
func pollOmnichainSource(ctx context.Context, chain SourceHistoricalChain) iter.Seq2[SourceRealtimeEvent, error] {
	return func(yield func(SourceRealtimeEvent, error) bool) {
		local := chain.Local
		fail := func(err error) { yield(SourceRealtimeEvent{}, err) }
		interval := uint64(1000)
		if v := chain.Chain["pollingInterval"]; v != nil && !filter.IsUndefined(v) {
			n, err := strconv.ParseUint(fmt.Sprint(v), 10, 64)
			if err != nil || n < 100 || n > uint64(math.MaxInt64/int64(time.Millisecond)) {
				fail(fmt.Errorf("invalid polling interval for chain %d: %v", local.ChainID, v))
				return
			}
			interval = n
		}
		if local.Requester == nil || chain.ReorgWindow < 0 {
			fail(fmt.Errorf("invalid realtime requester or reorg window for chain %d", local.ChainID))
			return
		}
		var filters []filter.Object
		for _, cb := range local.FilterSession.Callbacks(local.EventCallbacks) {
			f, ok := cb["filter"].(filter.Object)
			if !ok {
				fail(fmt.Errorf("missing callback filter for chain %d", local.ChainID))
				return
			}
			filters = append(filters, f)
		}
		syncer, err := NewManagedSourceRealtimeSync(local.Requester, local.Children, local.Progress.Finalized, nil, filters, SourceRealtimeChain{
			ID: local.ChainID, Name: fmt.Sprint(chain.Chain["name"]), ReorgWindow: uint64(chain.ReorgWindow), PollingInterval: interval,
		})
		if err != nil {
			fail(err)
			return
		}
		var end uint64
		bounded := local.Progress.End != nil
		if bounded {
			end, err = sourceHistoricalNumber(local.Progress.End["number"])
			if err != nil {
				fail(err)
				return
			}
		}
		actions := rpc.NewActions(local.Requester)
		for {
			head, err := actions.GetSourceBlockByNumber(ctx, "latest", true)
			if err != nil {
				fail(err)
				return
			}
			n, err := sourceHistoricalNumber(head["number"])
			if err != nil {
				fail(err)
				return
			}
			if bounded && n > end {
				head, err = actions.GetSourceBlockByNumber(ctx, Hex(end), true)
				if err != nil {
					fail(err)
					return
				}
				if got, err := sourceHistoricalNumber(head["number"]); err != nil || got != end {
					fail(fmt.Errorf("RPC returned a different height for chain %d end %d", local.ChainID, end))
					return
				}
			}
			complete := false
			for event, err := range syncer.SyncManaged(ctx, head) {
				if err != nil {
					fail(err)
					return
				}
				if event.Type == "block" && bounded {
					number, err := sourceHistoricalNumber(event.Block["number"])
					if err != nil {
						fail(err)
						return
					}
					complete = number == end
				}
				if !yield(event, nil) {
					return
				}
			}
			if complete {
				return
			}
			select {
			case <-ctx.Done():
				fail(ctx.Err())
				return
			case <-time.After(time.Duration(interval) * time.Millisecond):
			}
		}
	}
}
