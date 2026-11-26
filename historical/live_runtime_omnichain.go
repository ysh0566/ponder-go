package historical

import (
	"context"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/rpc"
	source "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/store"
)

func (r *RuntimeRunner) omnichainWatermark() string {
	watermark := ""
	for _, head := range r.finalized {
		if watermark == "" || head.Checkpoint < watermark {
			watermark = head.Checkpoint
		}
	}
	return watermark
}

// Authenticate the setup limit during the baseline walk, which may use a
// different requester from preflight. Earlier reads are covered by ancestry.
func (r *RuntimeRunner) setupLineageCheck(i int, id uint64) func(uint64, store.ManagedLiveHead) error {
	if r.build.Ordering != "omnichain" || len(r.build.SetupCallbacks[i]) == 0 {
		return nil
	}
	height := max(r.starts[id], r.lastSetupBlock(i))
	watermark := r.omnichainWatermark()
	return func(n uint64, head store.ManagedLiveHead) error {
		if n == height && head.Checkpoint > watermark {
			return fmt.Errorf("omnichain setup block exceeds global finalized watermark on authenticated lineage")
		}
		return nil
	}
}

// Every finite event range and its setup reads must fit the common watermark.
func (r *RuntimeRunner) validateOmnichainFinalizedRange(ctx context.Context, requesters []rpc.Requester) error {
	watermark := r.omnichainWatermark()
	for i, chain := range r.build.Chains {
		id := runtimeChainID(chain)
		final, err := checkpoint.Decode(r.finalized[id].Checkpoint)
		if err != nil {
			return err
		}
		checkHeight := func(height uint64, field string) error {
			if height > final.BlockNumber {
				return fmt.Errorf("omnichain %s exceeds chain finality", field)
			}
			raw, err := rpc.NewActions(requesters[i]).GetSourceBlockByNumber(ctx, source.Hex(height), false)
			if err != nil {
				return err
			}
			head, err := runtimeHead(id, raw)
			if err != nil {
				return err
			}
			cp, err := checkpoint.Decode(head.Checkpoint)
			if err != nil || cp.BlockNumber != height || head.Checkpoint > watermark {
				return fmt.Errorf("omnichain %s exceeds global finalized watermark", field)
			}
			return nil
		}
		end := r.ends[id]
		if err := checkHeight(end, "end"); err != nil {
			return err
		}
		// A setup-only contract can set its default RPC height past the last event.
		if setupHeight := r.lastSetupBlock(i); setupHeight > end {
			if err := checkHeight(setupHeight, "setup block"); err != nil {
				return err
			}
		}
	}
	return nil
}
