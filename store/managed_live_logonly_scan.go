package store

import (
	"context"
	"fmt"
	"math"
	"sort"

	"ponder.local/ponder-go/filter"
)

// Scan evidence is separate from source/business positions. An empty chain has
// no source group to commit; its Down checkpoint must not become a fake Up.
type managedLogOnlyFragment struct {
	ID        string
	Intervals []SourceInterval
}
type managedLogOnlyScan struct {
	FiltersHash string
	// v17 receipts encode this field as null. It participates in their stored hash.
	Lineage   []ManagedLiveHead
	Scanned   *managedLineageRange `json:"scannedRange,omitempty"`
	FinalTail *managedLineageRange `json:"finalTailRange,omitempty"`
	Coverage  [][]managedLogOnlyFragment
	EmptyFrom uint64
}

func logOnlySpanCovered(spans []SourceInterval, start, end uint64) bool {
	if start > end {
		return false
	}
	next := float64(start)
	covered := false
	for _, span := range spans {
		if math.IsNaN(span[0]) || math.IsNaN(span[1]) || math.IsInf(span[0], 0) || math.IsInf(span[1], 0) || math.Trunc(span[0]) != span[0] || math.Trunc(span[1]) != span[1] || span[0] < 0 || span[1] < span[0] {
			return false
		}
		if covered || span[1] < next {
			continue
		}
		if span[0] > next {
			return false
		}
		if span[1] >= float64(end) {
			covered = true
		} else {
			next = span[1] + 1
		}
	}
	return covered
}

func (m *ManagedLive) validateLogOnlyScan(c ManagedLiveControl, scans map[uint64]managedLogOnlyScan) (map[uint64]ManagedLiveHead, error) {
	if !logOnlyOptions(m.opts) || len(scans) != len(m.opts.Chains) {
		return nil, ErrManagedLivePendingActions
	}
	ends := map[uint64]ManagedLiveHead{}
	for _, chain := range m.opts.Chains {
		id := chain.ID
		scan, ok := scans[id]
		filters := m.opts.runtimeLogFilters[id]
		if !ok || scan.FiltersHash != managedHash(filters) || len(scan.Coverage) != len(filters) {
			return nil, ErrManagedLiveBatchConflict
		}
		end, err := scan.pagedScanEnd(c, id)
		if err != nil {
			return nil, err
		}
		ends[id] = end
		if c.HistoryCuts[id] != m.opts.EndBlocks[id] {
			return nil, ErrManagedLiveBatchConflict
		}
		if prior, ok := c.LogOnlyScanHeads[id]; ok && prior != end {
			return nil, ErrManagedLiveBatchConflict
		}
		if err := validateLogCoverage(filters, scan.Coverage, m.opts.runtimeStarts[id], m.opts.EndBlocks[id]); err != nil {
			return nil, err
		}
		from := uint64(0)
		if !ManagedLiveReachedBlock(c, id, m.opts.EndBlocks[id]) {
			var err error
			from, err = ManagedLiveNextBlock(c, id)
			if err != nil || from > m.opts.EndBlocks[id] {
				return nil, ErrManagedLiveBatchConflict
			}
		}
		if scan.EmptyFrom != from {
			return nil, ErrManagedLiveBatchConflict
		}
	}
	return ends, nil
}

// This reads the owned source cache after all admitted sync work has joined.
// Callers supply RPC header lineage, never coverage or an "empty" boolean.
func (d *ManagedLiveDomains) logOnlyScan(ctx context.Context, c ManagedLiveControl, scans map[uint64]managedLogOnlyScan) (map[uint64]managedLogOnlyScan, error) {
	m := d.live()
	if !logOnlyOptions(m.opts) || len(scans) != len(m.opts.Chains) {
		return nil, ErrManagedLivePendingActions
	}
	source, err := d.storage.syncCache.SourceSyncStore(0)
	if err != nil {
		return nil, err
	}
	err = source.WithTransaction(ctx, func(s *SourceSyncStore) error {
		for _, chain := range m.opts.Chains {
			id := chain.ID
			filters := m.opts.runtimeLogFilters[id]
			scan := scans[id]
			scan.FiltersHash = managedHash(filters)
			scan.Coverage, err = readLogCoverage(ctx, s, filters)
			if err != nil {
				return err
			}
			if !ManagedLiveReachedBlock(c, id, m.opts.EndBlocks[id]) {
				scan.EmptyFrom, err = ManagedLiveNextBlock(c, id)
				if err != nil {
					return err
				}
				// Only actual absence of remaining matched raw logs can close a chain
				// omitted by the coordinator. ABI-skipped nonempty tails stay unsupported.
				err = logOnlyEmptyTail(ctx, SourceEventDataArgs{Filters: filters, ChainID: id, FromBlock: float64(scan.EmptyFrom), ToBlock: float64(m.opts.EndBlocks[id]), Limit: 1}, s.GetEventData)
				if err != nil {
					return err
				}
			}
			scans[id] = scan
		}
		_, err := m.validateLogOnlyScan(c, scans)
		return err
	})
	return scans, err
}

func logOnlyEmptyTail(ctx context.Context, args SourceEventDataArgs, read func(context.Context, SourceEventDataArgs) (SourceEventData, error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := read(ctx, args)
		if err != nil {
			return err
		}
		if len(rows.Logs) != 0 {
			return fmt.Errorf("log-only scan has unprocessed source logs: %w", ErrManagedLivePendingActions)
		}
		if math.IsNaN(rows.Cursor) || math.IsInf(rows.Cursor, 0) || math.Trunc(rows.Cursor) != rows.Cursor || rows.Cursor < args.FromBlock || rows.Cursor > args.ToBlock {
			return ErrManagedLiveBatchConflict
		}
		if rows.Cursor == args.ToBlock {
			return nil
		}
		args.FromBlock = rows.Cursor + 1
	}
}
func validateLogCoverage(filters []filter.Object, coverage [][]managedLogOnlyFragment, start, end uint64) error {
	if len(coverage) != len(filters) {
		return ErrManagedLiveBatchConflict
	}
	for i, f := range filters {
		fragments, err := filter.GetFragments(f)
		if err != nil || len(coverage[i]) != len(fragments) {
			return ErrManagedLiveBatchConflict
		}
		for j, fragment := range fragments {
			p := coverage[i][j]
			if p.ID != filter.EncodeFragment(fragment["fragment"].(filter.Object)) || !logOnlySpanCovered(p.Intervals, start, end) {
				return ErrManagedLivePendingActions
			}
		}
	}

	return nil
}

func readLogCoverage(ctx context.Context, s *SourceSyncStore, filters []filter.Object) ([][]managedLogOnlyFragment, error) {
	intervals, err := s.GetIntervals(ctx, filters)
	if err != nil {
		return nil, err
	}
	var result [][]managedLogOnlyFragment
	for _, f := range filters {
		parts, ok := intervals.Get(f)
		if !ok {
			return nil, ErrManagedLivePendingActions
		}
		coverage := []managedLogOnlyFragment{}
		for _, p := range parts {
			spans := append([]SourceInterval{}, p.Intervals...)
			sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
			coverage = append(coverage, managedLogOnlyFragment{ID: filter.EncodeFragment(p.Fragment), Intervals: spans})
		}
		result = append(result, coverage)
	}

	return result, nil
}
