package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const lineagePageSize = 256

// ManagedLiveHeaderReader returns a header from the runner's authenticated
// acquisition. A receipt only retains the endpoints and digest. Its page rows
// live in the same business database transaction as the receipt.
type ManagedLiveHeaderReader func(chain, number uint64) (ManagedLiveHead, error)

type managedLineageRange struct {
	First  ManagedLiveHead `json:"first"`
	Last   ManagedLiveHead `json:"last"`
	Digest string          `json:"digest"`
}

type lineagePageReader func(first, last uint64) ([]ManagedLiveHead, error)
type lineagePageWriter func(first uint64, page []ManagedLiveHead) error

// walkLineageRange uses the same checks for the acquired input and for a cold
// database read. The page boundary does not reset the predecessor check.
func walkLineageRange(ctx context.Context, first, last ManagedLiveHead, read lineagePageReader, write lineagePageWriter) (managedLineageRange, error) {
	var result managedLineageRange
	start, low, err := normalizeLiveHead(first)
	end, high, e := normalizeLiveHead(last)
	if err != nil || e != nil || start != first || end != last || low.ChainID != high.ChainID || low.BlockNumber > high.BlockNumber {
		return result, ErrManagedLiveBatchConflict
	}
	digest := sha256.New()
	io.WriteString(digest, "ponder-go/finalized-header-range/v1\n")
	var previous ManagedLiveHead
	var timestamp uint64
	for n := low.BlockNumber; n <= high.BlockNumber; {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		through := min(n+lineagePageSize-1, high.BlockNumber)
		page, err := read(n, through)
		if err != nil {
			return result, err
		}
		if uint64(len(page)) != through-n+1 {
			return result, ErrManagedLiveBatchConflict
		}
		for i, head := range page {
			height := n + uint64(i)
			normalized, cp, err := normalizeLiveHead(head)
			if err != nil || normalized != head || cp.ChainID != low.ChainID || cp.BlockNumber != height {
				return result, ErrManagedLiveBatchConflict
			}
			if height == low.BlockNumber {
				if head != first {
					return result, ErrManagedLiveBatchConflict
				}
			} else if head.ParentHash != previous.Hash || cp.BlockTimestamp < timestamp {
				return result, ErrManagedLiveBatchConflict
			}
			io.WriteString(digest, head.Checkpoint)
			io.WriteString(digest, head.Hash)
			io.WriteString(digest, head.ParentHash)
			previous, timestamp = head, cp.BlockTimestamp
		}
		if write != nil {
			if err := write(n, page); err != nil {
				return result, err
			}
		}
		n = through + 1
	}
	if previous != last {
		return result, ErrManagedLiveBatchConflict
	}
	return managedLineageRange{First: first, Last: last, Digest: hex.EncodeToString(digest.Sum(nil))}, nil
}

func readLineageHeaders(chain uint64, get ManagedLiveHeaderReader) lineagePageReader {
	return func(first, last uint64) ([]ManagedLiveHead, error) {
		page := make([]ManagedLiveHead, 0, last-first+1)
		for n := first; n <= last; n++ {
			h, err := get(chain, n)
			if err != nil {
				return nil, err
			}
			page = append(page, h)
		}
		return page, nil
	}
}

func (s *Store) createLineagePages(ctx context.Context, tx Transaction) error {
	return tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+s.internal("_ponder_go_live_lineage_pages")+" (operation_id text NOT NULL,chain_id bigint NOT NULL,first_block bigint NOT NULL,headers jsonb NOT NULL CHECK (jsonb_typeof(headers)='array' AND jsonb_array_length(headers) BETWEEN 1 AND 256 AND octet_length(headers::text)<=98304),PRIMARY KEY(operation_id,chain_id,first_block))")
}

