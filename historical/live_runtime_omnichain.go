package historical

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/rpc"
	source "ponder.local/ponder-go/runtime"
)

// This finite closure deliberately excludes the upstream historical-to-realtime
// pending queue. Every end must be below the same acquired global watermark.
func (r *RuntimeRunner) validateOmnichainFinalizedRange(ctx context.Context, requesters []rpc.Requester) error {
	watermark := ""
	for _, head := range r.finalized {
		if watermark == "" || head.Checkpoint < watermark {
			watermark = head.Checkpoint
		}
	}
	for i, chain := range r.build.Chains {
		id := runtimeChainID(chain)
		final, err := checkpoint.Decode(r.finalized[id].Checkpoint)
		if err != nil || r.ends[id] > final.BlockNumber {
			return fmt.Errorf("omnichain end exceeds chain finality")
		}
		raw, err := rpc.NewActions(requesters[i]).GetSourceBlockByNumber(ctx, source.Hex(r.ends[id]), false)
		if err != nil {
			return err
		}
		end, err := runtimeHead(id, raw)
		if err != nil {
			return err
		}
		cp, err := checkpoint.Decode(end.Checkpoint)
		if err != nil || cp.BlockNumber != r.ends[id] || end.Checkpoint > watermark {
			return fmt.Errorf("omnichain end exceeds global finalized watermark")
		}
	}
	return nil
}
