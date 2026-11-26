package store

import (
	"slices"
	"testing"
)

func TestIsolatedJournalAfterPeerRemoval(t *testing.T) {
	first := liveSegmentsUnitBlock(1, 5, "")
	empty := liveSegmentsUnitBlock(1, 6, first.Head.Hash)
	last := liveSegmentsUnitBlock(1, 7, empty.Head.Hash)
	row1 := liveSegmentsUnitJournal(11, first, "null", 0)
	row2 := liveSegmentsUnitJournal(31, last, "null", 0)
	makeSegment := func(ordinal, predecessor uint64, before int64, block ManagedLiveBlock, rows ...managedLiveJournalEntry) ManagedLiveSegment {
		t.Helper()
		span, err := liveJournalSpan(before, block.Head, rows)
		if err != nil {
			t.Fatal(err)
		}
		return ManagedLiveSegment{Ordinal: ordinal, Predecessor: predecessor, Block: block, Journal: span}
	}
	// Ordinals 2 and 4 belonged to the peer; their journal is archived.
	segments := []ManagedLiveSegment{makeSegment(1, 0, 0, first, row1), makeSegment(3, 2, 17, empty), makeSegment(5, 4, 29, last, row2)}
	rows := []managedLiveJournalEntry{row1, row2}
	got, err := partitionIsolatedJournal(segments, rows)
	if err != nil || len(got) != 3 || len(got[1]) != 1 || len(got[3]) != 0 || len(got[5]) != 1 {
		t.Fatalf("isolated partition: %v %v", got, err)
	}
	if _, err := partitionLiveJournal(segments, rows, 0); err == nil {
		t.Fatal("global ordering accepted a missing predecessor")
	}
	for name, change := range map[string]func([]ManagedLiveSegment, []managedLiveJournalEntry){
		"missing row": func(s []ManagedLiveSegment, _ []managedLiveJournalEntry) { s[2].Journal.Count++ },
		"digest":      func(s []ManagedLiveSegment, _ []managedLiveJournalEntry) { s[1].Journal.Digest = s[0].Journal.Digest },
		"predecessor": func(s []ManagedLiveSegment, _ []managedLiveJournalEntry) { s[2].Predecessor = s[2].Ordinal },
		"ordinal":     func(s []ManagedLiveSegment, _ []managedLiveJournalEntry) { s[1].Ordinal = s[0].Ordinal },
		"chain":       func(_ []ManagedLiveSegment, r []managedLiveJournalEntry) { r[1].ChainID = 2 },
		"unbound row": func(s []ManagedLiveSegment, _ []managedLiveJournalEntry) { s[2] = makeSegment(5, 4, 29, last) },
	} {
		t.Run(name, func(t *testing.T) {
			s, r := slices.Clone(segments), slices.Clone(rows)
			change(s, r)
			if _, err := partitionIsolatedJournal(s, r); err == nil {
				t.Fatal("altered segment or row accepted")
			}
		})
	}
}
