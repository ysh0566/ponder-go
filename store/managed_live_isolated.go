package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// Isolated chains can finalize or discard interleaved segments independently.
// Each segment's original predecessor and journal boundary are authenticated
// when its batch is replayed. They may now belong to an archived peer segment.
func partitionIsolatedJournal(segments []ManagedLiveSegment, rows []managedLiveJournalEntry) (map[uint64][]managedLiveJournalEntry, error) {
	partition := make(map[uint64][]managedLiveJournalEntry, len(segments))
	var previous uint64
	for _, s := range segments {
		if s.Ordinal <= previous || s.Predecessor >= s.Ordinal || s.Journal.Count > uint64(len(rows)) {
			return nil, ErrManagedLiveBatchConflict
		}
		batch := rows[:s.Journal.Count]
		span, err := liveJournalSpan(s.Journal.Before, s.Block.Head, batch)
		if err != nil || span != s.Journal {
			return nil, fmt.Errorf("isolated block journal differs from its committed span")
		}
		partition[s.Ordinal] = batch
		rows = rows[s.Journal.Count:]
		previous = s.Ordinal
	}
	if len(rows) != 0 {
		return nil, fmt.Errorf("isolated journal contains rows without a segment")
	}
	return partition, nil
}

func segmentCheckpoints(segments []ManagedLiveSegment) map[string]bool {
	result := make(map[string]bool, len(segments))
	for _, segment := range segments {
		result[segment.Block.Head.Checkpoint] = true
	}
	return result
}

// Evidence replay and SQL must remove exactly the same physical execution set.
// A checkpoint includes chain ID and there is one active segment per block.
func retainIsolatedHistory(active []ManagedLiveSegment, rows []managedLiveJournalEntry, removed []ManagedLiveSegment) ([]ManagedLiveSegment, []managedLiveJournalEntry) {
	checkpoints := segmentCheckpoints(removed)
	active = slices.DeleteFunc(active, func(s ManagedLiveSegment) bool { return checkpoints[s.Block.Head.Checkpoint] })
	rows = slices.DeleteFunc(rows, func(r managedLiveJournalEntry) bool { return checkpoints[r.Checkpoint] })
	return active, rows
}

func (s *Store) deleteIsolatedSegments(ctx context.Context, tx Transaction, segments []ManagedLiveSegment) error {
	if len(segments) == 0 {
		return nil
	}
	type entry struct {
		Ordinal    uint64 `json:"ordinal"`
		Checkpoint string `json:"checkpoint"`
	}
	entries := make([]entry, len(segments))
	for i, seg := range segments {
		entries[i] = entry{seg.Ordinal, seg.Block.Head.Checkpoint}
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	const items = " FROM jsonb_to_recordset($1::jsonb) AS s(ordinal bigint, checkpoint text))"
	if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE checkpoint IN (SELECT s.checkpoint"+items, string(payload)); err != nil {
		return err
	}
	return tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_live_segments")+" WHERE ordinal IN (SELECT s.ordinal"+items, string(payload))
}
