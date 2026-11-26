package historical

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/rpc"
	source "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/store"
)

// Only a well-formed chain observation that has not reached the requested
// range is eligible for another poll. RPC and proof errors go to the caller.
type pendingOmnichainRange string

func (p pendingOmnichainRange) Error() string { return string(p) }

func (r *RuntimeRunner) checkOpeningRange(ctx context.Context, reqs []rpc.Requester) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.acquireOpeningHeads(ctx, reqs); err != nil {
			return err
		}
		if r.build.Ordering != "omnichain" {
			return ctx.Err()
		}

		err := r.validateOmnichainFinalizedRange(ctx, reqs)
		if err == nil {
			return ctx.Err()
		}
		var pending pendingOmnichainRange
		if r.options.OmnichainFinalityPollInterval == 0 || !errors.As(err, &pending) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.options.OmnichainFinalityPollInterval):
		}
	}
}

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
				return pendingOmnichainRange("omnichain " + field + " exceeds chain finality")
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
			if err != nil || cp.BlockNumber != height {
				return fmt.Errorf("invalid omnichain %s header at requested height %d", field, height)
			}
			if head.Checkpoint > watermark {
				return pendingOmnichainRange("omnichain " + field + " exceeds global finalized watermark")
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
