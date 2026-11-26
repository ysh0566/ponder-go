package store

import (
	"context"
	"fmt"

	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/rpc"
)

// LogScanRPC rechecks finalized log and account ranges without writing a sync cache.
// The reader belongs to one chain and one scan.
type LogScanRPC struct {
	Actions *rpc.Actions
	Options rpc.ActionOptions
}

type logScanRange struct {
	chain, from, to uint64
	checkEmpty      bool
	emptyFrom       uint64
}

// Fetch before opening the cache transaction. Cached chains still read their
// coverage and their empty ranges from one consistent sync-store transaction.
func (d *ManagedLiveDomains) readLogScans(ctx context.Context, ranges []logScanRange, get ManagedLiveHeaderReader) ([]managedGapScan, error) {
	opts := d.live().opts
	out := make([]managedGapScan, len(ranges))
	var cached []int
	var readers map[uint64]LogScanRPC
	for i, r := range ranges {
		if runtimeRPCCacheEnabled(opts, r.chain) {
			cached = append(cached, i)
			continue
		}
		if readers == nil {
			if d.openLogScanRPC == nil {
				return nil, ErrManagedLivePendingActions
			}
			var err error
			readers, err = d.openLogScanRPC(ctx)
			if err != nil {
				return nil, err
			}
		}
		reader := readers[r.chain]
		if reader.Actions == nil || get == nil {
			return nil, ErrManagedLivePendingActions
		}
		scan, err := reader.scan(ctx, opts, r, get)
		if err != nil {
			return nil, err
		}
		out[i] = scan
	}
	if len(cached) == 0 {
		return out, nil
	}
	cache, err := d.storage.syncCache.SourceSyncStore(0)
	if err != nil {
		return nil, err
	}
	err = cache.WithTransaction(ctx, func(source *SourceSyncStore) error {
		for _, i := range cached {
			r := ranges[i]
			filters := opts.runtimeLogFilters[r.chain]
			coverage, err := readLogCoverage(ctx, source, filters)
			if err != nil {
				return err
			}
			digest := opts.scanFilterHash(r.chain, filters)
			if r.checkEmpty {
				children, err := readLogFactoryChildren(ctx, filters, func(ctx context.Context, factory filter.Object) ([]filter.ChildAddress, error) {
					rows, err := source.GetChildAddresses(ctx, factory)
					if err != nil {
						return nil, err
					}
					entries := make([]filter.ChildAddress, len(rows.Entries))
					for i, row := range rows.Entries {
						entries[i] = filter.ChildAddress{Address: row.Address, Block: row.BlockNumber}
					}
					return entries, nil
				})
				if err != nil {
					return err
				}
				digest, err = opts.logOnlyScanHash(ctx, SourceEventDataArgs{Filters: filters, ChainID: r.chain, FromBlock: float64(r.emptyFrom), ToBlock: float64(r.to), Limit: 1}, children, source.GetEventData)
				if err != nil {
					return err
				}
			}
			out[i] = managedGapScan{FiltersHash: digest, Coverage: coverage}
		}
		return nil
	})
	return out, err
}

func (reader LogScanRPC) scan(ctx context.Context, opts ManagedLiveOptions, r logScanRange, get ManagedLiveHeaderReader) (managedGapScan, error) {
	filters := opts.runtimeLogFilters[r.chain]
	callbacks := opts.runtimeLogCallbacks[r.chain]
	children, err := readLogFactoryChildren(ctx, filters, func(ctx context.Context, factory filter.Object) ([]filter.ChildAddress, error) {
		return reader.readFactory(ctx, r.chain, r.to, factory, get)
	})
	if err != nil {
		return managedGapScan{}, err
	}
	if err := reader.scanBlockRanges(ctx, r, blockScanCallbacks(filters, callbacks), children, get); err != nil {
		return managedGapScan{}, err
	}
	var scan managedGapScan
	skipped := false
	for _, f := range filters {
		start, end, err := logSourceBounds(f)
		if err != nil {
			return managedGapScan{}, err
		}
		from, to := max(r.from, start), min(r.to, end)
		if f["type"] == "log" && from <= to {
			queries, err := filter.MergeLogFiltersToRequests([]filter.RequiredInterval{{Filter: f, Interval: [2]int64{int64(from), int64(to)}}}, children, filter.AddressChunkSize)
			if err != nil {
				return managedGapScan{}, err
			}
			for _, request := range queries {
				query := request["params"].([]any)[0].(filter.Object)
				err := reader.scanLogs(ctx, filter.RPCValue(query).(filter.Object), from, to, func(log, internal filter.Object, number uint64) error {
					if !filter.IsLogFilterMatched(f, internal) || !filter.IsValueMatched(query["address"], internal["address"]) {
						return ErrManagedLiveBatchConflict
					}
					if r.checkEmpty && number >= r.emptyFrom {
						if err := verifyUncachedEmptyLog(r.chain, number, log, internal, callbacks, children, get); err != nil {
							return err
						}
						skipped = true
					}
					return nil
				})
				if err != nil {
					return managedGapScan{}, err
				}
			}
		}
		ranges, err := logFragmentRanges(f, r.from, r.to)
		if err != nil {
			return managedGapScan{}, err
		}
		var coverage []managedLogOnlyFragment
		for _, part := range ranges {
			var spans []SourceInterval
			if part.from <= part.to {
				spans = []SourceInterval{{float64(part.from), float64(part.to)}}
			}
			coverage = append(coverage, managedLogOnlyFragment{ID: part.id, Intervals: spans})
		}
		scan.Coverage = append(scan.Coverage, coverage)
	}
	if skipped {
		scan.FiltersHash = managedHash(callbacks)
	} else {
		scan.FiltersHash = opts.scanFilterHash(r.chain, filters)
	}
	return scan, nil
}

func verifyUncachedEmptyLog(chain, number uint64, log, internal filter.Object, callbacks []filter.Object, children logFactoryChildren, get ManagedLiveHeaderReader) error {
	if err := checkScanLogHeader(chain, number, log, get); err != nil {
		return err
	}
	// A log can match several callbacks. Check every matching ABI even if
	// a subsequent RPC query omits the same log from its response.
	var raw []events.RawEvent
	for i, callback := range callbacks {
		f := callback["filter"].(filter.Object)
		if f["type"] == "log" && filter.IsLogFilterMatched(f, internal) && children.matches(f, internal, number) {
			raw = append(raw, events.RawEvent{ChainID: chain, EventCallbackIndex: i, Log: internal})
		}
	}
	if len(raw) == 0 {
		// A factory query may include logs from before birth, or use only
		// topics when many children are registered.
		if len(callbacks) > 0 && len(children) > 0 {
			return nil
		}
		return ErrManagedLivePendingActions
	}
	var decoder events.Decoder
	decoded, err := decoder.DecodeEvents(nil, callbacks, raw)
	if err != nil {
		return err
	}
	if len(decoded) != 0 {
		return fmt.Errorf("uncached log scan has an unprocessed callback: %w", ErrManagedLivePendingActions)
	}
	return nil
}
