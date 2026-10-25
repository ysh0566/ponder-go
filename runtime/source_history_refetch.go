package runtime

import (
	"context"
	"fmt"
	"math"
	stdruntime "runtime"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// SourceHistoryEventStore is implemented by the production SourceSyncStore and
// its transaction-bound handle. Refetching reads persisted source data rather
// than requesting an RPC provider or replaying decoded input event bodies.
type SourceHistoryEventStore interface {
	GetEventData(context.Context, store.SourceEventDataArgs) (store.SourceEventData, error)
}

type SourceHistoryRefetchOptions struct {
	Chain          protocol.Object
	EventCallbacks []protocol.Object
	Children       *SourceHistoricalChildren
	Events         []protocol.Object
	Store          SourceHistoryEventStore
}

// RefetchLocalSourceEvents mirrors refetchLocalEvents: query all callback
// filters over the first/last input block range, use the input event count as
// page limit, and rebuild every matching raw event. It deliberately does not
// filter by input event checkpoint or event identity.
func RefetchLocalSourceEvents(ctx context.Context, o SourceHistoryRefetchOptions) ([]protocol.RawEvent, error) {
	if o.Store == nil || o.Children == nil {
		return nil, fmt.Errorf("source historical refetch store and children required")
	}
	if len(o.Events) == 0 {
		return nil, fmt.Errorf("source historical refetch requires at least one event")
	}
	chainID, err := sourceEventChainID(protocol.Object{"chain": o.Chain})
	if err != nil {
		return nil, err
	}
	from, err := sourceEventCheckpoint(o.Events[0])
	if err != nil {
		return nil, err
	}
	to, err := sourceEventCheckpoint(o.Events[len(o.Events)-1])
	if err != nil {
		return nil, err
	}
	first, err := checkpoint.Decode(from)
	if err != nil {
		return nil, err
	}
	last, err := checkpoint.Decode(to)
	if err != nil {
		return nil, err
	}
	filters := make([]filter.Object, len(o.EventCallbacks))
	for i, callback := range o.EventCallbacks {
		f, ok := callback["filter"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("source historical refetch callback filter required")
		}
		filters[i] = f
	}
	var result []protocol.RawEvent
	for cursor, end := float64(first.BlockNumber), float64(last.BlockNumber); cursor <= end; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := o.Store.GetEventData(ctx, store.SourceEventDataArgs{Filters: filters, FromBlock: cursor, ToBlock: end, ChainID: chainID, Limit: len(o.Events)})
		if err != nil {
			return nil, err
		}
		if math.IsNaN(data.Cursor) || math.IsInf(data.Cursor, 0) || data.Cursor < cursor {
			return nil, fmt.Errorf("source historical refetch cursor did not advance")
		}
		raw, err := sourceLocalBuild(SourceLocalHistoricalOptions{ChainID: chainID, EventCallbacks: o.EventCallbacks, Children: o.Children}, data)
		if err != nil {
			return nil, err
		}
		// The source yields to the event loop between pages. No database read or
		// transaction is held across this scheduling point.
		stdruntime.Gosched()
		if result == nil {
			result = raw
		} else {
			result = append(result, raw...)
		}
		cursor = data.Cursor + 1
	}
	return result, nil
}

// RefetchHistoricalSourceEvents refetches chains in indexing-build order and
// writes freshly decoded events into the input's chain positions. It preserves
// the source's positional behavior even if decoding drops events: unmatched
// output slots stay nil (the source returns sparse array slots), and surplus
// decoded values do not add new slots. Input checkpoint matching is not used.
func RefetchHistoricalSourceEvents(ctx context.Context, chains []SourceHistoricalChain, events []protocol.Object, syncStore SourceHistoryEventStore, decoder *protocol.Decoder) ([]protocol.Object, error) {
	if decoder == nil {
		decoder = &protocol.Decoder{}
	}
	result := make([]protocol.Object, len(events))
	for _, chain := range chains {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chainID, err := sourceEventChainID(protocol.Object{"chain": chain.Chain})
		if err != nil {
			return nil, err
		}
		chainEvents := []protocol.Object{}
		positions := []int{}
		for i, event := range events {
			id, err := sourceEventChainID(event)
			if err != nil {
				return nil, err
			}
			if id == chainID {
				chainEvents = append(chainEvents, event)
				positions = append(positions, i)
			}
		}
		if len(chainEvents) == 0 {
			continue
		}
		raw, err := RefetchLocalSourceEvents(ctx, SourceHistoryRefetchOptions{Chain: chain.Chain, EventCallbacks: chain.Local.EventCallbacks, Children: chain.Local.Children, Events: chainEvents, Store: syncStore})
		if err != nil {
			return nil, err
		}
		if raw == nil {
			// A descending input range leaves the source's local events value
			// undefined, and decodeEvents then rejects it as non-iterable.
			return nil, fmt.Errorf("source historical refetch raw events are undefined")
		}
		decoded, err := decoder.DecodeEvents(chain.Chain, chain.Local.EventCallbacks, raw)
		if err != nil {
			return nil, err
		}
		for i := 0; i < len(positions) && i < len(decoded); i++ {
			result[positions[i]] = decoded[i]
		}
	}
	return result, nil
}
