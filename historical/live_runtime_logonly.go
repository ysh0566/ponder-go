package historical

import (
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
)

func (r *RuntimeRunner) logOnlyFilters() map[uint64][]filter.Object {
	if !r.logOnly {
		return nil
	}
	out := map[uint64][]filter.Object{}
	for i, c := range r.build.Chains {
		id := runtimeChainID(c)
		for _, cb := range r.build.EventCallbacks[i] {
			out[id] = append(out[id], cb["filter"].(map[string]any))
		}
	}
	return out
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
