package store

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/filter"
)

// A log filter and its address factory can start or end at different heights.
func logSourceBounds(f filter.Object) (uint64, uint64, error) {
	var from uint64
	var ok bool
	if v := f["fromBlock"]; v != nil && !filter.IsUndefined(v) {
		if from, ok = logOnlyQuantity(v); !ok {
			return 0, 0, ErrManagedLiveBatchConflict
		}
	}
	to, ok := logOnlyQuantity(f["toBlock"])
	if !ok || from > to {
		return 0, 0, ErrManagedLiveBatchConflict
	}
	return from, to, nil
}

type logFragmentRange struct {
	id       string
	from, to uint64
}

func logFragmentRanges(f filter.Object, start, end uint64) ([]logFragmentRange, error) {
	from, to, err := logSourceBounds(f)
	if err != nil {
		return nil, err
	}
	parts, err := filter.GetFragments(f)
	if err != nil {
		return nil, err
	}
	var ranges []logFragmentRange
	for _, part := range parts {
		ranges = append(ranges, logFragmentRange{filter.EncodeFragment(part["fragment"].(filter.Object)), max(from, start), min(to, end)})
	}
	// Births before a gap can make a child eligible inside it. The parent
	// proof therefore begins at the factory's own start, even on cold reopen.
	for _, factory := range filter.GetFilterFactories(f) {
		from, to, err := logSourceBounds(factory)
		if err != nil {
			return nil, err
		}
		for _, id := range filter.FactoryFragmentIDs(factory) {
			ranges = append(ranges, logFragmentRange{id, from, min(to, end)})
		}
	}
	return ranges, nil
}

type logFactoryChildren map[string][]filter.ChildAddress

func (children logFactoryChildren) matches(f, log filter.Object, number uint64) bool {
	factory, ok := f["address"].(map[string]any)
	if !ok {
		return true
	}
	id, _ := factory["id"].(string)
	return filter.IsAddressMatched(log["address"], float64(number), children[id])
}

func readLogFactoryChildren(ctx context.Context, filters []filter.Object, read func(context.Context, filter.Object) ([]filter.ChildAddress, error)) (logFactoryChildren, error) {
	children := logFactoryChildren{}
	for _, f := range filters {
		for _, factory := range filter.GetFilterFactories(f) {
			id, _ := factory["id"].(string)
			if _, ok := children[id]; ok {
				continue
			}
			entries, err := read(ctx, factory)
			if err != nil {
				return nil, err
			}
			children[id] = entries
		}
	}
	return children, nil
}

func (r LogScanRPC) readFactory(ctx context.Context, chain, through uint64, factory filter.Object, get ManagedLiveHeaderReader) ([]filter.ChildAddress, error) {
	from, to, err := logSourceBounds(factory)
	if err != nil {
		return nil, err
	}
	to = min(to, through)
	if from > to {
		return nil, nil
	}
	parent := factory["address"]
	if parent == nil {
		parent = filter.Undefined
	}
	query := filter.LogRangeRequest(filter.Object{"topic0": factory["eventSelector"]}, parent, from, to)
	births := map[string]uint64{}
	err = r.scanLogs(ctx, query, from, to, func(log, internal filter.Object, n uint64) error {
		if !filter.IsLogFactoryMatched(factory, internal) {
			return ErrManagedLiveBatchConflict
		}
		if err := checkScanLogHeader(chain, n, log, get); err != nil {
			return err
		}
		address, err := filter.GetSourceChildAddress(log, factory)
		if err != nil {
			if filter.IsUndefined(parent) {
				return nil
			}
			return err
		}
		if born, ok := births[address]; !ok || n < born {
			births[address] = n
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var children []filter.ChildAddress
	for _, address := range slices.Sorted(maps.Keys(births)) {
		children = append(children, filter.ChildAddress{Address: address, Block: births[address]})
	}
	return children, nil
}

func (r LogScanRPC) scanLogs(ctx context.Context, query filter.Object, from, to uint64, visit func(filter.Object, filter.Object, uint64) error) error {
	next := from
	for page, err := range r.Actions.GetLogsWithPagination(ctx, query, &r.Options) {
		if err != nil {
			return err
		}
		if page.FromBlock != next || page.ToBlock < next || page.ToBlock > to {
			return ErrManagedLiveBatchConflict
		}
		for _, log := range page.Logs {
			n, ok := logOnlyQuantity(log["blockNumber"])
			internal, err := events.SyncLogToInternal(log)
			if err != nil || !ok || n < next || n > page.ToBlock || log["removed"] != false {
				return fmt.Errorf("log outside requested canonical page: %w", ErrManagedLiveBatchConflict)
			}
			if err := visit(log, internal, n); err != nil {
				return err
			}
		}
		next = page.ToBlock + 1
	}
	if next != to+1 {
		return ErrManagedLivePendingActions
	}
	return nil
}

func checkScanLogHeader(chain, number uint64, log filter.Object, get ManagedLiveHeaderReader) error {
	head, err := get(chain, number)
	if err != nil {
		return err
	}
	_, cp, err := normalizeLiveHead(head)
	hash, ok := log["blockHash"].(string)
	_, data := log["data"].(string)
	if err != nil || cp.ChainID != chain || cp.BlockNumber != number || !ok || !strings.EqualFold(hash, head.Hash) || !data {
		return ErrManagedLiveBatchConflict
	}
	return nil
}
