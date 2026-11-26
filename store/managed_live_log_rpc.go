package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/rpc"
)

// LogScanRPC rechecks finalized static-log ranges without writing a sync cache.
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
			digest := managedHash(filters)
			if r.checkEmpty {
				digest, err = opts.logOnlyScanHash(ctx, SourceEventDataArgs{Filters: filters, ChainID: r.chain, FromBlock: float64(r.emptyFrom), ToBlock: float64(r.to), Limit: 1}, source.GetEventData)
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
	var scan managedGapScan
	skipped := false
	for _, f := range filters {
		start, _ := logOnlyQuantity(filter.FilterFromBlock(f))
		end, _ := logOnlyQuantity(filter.FilterToBlock(f))
		from, to := max(r.from, start), min(r.to, end)
		var spans []SourceInterval
		if from <= to {
			address, ok := f["address"]
			if !ok {
				address = filter.Undefined
			}
			query := filter.LogRangeRequest(f, address, from, to)
			next := from
			for page, err := range reader.Actions.GetLogsWithPagination(ctx, query, &reader.Options) {
				if err != nil {
					return managedGapScan{}, err
				}
				if page.FromBlock != next || page.ToBlock < next || page.ToBlock > to {
					return managedGapScan{}, ErrManagedLiveBatchConflict
				}
				for _, log := range page.Logs {
					number, ok := logOnlyQuantity(log["blockNumber"])
					internal, err := events.SyncLogToInternal(log)
					if err != nil || !ok || number < next || number > page.ToBlock || log["removed"] != false || !filter.IsLogFilterMatched(f, internal) {
						return managedGapScan{}, fmt.Errorf("log outside requested canonical filter or page: %w", ErrManagedLiveBatchConflict)
					}
					if r.checkEmpty && number >= r.emptyFrom {
						if err := verifyUncachedEmptyLog(r.chain, number, log, internal, callbacks, get); err != nil {
							return managedGapScan{}, err
						}
						skipped = true
					}
				}
				next = page.ToBlock + 1
			}
			if next != to+1 {
				return managedGapScan{}, ErrManagedLivePendingActions
			}
			spans = []SourceInterval{{float64(from), float64(to)}}
		}
		fragments, err := filter.GetFragments(f)
		if err != nil {
			return managedGapScan{}, err
		}
		coverage := make([]managedLogOnlyFragment, len(fragments))
		for j, part := range fragments {
			coverage[j] = managedLogOnlyFragment{ID: filter.EncodeFragment(part["fragment"].(filter.Object)), Intervals: spans}
		}
		scan.Coverage = append(scan.Coverage, coverage)
	}
	if skipped {
		scan.FiltersHash = managedHash(callbacks)
	} else {
		scan.FiltersHash = managedHash(filters)
	}
	return scan, nil
}

func verifyUncachedEmptyLog(chain, number uint64, log, internal filter.Object, callbacks []filter.Object, get ManagedLiveHeaderReader) error {
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
	// A log can match several callbacks. Check every matching ABI even if
	// a subsequent RPC query omits the same log from its response.
	var raw []events.RawEvent
	for i, callback := range callbacks {
		if filter.IsLogFilterMatched(callback["filter"].(filter.Object), internal) {
			raw = append(raw, events.RawEvent{ChainID: chain, EventCallbackIndex: i, Log: internal})
		}
	}
	if len(raw) == 0 {
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
