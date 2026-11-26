package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/filter"
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
	next := float64(start)
	// A filter outside the scanned interval has no coverage obligation.
	// Its stored interval records still have to be well formed.
	covered := start > end
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
		if !ok || !m.opts.validLogScanHash(id, scan.FiltersHash) || len(scan.Coverage) != len(filters) {
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

func (d *ManagedLiveDomains) logOnlyScan(ctx context.Context, c ManagedLiveControl, scans map[uint64]managedLogOnlyScan, get ManagedLiveHeaderReader) (map[uint64]managedLogOnlyScan, error) {
	m := d.live()
	if !logOnlyOptions(m.opts) || len(scans) != len(m.opts.Chains) {
		return nil, ErrManagedLivePendingActions
	}
	var ranges []logScanRange
	for _, chain := range m.opts.Chains {
		id := chain.ID
		r := logScanRange{chain: id, from: m.opts.runtimeStarts[id], to: m.opts.EndBlocks[id]}
		if !ManagedLiveReachedBlock(c, id, r.to) {
			from, err := ManagedLiveNextBlock(c, id)
			if err != nil {
				return nil, err
			}
			r.checkEmpty, r.emptyFrom = true, from
		}
		ranges = append(ranges, r)
	}
	evidence, err := d.readLogScans(ctx, ranges, get)
	if err != nil {
		return nil, err
	}
	for i, r := range ranges {
		scan := scans[r.chain]
		scan.Coverage, scan.FiltersHash = evidence[i].Coverage, evidence[i].FiltersHash
		scan.EmptyFrom = r.emptyFrom
		scans[r.chain] = scan
	}
	_, err = m.validateLogOnlyScan(c, scans)
	return scans, err
}

// The plain filter hash certifies a range without matching raw logs. If raw
// logs were skipped, the receipt binds the ordered ABI/filter pairs used by
// the real decoder. The two proofs share the same coverage and lineage checks.
func (o ManagedLiveOptions) validLogScanHash(id uint64, digest string) bool {
	callbacks := o.runtimeLogCallbacks[id]
	return digest == managedHash(o.runtimeLogFilters[id]) || (len(callbacks) > 0 && digest == managedHash(callbacks))
}

func logOnlyABICallbacks(filters map[uint64][]filter.Object, abis map[uint64][]any) (map[uint64][]filter.Object, error) {
	if len(abis) != len(filters) || len(filters) == 0 {
		return nil, ErrManagedLiveBatchConflict
	}
	result := make(map[uint64][]filter.Object, len(filters))
	for id, group := range filters {
		if len(abis[id]) != len(group) {
			return nil, ErrManagedLiveBatchConflict
		}
		for i, f := range group {
			raw, err := json.Marshal(abis[id][i])
			if err != nil {
				return nil, err
			}
			var item abi.Event
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, err
			}
			if item.Type != "event" || item.Name == "" {
				return nil, fmt.Errorf("invalid log-only event ABI for chain %d", id)
			}
			result[id] = append(result[id], filter.Object{"type": "contract", "filter": f, "abiItem": item})
		}
	}
	return result, nil
}

func (o ManagedLiveOptions) logOnlyScanHash(ctx context.Context, args SourceEventDataArgs, children logFactoryChildren, read func(context.Context, SourceEventDataArgs) (SourceEventData, error)) (string, error) {
	callbacks := o.runtimeLogCallbacks[args.ChainID]
	skipped := false
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// GetEventData may fetch a union of several source filters. Keep
		// their active set constant so a cached log outside its own
		// filter range cannot be mistaken for an unprocessed callback.
		page := args
		for _, f := range args.Filters {
			bounds, err := sourceRangeFromFilter(f)
			if err != nil {
				return "", err
			}
			if from := bounds.FromBlock; from != nil && *from > args.FromBlock {
				page.ToBlock = min(page.ToBlock, *from-1)
			}
			if to := bounds.ToBlock; to != nil && *to >= args.FromBlock {
				page.ToBlock = min(page.ToBlock, *to)
			}
		}
		rows, err := read(ctx, page)
		if err != nil {
			return "", err
		}
		if len(rows.Logs) != 0 {
			if err := verifyABISkippedPage(args.ChainID, callbacks, rows, children); err != nil {
				return "", err
			}
			skipped = true
		}
		if math.IsNaN(rows.Cursor) || math.IsInf(rows.Cursor, 0) || math.Trunc(rows.Cursor) != rows.Cursor || rows.Cursor < args.FromBlock || rows.Cursor > page.ToBlock {
			return "", ErrManagedLiveBatchConflict
		}
		if rows.Cursor == args.ToBlock {
			if skipped {
				return managedHash(callbacks), nil
			}
			return managedHash(args.Filters), nil
		}
		args.FromBlock = rows.Cursor + 1
	}
}

