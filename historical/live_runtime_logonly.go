package historical

import (
	"context"
	"fmt"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/store"
)

func (r *RuntimeRunner) logOnlySources() (map[uint64][]filter.Object, map[uint64][]any) {
	if len(r.logOnlyChains) == 0 {
		return nil, nil
	}
	filters := map[uint64][]filter.Object{}
	abis := map[uint64][]any{}
	for i, c := range r.build.Chains {
		id := runtimeChainID(c)
		if !r.logOnlyChains[id] {
			filters[id] = nil // Explicitly covered by a closing block callback.
			continue
		}
		for _, cb := range r.build.EventCallbacks[i] {
			filters[id] = append(filters[id], cb["filter"].(map[string]any))
			abis[id] = append(abis[id], cb["abiItem"])
		}
	}
	return filters, abis
}

// An uncached factory scan may rediscover births before the durable cursor.
func (r *RuntimeRunner) logScanLineageStart(id, cursor uint64) uint64 {
	if r.logOnlyChains[id] && r.options.FactorySources {
		for i, chain := range r.build.Chains {
			if runtimeChainID(chain) != id || runtimeRPCCacheEnabled(chain) {
				continue
			}
			for _, cb := range r.build.EventCallbacks[i] {
				for _, factory := range filter.GetFilterFactories(cb["filter"].(filter.Object)) {
					start, _ := uintQuantity(filter.FilterFromBlock(factory))
					cursor = min(cursor, start)
				}
			}
		}
	}
	return cursor
}

// Storage proofs never reuse the preflight transport or a sealed fetch epoch.
func (r *RuntimeRunner) openLogScanRPC(ctx context.Context) (map[uint64]store.LogScanRPC, error) {
	r.sequence++
	requesters, err := r.options.Requesters(ctx, r.sequence)
	if err != nil {
		return nil, err
	}
	if len(requesters) != len(r.build.Chains) {
		return nil, fmt.Errorf("log scan requester count mismatch")
	}
	readers := make(map[uint64]store.LogScanRPC)
	acquisition := r.options.Historical.Acquisition
	for i, chain := range r.build.Chains {
		if !r.logOnlyChains[runtimeChainID(chain)] || runtimeRPCCacheEnabled(chain) {
			continue
		}
		id := runtimeChainID(chain)
		if requesters[i] == nil {
			return nil, fmt.Errorf("missing log scan requester for chain %d", id)
		}
		readers[id] = store.LogScanRPC{
			Actions: rpc.NewActions(requesters[i]),
			Options: rpc.ActionOptions{
				EthGetLogsBlockRange: acquisition.EthGetLogsBlockRange,
				Logger:               acquisition.Logger,
			},
		}
	}
	return readers, nil
}

func (r *RuntimeRunner) validateOmnichainPartition(id uint64, events []indexing.Object, head store.ManagedLiveHead) error {
	if !r.logOnlyChains[id] {
		return validateOmnichainBlockPartition(events, head)
	}
	h, e := checkpoint.Decode(head.Checkpoint)
	if e != nil {
		return e
	}
	previous := ""
	for _, event := range events {
		raw, ok := event["checkpoint"].(string)
		cp, ce := checkpoint.Decode(raw)
		if !ok || ce != nil || event["type"] != "log" || cp.EventType != checkpoint.Logs || cp.TransactionIndex == checkpoint.Max.TransactionIndex || cp.ChainID != h.ChainID || cp.BlockNumber != h.BlockNumber || cp.BlockTimestamp != h.BlockTimestamp || raw < previous {
			return fmt.Errorf("log-only partition differs from acquired source identity/order")
		}
		previous = raw
	}
	return nil
}
