package runtime

import (
	"ponder.local/ponder-go/checkpoint"
)

// OrderedEvent is a native event plus its scheduler chain/cursor metadata.
// Event.Block retains raw RPC hex quantities; the partition cursor uses the
// exact same decoded values and full 75-digit end-of-block sentinel as source.
type OrderedEvent struct {
	ChainID    uint64
	Checkpoint string
	Event      Event
}
type EventGroup struct {
	Events     []OrderedEvent
	ChainID    uint64
	Checkpoint string
}

// SplitEvents preserves source consecutive-hash grouping and input order.
// It does not sort events or coalesce a later occurrence of an earlier hash.
func SplitEvents(events []OrderedEvent) ([]EventGroup, error) {
	result := []EventGroup{}
	for _, event := range events {
		if len(result) == 0 || result[len(result)-1].Events[0].Event.Block.Hash != event.Event.Block.Hash {
			n, e := Number(event.Event.Block.Number)
			if e != nil {
				return nil, e
			}
			ts, e := Number(event.Event.Block.Timestamp)
			if e != nil {
				return nil, e
			}
			cp, e := checkpoint.Encode(checkpoint.Block(ts, event.ChainID, n, true))
			if e != nil {
				return nil, e
			}
			result = append(result, EventGroup{Events: []OrderedEvent{}, ChainID: event.ChainID, Checkpoint: cp})
		}
		last := &result[len(result)-1]
		last.Events = append(last.Events, event)
	}
	return result, nil
}
