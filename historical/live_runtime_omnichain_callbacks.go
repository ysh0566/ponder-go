package historical

import (
	"fmt"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/store"
)

// A single covering, every-block callback can use dense block receipts. Other
// layouts prove their empty heights and commit only actual callback positions.
func omnichainDenseCallbacks(callbacks []indexing.Object) (bool, error) {
	if len(callbacks) == 0 {
		return false, fmt.Errorf("omnichain requires event callbacks")
	}
	blockSources := map[string]bool{}
	var closing filter.Object
	var first, last uint64
	for i, cb := range callbacks {
		f, ok := cb["filter"].(map[string]any)
		if !ok {
			return false, fmt.Errorf("omnichain callback filter missing")
		}
		from, fe := uintQuantity(filter.FilterFromBlock(f))
		to, te := uintQuantity(filter.FilterToBlock(f))
		if fe != nil || te != nil || to < from {
			return false, fmt.Errorf("omnichain requires finite event ranges with startBlock <= endBlock")
		}
		if i == 0 || from < first {
			first = from
		}
		last = max(last, to)
		switch f["type"] {
		case "block":
			period, validPeriod := store.RuntimeBlockPeriod(f["interval"])
			offset, oe := uintQuantity(f["offset"])
			if cb["type"] != "block" || !validPeriod || oe != nil || offset >= period {
				return false, fmt.Errorf("invalid omnichain block interval or offset")
			}
			name, _ := f["sourceId"].(string)
			if name == "" || blockSources[name] {
				return false, fmt.Errorf("missing or duplicate omnichain block source %q", name)
			}
			blockSources[name] = true
			if period == 1 {
				closing = f
			}
		case "log":
			if cb["type"] != "contract" {
				return false, fmt.Errorf("omnichain logs require contract ABI callbacks")
			}
		case "transaction", "transfer":
			if cb["type"] != "account" {
				return false, fmt.Errorf("omnichain transactions and transfers require account callbacks")
			}
		case "trace":
			if cb["type"] != "contract" || f["callType"] != "CALL" {
				return false, fmt.Errorf("omnichain traces require a contract CALL callback")
			}
		default:
			return false, fmt.Errorf("unsupported finalized omnichain callback type %v", f["type"])
		}
	}
	return len(blockSources) == 1 && closing != nil && filter.FilterFromBlock(closing) == float64(first) && filter.FilterToBlock(closing) == float64(last), nil
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
	closingKey := checkpoint.MustEncode(closing)
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
		if kind != "block" && (cp.TransactionIndex == checkpoint.Max.TransactionIndex || raw >= closingKey) {
			return fmt.Errorf("omnichain %s event lies outside its closing block", kind)
		}
		if !cp.MatchesEvent(kind) {
			return fmt.Errorf("invalid omnichain %s checkpoint", kind)
		}
		if kind == "block" {
			blocks++
		}
	}
	if blocks != 1 || len(events) == 0 || previous != closingKey {
		return fmt.Errorf("omnichain block partition requires exactly one final closing block callback")
	}
	return nil
}
