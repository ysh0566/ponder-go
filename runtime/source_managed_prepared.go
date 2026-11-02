package runtime

import (
	"context"
	"fmt"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
	"strings"
)

type PreparedManagedSourceBlock struct {
	chain      protocol.Object
	callbacks  []protocol.Object
	session    *filter.Session
	children   *SourceHistoricalChildren
	id, number uint64
	expected   filter.Object
	data       sourceHistoricalAcquisition
	factories  []store.SourceFactoryChildren
}

// PrepareManagedSourceBlock does all RPC acquisition before taking the sync
// transaction. A slow provider cannot hold the shared sync connection while a
// different chain's completed block is being dispatched or refetched.
func PrepareManagedSourceBlock(ctx context.Context, chain protocol.Object, callbacks []protocol.Object, session *filter.Session, children *SourceHistoricalChildren, syncer *SourceHistoricalSync, number uint64, expected filter.Object) (*PreparedManagedSourceBlock, error) {
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
	prepared, err := syncer.prepareBlockRangeData(ctx, span, required, factories)
	if err != nil {
		return nil, err
	}
	data, err := fetchSourceHistoricalAcquisition(ctx, syncer.base.requester, syncer.base.actions, children, filters, number, prepared.logs, &rpc.ActionOptions{EthGetLogsBlockRange: syncer.base.options.EthGetLogsBlockRange, Logger: syncer.base.options.Logger}, true)
	if err != nil {
		return nil, err
	}
	if data.RawBlock == nil {
		return nil, fmt.Errorf("historical source returned no acquired header")
	}
	if err = managedSourceHeaderEqual(data.RawBlock, expected); err != nil {
		return nil, err
	}
	return &PreparedManagedSourceBlock{chain: chain, callbacks: callbacks, session: session, children: children, id: id, number: number, expected: expected, data: data, factories: prepared.children}, nil
}
func managedSourceHeaderEqual(actual, expected filter.Object) error {
	for _, k := range []string{"number", "timestamp"} {
		a, e := sourceHistoricalNumber(actual[k])
		b, e2 := sourceHistoricalNumber(expected[k])
		if e != nil || e2 != nil || a != b {
			return fmt.Errorf("historical acquired header differs from selected identity: %s", k)
		}
	}
	for _, k := range []string{"hash", "parentHash"} {
		if !strings.EqualFold(fmt.Sprint(actual[k]), fmt.Sprint(expected[k])) {
			return fmt.Errorf("historical acquired header differs from selected identity: %s", k)
		}
	}
	return nil
}
func (p *PreparedManagedSourceBlock) PersistAndDecode(ctx context.Context, s *store.SourceSyncStore) ([]protocol.Object, error) {
	if err := s.InsertChildAddresses(ctx, p.id, p.factories); err != nil {
		return nil, err
	}
	for _, write := range []func() error{func() error { return s.InsertBlocks(ctx, p.id, []filter.Object{p.data.RawBlock}) }, func() error { return s.InsertTransactions(ctx, p.id, p.data.RawTransactions) }, func() error { return s.InsertTransactionReceipts(ctx, p.id, p.data.RawTransactionReceipts) }, func() error { return s.InsertTraces(ctx, p.id, p.data.RawTraces) }, func() error { return s.InsertLogs(ctx, p.id, p.data.RawLogs) }} {
		if err := write(); err != nil {
			return nil, err
		}
	}
	filters := []filter.Object{}
	for _, cb := range p.session.Callbacks(p.callbacks) {
		filters = append(filters, cb["filter"].(map[string]any))
	}
	data, err := s.GetEventData(ctx, store.SourceEventDataArgs{FilterSession: p.session, Filters: filters, FromBlock: float64(p.number), ToBlock: float64(p.number), ChainID: p.id, Limit: 100000})
	if err != nil {
		return nil, err
	}
	identity, err := s.GetBlockIdentity(ctx, p.id, p.number)
	if err != nil {
		return nil, err
	}
	timestamp, err := sourceHistoricalNumber(p.expected["timestamp"])
	if err != nil {
		return nil, err
	}
	if identity == nil || identity.Number != p.number || identity.Timestamp != timestamp || !strings.EqualFold(identity.Hash, fmt.Sprint(p.expected["hash"])) || !strings.EqualFold(identity.ParentHash, fmt.Sprint(p.expected["parentHash"])) {
		return nil, fmt.Errorf("persisted source block differs from acquired canonical identity")
	}
	for _, b := range data.Blocks {
		n, e := sourceLocalBlockQuantity(b["number"])
		ts, e2 := sourceLocalBlockQuantity(b["timestamp"])
		if e != nil || e2 != nil || n != p.number || ts != timestamp || !strings.EqualFold(fmt.Sprint(b["hash"]), identity.Hash) {
			return nil, fmt.Errorf("projected source block differs from acquired identity")
		}
	}
	raw, err := sourceLocalBuild(SourceLocalHistoricalOptions{ChainID: p.id, EventCallbacks: p.callbacks, FilterSession: p.session, Children: p.children}, data)
	if err != nil {
		return nil, err
	}
	return sourceHistoryDecodeSession(p.chain, p.callbacks, p.session, &protocol.Decoder{}, raw)
}

func (p *PreparedManagedSourceBlock) DecodeInMemory() ([]protocol.Object, error) {
	raw, err := sourceLocalBuild(SourceLocalHistoricalOptions{FilterSession: p.session, ChainID: p.id, EventCallbacks: p.callbacks, Children: p.children}, p.data.SourceEventData)
	if err != nil {
		return nil, err
	}
	return sourceHistoryDecodeSession(p.chain, p.callbacks, p.session, &protocol.Decoder{}, raw)
}
