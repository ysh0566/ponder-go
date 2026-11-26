package historical

import (
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/store"
)

func (r *RuntimeRunner) logOnlySources() (map[uint64][]filter.Object, map[uint64][]any) {
	if !r.logOnly {
		return nil, nil
	}
	filters := map[uint64][]filter.Object{}
	abis := map[uint64][]any{}
	for i, c := range r.build.Chains {
		id := runtimeChainID(c)
		for _, cb := range r.build.EventCallbacks[i] {
			filters[id] = append(filters[id], cb["filter"].(map[string]any))
			abis[id] = append(abis[id], cb["abiItem"])
		}
	}
	return filters, abis
}
func (r *RuntimeRunner) validateOmnichainPartition(events []indexing.Object, head store.ManagedLiveHead) error {
	if !r.logOnly {
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
		if !ok || ce != nil || event["type"] != "log" || cp.EventType != checkpoint.Logs || cp.ChainID != h.ChainID || cp.BlockNumber != h.BlockNumber || cp.BlockTimestamp != h.BlockTimestamp || raw < previous {
			return fmt.Errorf("log-only partition differs from acquired source identity/order")
		}
		previous = raw
	}
	return nil
}
