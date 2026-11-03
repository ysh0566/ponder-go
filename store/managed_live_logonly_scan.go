package store

import (
	"context"
	"fmt"
	"math"
	"sort"

	"ponder.local/ponder-go/checkpoint"
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
	Lineage     []ManagedLiveHead
	Coverage    [][]managedLogOnlyFragment
	EmptyFrom   uint64
}

func logOnlySpanCovered(spans []SourceInterval, start, end uint64) bool {
	next := float64(start)
	for _, span := range spans {
		if math.IsNaN(span[0]) || math.IsNaN(span[1]) || math.IsInf(span[0], 0) || math.IsInf(span[1], 0) || math.Trunc(span[0]) != span[0] || math.Trunc(span[1]) != span[1] || span[0] < 0 || span[1] < span[0] {
			return false
		}
		if span[1] < next {
			continue
		}
		if span[0] > next {
			return false
		}
		if span[1] >= float64(end) {
			return true
		}
		next = span[1] + 1
	}
	return false
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
		if !ok || scan.FiltersHash != managedHash(filters) || len(scan.Coverage) != len(filters) || len(scan.Lineage) == 0 || scan.Lineage[0] != c.Cursors[id] || scan.Lineage[len(scan.Lineage)-1] != c.FinalizedHeads[id] {
			return nil, ErrManagedLiveBatchConflict
		}
		for i, h := range scan.Lineage {
			normalized, cp, err := normalizeLiveHead(h)
			if err != nil || normalized != h || cp.ChainID != id {
				return nil, ErrManagedLiveBatchConflict
			}
			if i > 0 {
				previous := scan.Lineage[i-1]
				prior, _ := checkpoint.Decode(previous.Checkpoint)
				if cp.BlockNumber != prior.BlockNumber+1 || cp.BlockTimestamp < prior.BlockTimestamp || h.ParentHash != previous.Hash {
					return nil, ErrManagedLiveBatchConflict
				}
			}
			if cp.BlockNumber == m.opts.EndBlocks[id] {
				ends[id] = h
			}
		}
		end, ok := ends[id]
		if !ok || c.HistoryCuts[id] != m.opts.EndBlocks[id] {
			return nil, ErrManagedLiveBatchConflict
		}
		if prior, ok := c.LogOnlyScanHeads[id]; ok && prior != end {
			return nil, ErrManagedLiveBatchConflict
		}
		for i, f := range filters {
			fragments, err := filter.GetFragments(f)
			if err != nil || len(scan.Coverage[i]) != len(fragments) {
				return nil, ErrManagedLiveBatchConflict
			}
			for j, fragment := range fragments {
				p := scan.Coverage[i][j]
				if p.ID != filter.EncodeFragment(fragment["fragment"].(filter.Object)) || !logOnlySpanCovered(p.Intervals, m.opts.runtimeStarts[id], m.opts.EndBlocks[id]) {
					return nil, ErrManagedLivePendingActions
				}
			}
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
func (d *ManagedLiveDomains) logOnlyScan(ctx context.Context, c ManagedLiveControl, lineage map[uint64][]ManagedLiveHead) (map[uint64]managedLogOnlyScan, error) {
	m := d.live()
	if !logOnlyOptions(m.opts) || len(lineage) != len(m.opts.Chains) {
		return nil, ErrManagedLivePendingActions
	}
	scans := map[uint64]managedLogOnlyScan{}
	source, err := d.storage.syncCache.SourceSyncStore(0)
	if err != nil {
		return nil, err
	}
	err = source.WithTransaction(ctx, func(s *SourceSyncStore) error {
		for _, chain := range m.opts.Chains {
			id := chain.ID
			filters := m.opts.runtimeLogFilters[id]
			intervals, err := s.GetIntervals(ctx, filters)
			if err != nil {
				return err
			}
			scan := managedLogOnlyScan{FiltersHash: managedHash(filters), Lineage: append([]ManagedLiveHead{}, lineage[id]...)}
			for _, f := range filters {
				parts, ok := intervals.Get(f)
				if !ok {
					return ErrManagedLivePendingActions
				}
				coverage := []managedLogOnlyFragment{}
				for _, p := range parts {
					spans := append([]SourceInterval{}, p.Intervals...)
					sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
					coverage = append(coverage, managedLogOnlyFragment{ID: filter.EncodeFragment(p.Fragment), Intervals: spans})
				}
				scan.Coverage = append(scan.Coverage, coverage)
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

func (d *ManagedLiveDomains) ActivateLogOnlyRuntime(ctx context.Context, id string, heads map[uint64]ManagedLiveHead, lineage map[uint64][]ManagedLiveHead) error {
	return d.setRuntimeServingWithScan(ctx, id, true, heads, lineage)
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
func logOnlySameLineage(scans map[uint64]managedLogOnlyScan, lineage map[uint64][]ManagedLiveHead) bool {
	if len(scans) != len(lineage) {
		return false
	}
	for id, s := range scans {
		rows, ok := lineage[id]
		if !ok || managedHash(rows) != managedHash(s.Lineage) {
			return false
		}
	}
	return true
}
