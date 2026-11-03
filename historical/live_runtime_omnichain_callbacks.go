package historical

import (
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
)

// A closing block callback authenticates a complete block boundary even when
// static log callbacks overlap or SQL pages contain several events per block.
func validateOmnichainCallbacks(callbacks []indexing.Object) error {
	var closing filter.Object
	for _, cb := range callbacks {
		f, ok := cb["filter"].(map[string]any)
		if !ok {
			return fmt.Errorf("omnichain callback filter missing")
		}
		if len(filter.GetFilterFactories(f)) != 0 {
			return fmt.Errorf("omnichain factory sources are not supported")
		}
		switch f["type"] {
		case "block":
			interval, ie := uintQuantity(f["interval"])
			offset, oe := uintQuantity(f["offset"])
			if closing != nil || cb["type"] != "block" || ie != nil || oe != nil || interval != 1 || offset != 0 {
				return fmt.Errorf("omnichain requires exactly one interval1 block callback per chain with offset0")
			}
			closing = f
		case "log":
			if cb["type"] != "contract" {
				return fmt.Errorf("omnichain logs require contract ABI callbacks")
			}
		default:
			return fmt.Errorf("omnichain supports a closing block callback and static contract logs")
		}
	}
	if closing == nil {
		return validateOmnichainLogOnlyCallbacks(callbacks)
	}
	start, se := uintQuantity(filter.FilterFromBlock(closing))
	end, ee := uintQuantity(filter.FilterToBlock(closing))
	if se != nil || ee != nil || start == 0 || end < start {
		return fmt.Errorf("omnichain block callback requires a finite startBlock>=1 range")
	}
	for _, cb := range callbacks {
		f := cb["filter"].(map[string]any)
		from, fe := uintQuantity(filter.FilterFromBlock(f))
		to, te := uintQuantity(filter.FilterToBlock(f))
		if fe != nil || te != nil || from < start || to > end || to < from {
			return fmt.Errorf("omnichain closing block callback must cover every log callback range")
		}
	}
	return nil
}

// Each physical block remains complete. Its closing callback is the only
// logical partial boundary accepted by the existing v11 receipt format.
func validateOmnichainBlockPartition(events []indexing.Object, head store.ManagedLiveHead) error {
	h, err := checkpoint.Decode(head.Checkpoint)
	if err != nil {
		return err
	}
	closing := checkpoint.Block(h.BlockTimestamp, h.ChainID, h.BlockNumber, true)
	closing.EventType = checkpoint.Blocks
	closing.EventIndex = 0
	blocks := 0
	previous := ""
	for _, event := range events {
		raw, ok := event["checkpoint"].(string)
		cp, e := checkpoint.Decode(raw)
		if !ok || e != nil || cp.ChainID != h.ChainID || cp.BlockNumber != h.BlockNumber || cp.BlockTimestamp != h.BlockTimestamp || raw < previous {
			return fmt.Errorf("omnichain block partition has invalid source event order or identity")
		}
		previous = raw
		kind, _ := event["type"].(string)
		switch kind {
		case "block":
			if cp != closing {
				return fmt.Errorf("omnichain block callback is not the canonical closing event")
			}
			blocks++
		case "log":
			if cp.EventType != checkpoint.Logs || raw >= checkpoint.MustEncode(closing) {
				return fmt.Errorf("omnichain log event lies outside its closing block")
			}
		default:
			return fmt.Errorf("unsupported event in finalized omnichain block partition")
		}
	}
	if blocks != 1 || len(events) == 0 || previous != checkpoint.MustEncode(closing) {
		return fmt.Errorf("omnichain block partition requires exactly one final closing block callback")
	}
	return nil
}

// A source build chooses one persistence grammar for all configured chains.
// Mixed closing-block and log-only chains are outside the log-only closure.
func validateOmnichainCallbackModes(groups [][]indexing.Object) error {
	closing, logOnly := false, false
	for _, callbacks := range groups {
		if len(callbacks) == 0 {
			return nil
		} // The caller retains the pinned empty-filter error.
		block := false
		for _, cb := range callbacks {
			f, ok := cb["filter"].(map[string]any)
			if !ok {
				return nil
			} // The per-chain validator reports the malformed filter.
			block = block || f["type"] == "block"
		}
		closing = closing || block
		logOnly = logOnly || !block
	}
	if closing && logOnly {
		return fmt.Errorf("mixed closing-block and log-only omnichain chains are not supported")
	}
	if logOnly {
		remaining := uint64(store.ManagedLiveOmnichainBlockLimit(len(groups)))
		for _, callbacks := range groups {
			if err := validateOmnichainLogOnlyCallbacks(callbacks); err != nil {
				return err
			}
			f := callbacks[0]["filter"].(map[string]any)
			from, _ := uintQuantity(filter.FilterFromBlock(f))
			to, _ := uintQuantity(filter.FilterToBlock(f))
			span := to - from + 1
			if span > remaining {
				return fmt.Errorf("log-only configured physical range exceeds atomic receipt budget")
			}
			remaining -= span
		}
	}
	return nil
}
func validateOmnichainLogOnlyCallbacks(callbacks []indexing.Object) error {
	if len(callbacks) == 0 {
		return fmt.Errorf("log-only omnichain requires static ABI callbacks")
	}
	var start, end uint64
	for i, cb := range callbacks {
		f := cb["filter"].(map[string]any)
		from, fe := uintQuantity(filter.FilterFromBlock(f))
		to, te := uintQuantity(filter.FilterToBlock(f))
		if cb["type"] != "contract" || f["type"] != "log" || fe != nil || te != nil || from == 0 || to < from {
			return fmt.Errorf("log-only omnichain requires finite static ABI log ranges with startBlock>=1")
		}
		if i == 0 {
			start, end = from, to
		} else if from != start || to != end {
			return fmt.Errorf("log-only omnichain callback ranges must match within each chain")
		}
	}
	return nil
}