func verifyABISkippedPage(chainID uint64, callbacks []filter.Object, rows SourceEventData, children logFactoryChildren) error {
	if len(callbacks) == 0 {
		return ErrManagedLivePendingActions
	}
	// SQL returns candidate logs for a factory without restricting the emitter.
	// Discard a candidate only when every matching source excludes it by birth.
	if len(children) != 0 {
		eligible := make([]filter.Object, 0, len(rows.Logs))
		for _, log := range rows.Logs {
			n, ok := logOnlyQuantity(log["blockNumber"])
			address, hasAddress := log["address"].(string)
			if !ok || !hasAddress || address == "" {
				return ErrManagedLiveBatchConflict
			}
			matched := false
			for _, cb := range callbacks {
				f := cb["filter"].(filter.Object)
				if !filter.IsLogFilterMatched(f, log) {
					continue
				}
				matched = true
				if children.matches(f, log, n) {
					eligible = append(eligible, log)
					break
				}
			}
			if !matched {
				return ErrManagedLiveBatchConflict
			}
		}
		rows.Logs = eligible
	}
	raw, err := events.BuildEvents(events.Batch{ChildAddresses: children, ChainID: chainID, EventCallbacks: callbacks, Blocks: rows.Blocks, Transactions: rows.Transactions, TransactionReceipts: rows.TransactionReceipts, Logs: rows.Logs})
	if err != nil {
		return err
	}
	// A single log may match several callbacks. Account for every log
	// before allowing its range to close.
	distinct := 0
	for i, event := range raw {
		height, ok := logOnlyQuantity(event.Log["blockNumber"])
		block, hasBlock := logOnlyQuantity(event.Block["number"])
		_, hasData := event.Log["data"].(string)
		if !ok || !hasBlock || block != height || !hasData {
			return ErrManagedLiveBatchConflict
		}
		if i == 0 || raw[i-1].Checkpoint != event.Checkpoint {
			distinct++
		}
	}
	if distinct != len(rows.Logs) {
		return ErrManagedLiveBatchConflict
	}
	var decoder events.Decoder
	decoded, err := decoder.DecodeEvents(nil, callbacks, raw)
	if err != nil {
		return err
	}
	if len(decoded) != 0 {
		return fmt.Errorf("log-only scan has unprocessed source logs: %w", ErrManagedLivePendingActions)
	}
	return nil
}

func validateLogCoverage(filters []filter.Object, coverage [][]managedLogOnlyFragment, start, end uint64) error {
	if start > end || len(coverage) != len(filters) {
		return ErrManagedLiveBatchConflict
	}
	for i, f := range filters {
		ranges, err := logFragmentRanges(f, start, end)
		if err != nil || len(coverage[i]) != len(ranges) {
			return ErrManagedLiveBatchConflict
		}
		for j, part := range ranges {
			p := coverage[i][j]
			if p.ID != part.id || !logOnlySpanCovered(p.Intervals, part.from, part.to) {
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
		var coverage []managedLogOnlyFragment
		for _, source := range append([]filter.Object{f}, filter.GetFilterFactories(f)...) {
			parts, ok := intervals.Get(source)
			if !ok {
				return nil, ErrManagedLivePendingActions
			}
			for _, p := range parts {
				spans := append([]SourceInterval{}, p.Intervals...)
				sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
				coverage = append(coverage, managedLogOnlyFragment{ID: filter.EncodeFragment(p.Fragment), Intervals: spans})
			}
		}
		result = append(result, coverage)
	}

	return result, nil
}
