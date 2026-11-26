package store

import (
	"context"
	"github.com/ysh0566/ponder-go/checkpoint"
)

type managedGapScan struct {
	FiltersHash string
	Coverage    [][]managedLogOnlyFragment
}

func gapBounds(b ManagedLiveBlock) (chain, from, to uint64, err error) {
	_, first, err := normalizeLiveHead(b.RangeFirst)
	if err != nil {
		return 0, 0, 0, err
	}
	end, err := checkpoint.Decode(b.Head.Checkpoint)
	if err != nil || first.ChainID != end.ChainID || end.BlockNumber <= first.BlockNumber {
		return 0, 0, 0, ErrManagedLiveBatchConflict
	}
	to = end.BlockNumber
	if b.LogEventCount != 0 {
		to--
	}
	return first.ChainID, first.BlockNumber, to, nil
}

func validateGapScans(o ManagedLiveOptions, b ManagedLiveBatch, scans map[int]managedGapScan) error {
	count := 0
	for i, block := range b.Blocks {
		if block.RangeDigest == "" {
			continue
		}
		id, from, to, err := gapBounds(block)
		scan, ok := scans[i]
		if err != nil || o.runtimeLogFilters[id] == nil || !ok || !o.validLogScanHash(id, scan.FiltersHash) {
			return ErrManagedLiveBatchConflict
		}
		if err := checkEmptyBlockSchedules(o.runtimeLogFilters[id], from, to); err != nil {
			return err
		}
		if err := validateLogCoverage(o.runtimeLogFilters[id], scan.Coverage, from, to); err != nil {
			return err
		}
		count++
	}
	if count != len(scans) {
		return ErrManagedLiveBatchConflict
	}
	return nil
}

func (d *ManagedLiveDomains) RetainPagedLogOnlyBatch(ctx context.Context, b ManagedLiveBatch, get ManagedLiveHeaderReader) (*ManagedLiveAcquisition, error) {
	if !d.mu.TryLock() {
		return nil, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return nil, err
	}
	m := d.live()
	if !logOnlyOptions(m.opts) || get == nil {
		return nil, ErrManagedLiveBatchConflict
	}
	acquired, err := m.RetainBatch(ctx, b)
	if err != nil {
		return nil, err
	}
	var indexes []int
	var ranges []logScanRange
	for i, block := range b.Blocks {
		if block.RangeDigest == "" {
			continue
		}
		id, from, to, err := gapBounds(block)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, i)
		ranges = append(ranges, logScanRange{chain: id, from: from, to: to, checkEmpty: true, emptyFrom: from})
	}
	evidence, err := d.readLogScans(ctx, ranges, get)
	if err != nil {
		return nil, err
	}
	scans := make(map[int]managedGapScan, len(indexes))
	for j, i := range indexes {
		scans[i] = evidence[j]
	}
	if err := validateGapScans(m.opts, b, scans); err != nil {
		return nil, err
	}
	acquired.readHeaders, acquired.gapScans = get, scans
	return acquired, nil
}