func (s *Store) writeLineageRange(ctx context.Context, tx Transaction, op string, first, last ManagedLiveHead, get ManagedLiveHeaderReader) (managedLineageRange, error) {
	_, cp, err := normalizeLiveHead(first)
	if err != nil || !liveID(op) || get == nil {
		return managedLineageRange{}, ErrManagedLiveBatchConflict
	}
	return walkLineageRange(ctx, first, last, readLineageHeaders(cp.ChainID, get), func(n uint64, page []ManagedLiveHead) error {
		raw, err := json.Marshal(page)
		if err != nil {
			return err
		}
		return tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_lineage_pages")+" (operation_id,chain_id,first_block,headers) VALUES ($1,$2,$3,$4::jsonb)", op, cp.ChainID, n, string(raw))
	})
}

func (s *Store) verifyLineageRange(ctx context.Context, q Querier, op string, expected managedLineageRange) error {
	_, cp, err := normalizeLiveHead(expected.First)
	if err != nil || !liveID(op) || !liveDigest(expected.Digest) {
		return ErrManagedLiveBatchConflict
	}
	_, end, err := normalizeLiveHead(expected.Last)
	if err != nil || end.ChainID != cp.ChainID || end.BlockNumber < cp.BlockNumber {
		return ErrManagedLiveBatchConflict
	}
	count, err := q.Query(ctx, "SELECT count(*)::text AS count FROM "+s.internal("_ponder_go_live_lineage_pages")+" WHERE operation_id=$1 AND chain_id=$2 AND first_block BETWEEN $3 AND $4", op, cp.ChainID, cp.BlockNumber, end.BlockNumber)
	if err != nil {
		return err
	}
	if len(count.Rows) != 1 || count.Rows[0]["count"] != fmt.Sprint(1+(end.BlockNumber-cp.BlockNumber)/lineagePageSize) {
		return ErrManagedLiveBatchConflict
	}
	got, err := walkLineageRange(ctx, expected.First, expected.Last, func(first, last uint64) ([]ManagedLiveHead, error) {
		r, err := q.Query(ctx, "SELECT headers::text AS headers FROM "+s.internal("_ponder_go_live_lineage_pages")+" WHERE operation_id=$1 AND chain_id=$2 AND first_block=$3", op, cp.ChainID, first)
		if err != nil {
			return nil, err
		}
		if len(r.Rows) != 1 {
			return nil, ErrManagedLiveBatchConflict
		}
		var page []ManagedLiveHead
		if err := decodeLiveJSON([]byte(fmt.Sprint(r.Rows[0]["headers"])), &page); err != nil {
			return nil, errors.Join(ErrManagedLiveBatchConflict, err)
		}
		return page, nil
	}, nil)
	if err != nil {
		return err
	}
	if got != expected {
		return ErrManagedLiveBatchConflict
	}
	return nil
}

// The configured cut can lie below the RPC finality anchor. Keeping the two
// ranges disjoint authenticates that interior cut without moving the cursor
// of a chain that produced no source group.
func (scan managedLogOnlyScan) pagedScanEnd(c ManagedLiveControl, id uint64) (ManagedLiveHead, error) {
	fail := ErrManagedLiveBatchConflict
	if len(scan.Lineage) != 0 || scan.Scanned == nil || scan.Scanned.First != c.Cursors[id] || !liveDigest(scan.Scanned.Digest) {
		return ManagedLiveHead{}, fail
	}
	end := scan.Scanned.Last
	normalized, at, err := normalizeLiveHead(end)
	if err != nil || end != normalized || at.ChainID != id || at.BlockNumber != c.HistoryCuts[id] {
		return ManagedLiveHead{}, fail
	}
	if end == c.FinalizedHeads[id] {
		if scan.FinalTail != nil {
			return ManagedLiveHead{}, fail
		}
	} else {
		tail := scan.FinalTail
		if tail == nil || tail.Last != c.FinalizedHeads[id] || !liveDigest(tail.Digest) {
			return ManagedLiveHead{}, fail
		}
		first, cp, err := normalizeLiveHead(tail.First)
		if err != nil || first != tail.First || cp.ChainID != id || cp.BlockNumber != at.BlockNumber+1 || cp.BlockTimestamp < at.BlockTimestamp || first.ParentHash != end.Hash {
			return ManagedLiveHead{}, fail
		}
	}
	return end, nil
}

