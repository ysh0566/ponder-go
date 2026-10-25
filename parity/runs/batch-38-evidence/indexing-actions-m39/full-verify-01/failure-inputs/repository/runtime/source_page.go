package runtime

import (
	"context"
	"fmt"
	"strings"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// SourcePage contains decoded events from an owned source sync cache. Cursor is
// the source pagination boundary; fetching missing ranges and block provenance
// remain the scheduler's responsibility.
type SourcePage struct {
	Events []OrderedEvent
	Cursor float64
}

func (e *Engine) ReadSourcePage(ctx context.Context, source *store.SourceSyncStore, fromBlock, toBlock float64, limit int) (SourcePage, error) {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	if e.Config.SourcePlan == nil || source == nil {
		return SourcePage{}, fmt.Errorf("compiled source plan and source sync store required")
	}
	callbacks := []protocol.Object{}
	filters := []filter.Object{}
	children := map[string][]filter.ChildAddress{}
	for _, callback := range e.Config.SourcePlan.Callbacks {
		value := callback.Value()
		callbacks = append(callbacks, value)
		f := callback.Filter.Value()
		filters = append(filters, f)
		for _, factory := range filter.GetFilterFactories(f) {
			id, _ := factory["id"].(string)
			if _, exists := children[id]; exists {
				continue
			}
			addresses, err := source.GetChildAddresses(ctx, factory)
			if err != nil {
				return SourcePage{}, err
			}
			children[id] = []filter.ChildAddress{}
			for _, entry := range addresses.Entries {
				children[id] = append(children[id], filter.ChildAddress{Address: entry.Address, Block: entry.BlockNumber})
			}
		}
	}
	data, err := source.GetEventData(ctx, store.SourceEventDataArgs{Filters: filters, FromBlock: fromBlock, ToBlock: toBlock, ChainID: e.chain.ID, Limit: limit})
	if err != nil {
		return SourcePage{}, err
	}
	built, err := protocol.BuildEvents(protocol.Batch{EventCallbacks: callbacks, Blocks: data.Blocks, Transactions: data.Transactions, TransactionReceipts: data.TransactionReceipts, Logs: data.Logs, Traces: data.Traces, ChildAddresses: children, ChainID: e.chain.ID})
	if err != nil {
		return SourcePage{}, err
	}
	pendingEvents := []pending{}
	for _, raw := range built {
		callback := callbacks[raw.EventCallbackIndex]
		name, _ := callback["name"].(string)
		cursor, err := checkpoint.Decode(raw.Checkpoint)
		if err != nil {
			return SourcePage{}, err
		}
		hash, _ := raw.Block["hash"].(string)
		parent, _ := raw.Block["parentHash"].(string)
		bloom, _ := raw.Block["logsBloom"].(string)
		block := Block{Number: Hex(cursor.BlockNumber), Timestamp: Hex(cursor.BlockTimestamp), Hash: hash, ParentHash: parent, LogsBloom: bloom}
		normalized := raw
		event := Event{Name: name, Block: block, Transaction: raw.Transaction, Receipt: raw.TransactionReceipt, Trace: raw.Trace, Normalized: &normalized}
		if raw.Log != nil {
			topics := []string{}
			if values, ok := raw.Log["topics"].([]any); ok {
				for _, v := range values {
					if v == nil || filter.IsUndefined(v) {
						break
					}
					text, ok := v.(string)
					if !ok {
						return SourcePage{}, fmt.Errorf("cached source topic is not hex")
					}
					topics = append(topics, text)
				}
			}
			address, _ := raw.Log["address"].(string)
			data, _ := raw.Log["data"].(string)
			txHash, _ := raw.Transaction["hash"].(string)
			event.Log = &Log{BlockNumber: block.Number, Address: address, Topics: topics, Data: data, BlockHash: hash, TransactionHash: txHash, TransactionIndex: Hex(cursor.TransactionIndex), LogIndex: Hex(cursor.EventIndex)}
		}
		if callback["type"] == "account" && raw.Trace != nil {
			event.Transfer = map[string]any{"from": raw.Trace["from"], "to": raw.Trace["to"], "value": raw.Trace["value"]}
			event.Args = event.Transfer
		}
		if callback["filter"].(map[string]any)["hasTransactionReceipt"] != true {
			event.Receipt = nil
		}
		id := fmt.Sprintf("account:%d:%s:%s:%s", e.chain.ID, hash, name, raw.Checkpoint)
		if raw.Log != nil {
			sourceID, _ := callback["filter"].(map[string]any)["sourceId"].(string)
			id = fmt.Sprintf("log:%d:%s:%s:%s", e.chain.ID, hash, sourceID, event.Log.LogIndex)
		} else if callback["filter"].(map[string]any)["type"] == "block" {
			id = fmt.Sprintf("block:%d:%s:%s", e.chain.ID, hash, strings.TrimSuffix(name, ":block"))
		}
		pendingEvents = append(pendingEvents, pending{cursor: cursor, id: id, event: event, callback: callback})
	}
	decoded, err := e.decodePending(pendingEvents)
	if err != nil {
		return SourcePage{}, err
	}
	page := SourcePage{Events: []OrderedEvent{}, Cursor: data.Cursor}
	for _, event := range decoded {
		page.Events = append(page.Events, OrderedEvent{ChainID: e.chain.ID, Checkpoint: checkpoint.MustEncode(event.cursor), Event: event.event})
	}
	return page, nil
}

// DispatchSourceEvents uses the normal journal/checkpoint transaction boundary.
// It dispatches an already acquired page; it does not seal absent blocks or
// advance lifecycle finality beyond the supplied event groups.
func (e *Engine) DispatchSourceEvents(ctx context.Context, events []OrderedEvent) error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	if e.Config.SourcePlan == nil {
		return fmt.Errorf("compiled source plan required")
	}
	for _, event := range events {
		cp, err := checkpoint.Decode(event.Checkpoint)
		if err != nil {
			return err
		}
		if event.ChainID != e.chain.ID || cp.ChainID != e.chain.ID || event.Event.Normalized == nil {
			return fmt.Errorf("source event chain/checkpoint metadata invalid")
		}
	}
	if !e.options.DisableCache {
		if _, err := e.Store.EnableProtocolCache(ctx); err != nil {
			return err
		}
	}
	invoke := func(w *store.Writer, list []OrderedEvent) error {
		client := e.Client.WithCache(nil)
		if !e.options.DisableCache {
			client = e.Client.WithCache(w.SourceRPCRequestCache())
		}
		for _, event := range list {
			handler := e.Handlers[event.Event.Name]
			if handler == nil {
				continue
			}
			if err := handler(ctx, &Context{Event: event.Event, DB: w, Client: client, Chain: e.chain}); err != nil {
				return fmt.Errorf("%s: %w", event.Event.Name, err)
			}
		}
		return nil
	}
	if e.staged != nil {
		groups, err := SplitEvents(events)
		if err != nil {
			return err
		}
		for _, group := range groups {
			target, err := checkpoint.Decode(group.Checkpoint)
			if err != nil {
				return err
			}
			if _, err = e.staged.db.Actions().ProcessRuntimeBlock(ctx, target, !e.staged.realtime.Load(), func(w *store.Writer) error { return invoke(w, group.Events) }); err != nil {
				return err
			}
		}
		return nil
	}
	for _, ordered := range events {
		event := ordered.Event
		cp, _ := checkpoint.Decode(ordered.Checkpoint)
		id := fmt.Sprintf("account:%d:%s:%s:%s", e.chain.ID, event.Block.Hash, event.Name, ordered.Checkpoint)
		if event.Log != nil {
			index := event.Normalized.EventCallbackIndex
			if index < 0 || index >= len(e.Config.SourcePlan.Callbacks) {
				return fmt.Errorf("source callback index invalid")
			}
			sourceID, _ := e.Config.SourcePlan.Callbacks[index].Filter.Value()["sourceId"].(string)
			id = fmt.Sprintf("log:%d:%s:%s:%s", e.chain.ID, event.Block.Hash, sourceID, event.Log.LogIndex)
		} else if event.Normalized.Trace == nil && event.Transaction == nil {
			id = fmt.Sprintf("block:%d:%s:%s", e.chain.ID, event.Block.Hash, strings.TrimSuffix(event.Name, ":block"))
		}
		if err := e.Store.Apply(ctx, store.Event{ID: id, Checkpoint: cp, Name: event.Name}, func(w *store.Writer) error { return invoke(w, []OrderedEvent{ordered}) }); err != nil {
			return err
		}
	}
	return nil
}
