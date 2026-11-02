package runtime

import (
	"context"
	"fmt"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// DecodeSourceRealtimeEvent applies the same source normalization and real ABI
// decoder as historical extraction. The event is acquired by SourceRealtimeSync.
func DecodeSourceRealtimeEvent(chain protocol.Object, callbacks []protocol.Object, session *filter.Session, children *SourceHistoricalChildren, e SourceRealtimeEvent) ([]protocol.Object, error) {
	if e.Type != "block" {
		return nil, fmt.Errorf("a source block event is required")
	}
	block, err := protocol.SyncBlockToInternal(e.Block)
	if err != nil {
		return nil, err
	}
	data := store.SourceEventData{Blocks: []protocol.Object{block}}
	for _, v := range e.Transactions {
		v, err := protocol.SyncTransactionToInternal(v)
		if err != nil {
			return nil, err
		}
		data.Transactions = append(data.Transactions, v)
	}
	for _, v := range e.TransactionReceipts {
		v, err := protocol.SyncTransactionReceiptToInternal(v)
		if err != nil {
			return nil, err
		}
		data.TransactionReceipts = append(data.TransactionReceipts, v)
	}
	for _, v := range e.Logs {
		v, err := protocol.SyncLogToInternal(v)
		if err != nil {
			return nil, err
		}
		data.Logs = append(data.Logs, v)
	}
	txs := map[string]protocol.Object{}
	for _, v := range e.Transactions {
		txs[fmt.Sprint(v["hash"])] = v
	}
	for _, v := range e.Traces {
		v, err := protocol.SyncTraceToInternal(protocol.Object{"transactionHash": v.TransactionHash, "trace": v.Trace}, e.Block, txs[v.TransactionHash])
		if err != nil {
			return nil, err
		}
		data.Traces = append(data.Traces, v)
	}
	id, err := sourceEventChainID(protocol.Object{"chain": chain})
	if err != nil {
		return nil, err
	}
	raw, err := sourceLocalBuild(SourceLocalHistoricalOptions{ChainID: id, EventCallbacks: callbacks, FilterSession: session, Children: children}, data)
	if err != nil {
		return nil, err
	}
	return sourceHistoryDecodeSession(chain, callbacks, session, &protocol.Decoder{}, raw)
}

// AcquireManagedHistoricalBlock uses the actual persistent source acquisition
// and SQL extraction for one finalized block. The caller's source transaction
// atomically persists raw data before any business transaction starts.
func AcquireManagedHistoricalBlock(ctx context.Context, chain protocol.Object, callbacks []protocol.Object, session *filter.Session, children *SourceHistoricalChildren, syncer *SourceHistoricalSync, number uint64, expected filter.Object, s *store.SourceSyncStore) ([]protocol.Object, error) {
	id, err := sourceEventChainID(protocol.Object{"chain": chain})
	if err != nil {
		return nil, err
	}
	filters := []filter.Object{}
	for _, cb := range session.Callbacks(callbacks) {
		filters = append(filters, cb["filter"].(map[string]any))
	}
	span := store.SourceInterval{float64(number), float64(number)}
	cached, err := EmptySourceCachedIntervals(filters)
	if err != nil {
		return nil, err
	}
	required, _, err := RequiredSourceIntervals(filters, span, cached)
	if err != nil {
		return nil, err
	}
	_, factories, err := RequiredSourceIntervals(filters, span, cached)
	if err != nil {
		return nil, err
	}
	logs, err := syncer.SyncBlockRangeData(ctx, span, required, factories, s)
	if err != nil {
		return nil, err
	}
	actual, err := syncer.SyncBlockData(ctx, span, required, logs, s)
	if err != nil {
		return nil, err
	}
	if actual != nil {
		for _, k := range []string{"number", "timestamp", "hash", "parentHash"} {
			if fmt.Sprint(actual[k]) != fmt.Sprint(expected[k]) {
				return nil, fmt.Errorf("historical acquired header differs from selected identity: %s", k)
			}
		}
	}
	data, err := s.GetEventData(ctx, store.SourceEventDataArgs{FilterSession: session, Filters: filters, FromBlock: float64(number), ToBlock: float64(number), ChainID: id, Limit: 100000})
	if err != nil {
		return nil, err
	}
	identity, err := s.GetBlockIdentity(ctx, id, number)
	if err != nil {
		return nil, err
	}
	et, err := sourceHistoricalNumber(expected["timestamp"])
	if err != nil {
		return nil, err
	}
	if identity != nil && (identity.Number != number || identity.Timestamp != et || identity.Hash != fmt.Sprint(expected["hash"]) || identity.ParentHash != fmt.Sprint(expected["parentHash"])) {
		return nil, fmt.Errorf("persisted source block differs from acquired canonical identity")
	}
	for _, block := range data.Blocks {
		n, e := sourceLocalBlockQuantity(block["number"])
		t, te := sourceLocalBlockQuantity(block["timestamp"])
		if e != nil || te != nil || n != number || t != et || identity == nil || fmt.Sprint(block["hash"]) != identity.Hash {
			return nil, fmt.Errorf("projected source block differs from acquired identity")
		}
	}

	raw, err := sourceLocalBuild(SourceLocalHistoricalOptions{ChainID: id, EventCallbacks: callbacks, FilterSession: session, Children: children}, data)
	if err != nil {
		return nil, err
	}
	return sourceHistoryDecodeSession(chain, callbacks, session, &protocol.Decoder{}, raw)
}