func (m *ManagedLive) acquiredScanRanges(ctx context.Context, c ManagedLiveControl, lineage map[uint64][]ManagedLiveHead, get ManagedLiveHeaderReader) (map[uint64]managedLogOnlyScan, error) {
	scans := make(map[uint64]managedLogOnlyScan, len(m.opts.Chains))
	if !pagedLogOnlyOptions(m.opts) {
		if get != nil || len(lineage) != len(m.opts.Chains) {
			return nil, ErrManagedLiveBatchConflict
		}
		for id, heads := range lineage {
			scans[id] = managedLogOnlyScan{Lineage: append([]ManagedLiveHead{}, heads...)}
		}
		return scans, nil
	}
	if get == nil || lineage != nil {
		return nil, ErrManagedLiveBatchConflict
	}
	for _, chain := range m.opts.Chains {
		id := chain.ID
		cut := c.HistoryCuts[id]
		end, err := get(id, cut)
		if err != nil {
			return nil, err
		}
		page := readLineageHeaders(id, get)
		scanned, err := walkLineageRange(ctx, c.Cursors[id], end, page, nil)
		if err != nil {
			return nil, err
		}
		scan := managedLogOnlyScan{Scanned: &scanned}
		if end != c.FinalizedHeads[id] {
			start, err := get(id, cut+1)
			if err != nil {
				return nil, err
			}
			tail, err := walkLineageRange(ctx, start, c.FinalizedHeads[id], page, nil)
			if err != nil {
				return nil, err
			}
			scan.FinalTail = &tail
		}
		if _, err := scan.pagedScanEnd(c, id); err != nil {
			return nil, err
		}
		scans[id] = scan
	}
	return scans, nil
}

func (d *ManagedLiveDomains) ActivatePagedLogOnlyRuntime(ctx context.Context, id string, heads map[uint64]ManagedLiveHead, get ManagedLiveHeaderReader) error {
	return d.setRuntimeServingWithScan(ctx, id, true, heads, nil, get)
}

func samePagedScanInputs(a, b map[uint64]managedLogOnlyScan) bool {
	if len(a) != len(b) {
		return false
	}
	for id, x := range a {
		y, ok := b[id]
		if !ok || x.Scanned == nil || y.Scanned == nil || *x.Scanned != *y.Scanned || (x.FinalTail == nil) != (y.FinalTail == nil) {
			return false
		}
		if x.FinalTail != nil && *x.FinalTail != *y.FinalTail {
			return false
		}
	}
	return true
}

func (s *Store) writeScanPages(ctx context.Context, tx Transaction, op string, scans map[uint64]managedLogOnlyScan, get ManagedLiveHeaderReader) error {
	for _, scan := range scans {
		for _, r := range []*managedLineageRange{scan.Scanned, scan.FinalTail} {
			if r == nil {
				continue
			}
			got, err := s.writeLineageRange(ctx, tx, op, r.First, r.Last, get)
			if err != nil {
				return err
			}
			if got != *r {
				return ErrManagedLiveBatchConflict
			}
		}
	}
	return nil
}

// All physically retained pages must belong to one range declared by a
// durable operation. Range digests are bound by the operation's existing
// snapshot hash. Read the page contents in bounded chunks on every audit.
func (m *ManagedLive) auditLineagePages(ctx context.Context, q Querier, ops []managedLiveOperation) (map[ManagedLiveHead]ManagedLiveHead, error) {
	parents := make(map[ManagedLiveHead]ManagedLiveHead)
	s := m.owner.s
	var expectedPages uint64
	verify := func(op string, r managedLineageRange) error {
		if err := s.verifyLineageRange(ctx, q, op, r); err != nil {
			return err
		}
		_, first, _ := normalizeLiveHead(r.First)
		_, last, _ := normalizeLiveHead(r.Last)
		expectedPages += 1 + (last.BlockNumber-first.BlockNumber)/lineagePageSize
		return nil
	}
	for _, op := range ops {
		switch op.Kind {
		case "serve":
			var evidence managedRuntimeServe
			if err := decodeLiveJSON(op.Payload, &evidence); err != nil {
				return nil, err
			}
			for _, scan := range evidence.LogOnlyScan {
				for _, r := range []*managedLineageRange{scan.Scanned, scan.FinalTail} {
					if r != nil {
						if err := verify(op.ID, *r); err != nil {
							return nil, err
						}
					}
				}
			}
		case "batch":
			var e managedLiveBatchEvidence
			if err := decodeLiveJSON(op.Payload, &e); err != nil {
				return nil, err
			}
			for _, seg := range e.Segments {
				if seg.Block.RangeDigest == "" {
					continue
				}
				if err := verify(op.ID, seg.Block.rangeProof()); err != nil {
					return nil, err
				}
				if prior, ok := parents[seg.Block.Head]; ok && prior != seg.BeforeCursor {
					return nil, ErrManagedLiveBatchConflict
				}
				parents[seg.Block.Head] = seg.BeforeCursor
			}
		}
	}
	count, err := q.Query(ctx, "SELECT count(*)::text AS count FROM "+s.internal("_ponder_go_live_lineage_pages"))
	if err != nil {
		return nil, err
	}
	if len(count.Rows) != 1 || count.Rows[0]["count"] != fmt.Sprint(expectedPages) {
		return nil, ErrManagedLiveBatchConflict
	}
	return parents, nil
}

func (b ManagedLiveBlock) rangeProof() managedLineageRange {
	return managedLineageRange{First: b.RangeFirst, Last: b.Head, Digest: b.RangeDigest}
}

// BindLogOnlyRange records only the empty prefix and the event-bearing (or
// logical endpoint) block. No event inputs are synthesized for the prefix.
func (b *ManagedLiveBlock) BindLogOnlyRange(ctx context.Context, first ManagedLiveHead, get ManagedLiveHeaderReader) error {
	_, cp, err := normalizeLiveHead(first)
	if err != nil || get == nil {
		return ErrManagedLiveBatchConflict
	}
	r, err := walkLineageRange(ctx, first, b.Head, readLineageHeaders(cp.ChainID, get), nil)
	if err != nil {
		return err
	}
	b.RangeFirst, b.RangeDigest = r.First, r.Digest
	return nil
}

// Only inspect receipt fields in their typed positions. Journal rows can have
// user-defined column names and remain covered by their existing digest.
func rejectPagedReceipt(op managedLiveOperation) error {
	switch op.Kind {
	case "batch", "serve", "finalize", "recover", "reorg":
	default:
		return nil
	}
	type blockFields struct{ RangeFirst, RangeDigest json.RawMessage }
	type segment struct{ Block blockFields }
	var wire struct {
		GapScans                  json.RawMessage
		Batch                     struct{ Blocks []blockFields }
		Segments, Removed, Pruned []segment
		LogOnlyScan               map[string]struct{ ScannedRange, FinalTailRange json.RawMessage }
	}
	if err := json.Unmarshal(op.Payload, &wire); err != nil {
		return err
	}
	if wire.GapScans != nil {
		return ErrManagedLiveBatchConflict
	}
	for _, b := range wire.Batch.Blocks {
		if b.RangeFirst != nil || b.RangeDigest != nil {
			return ErrManagedLiveBatchConflict
		}
	}
	for _, rows := range [][]segment{wire.Segments, wire.Removed, wire.Pruned} {
		for _, r := range rows {
			if r.Block.RangeFirst != nil || r.Block.RangeDigest != nil {
				return ErrManagedLiveBatchConflict
			}
		}
	}
	for _, scan := range wire.LogOnlyScan {
		if scan.ScannedRange != nil || scan.FinalTailRange != nil {
			return ErrManagedLiveBatchConflict
		}
	}
	return nil
}
