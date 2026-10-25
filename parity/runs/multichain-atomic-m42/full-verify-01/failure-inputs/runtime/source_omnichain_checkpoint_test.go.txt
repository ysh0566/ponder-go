package runtime

import (
	"testing"

	"ponder.local/ponder-go/filter"
)

// Supplemental cursor controls do not add original-source case credit.
func TestSourceOmnichainCheckpointControls(t *testing.T) {
	progress := func(id, start, current, final, end uint64) *SourceSyncProgress {
		p := &SourceSyncProgress{ChainID: id, Start: sourceProgressBlock(start), Current: sourceProgressBlock(current), Finalized: sourceProgressBlock(final)}
		if end != 0 {
			p.End = sourceProgressBlock(end)
		}
		return p
	}
	for _, tc := range []struct {
		name string
		tag  SourceSyncProgressTag
		a, b *SourceSyncProgress
		want int
	}{
		{"start_minimum", SourceSyncStart, progress(1, 1, 3, 9, 10), progress(2, 2, 5, 7, 8), 0},
		{"finalized_minimum", SourceSyncFinalized, progress(1, 1, 3, 9, 10), progress(2, 2, 5, 7, 8), 1},
		{"end_maximum", SourceSyncEnd, progress(1, 1, 3, 9, 10), progress(2, 2, 5, 7, 8), 0},
		{"current_minimum", SourceSyncCurrent, progress(1, 1, 3, 9, 10), progress(2, 2, 5, 7, 8), 0},
		{"completed_chain_ignored", SourceSyncCurrent, progress(1, 1, 3, 9, 3), progress(2, 2, 5, 7, 8), 1},
		{"all_complete_maximum", SourceSyncCurrent, progress(1, 1, 3, 9, 3), progress(2, 2, 5, 7, 5), 1},
		{"equal_timestamp_chain_tiebreak", SourceSyncFinalized, progress(9, 1, 3, 9, 10), progress(2, 1, 3, 9, 10), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := []*SourceSyncProgress{tc.a, tc.b}
			got, err := GetSourceOmnichainCheckpoint(ps, tc.tag)
			if err != nil {
				t.Fatal(err)
			}
			want, err := ps[tc.want].GetCheckpoint(tc.tag)
			if err != nil || got == nil || want == nil || *got != *want {
				t.Fatalf("got %v, want %v, err %v", got, want, err)
			}
		})
	}
	t.Run("any_unbounded_end_unsets_global_end", func(t *testing.T) {
		got, err := GetSourceOmnichainCheckpoint([]*SourceSyncProgress{progress(1, 0, 3, 9, 10), progress(2, 0, 4, 9, 0)}, SourceSyncEnd)
		if err != nil || got != nil {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("absent_current_is_error_not_silently_completed", func(t *testing.T) {
		p := progress(1, 0, 3, 9, 3)
		p.Current = nil
		if _, err := GetSourceOmnichainCheckpoint([]*SourceSyncProgress{p}, SourceSyncCurrent); err == nil {
			t.Fatal("absent current accepted")
		}
	})
	t.Run("empty_invalid_tag_and_nil_progress_reject", func(t *testing.T) {
		if _, err := GetSourceOmnichainCheckpoint(nil, SourceSyncStart); err == nil {
			t.Fatal("empty accepted")
		}
		if _, err := GetSourceOmnichainCheckpoint([]*SourceSyncProgress{nil}, SourceSyncStart); err == nil {
			t.Fatal("nil accepted")
		}
		if _, err := GetSourceOmnichainCheckpoint([]*SourceSyncProgress{progress(1, 0, 1, 2, 3)}, "bad"); err == nil {
			t.Fatal("invalid tag accepted")
		}
	})
	t.Run("does_not_mutate_shared_progress", func(t *testing.T) {
		block := filter.Object{"number": "0x1", "timestamp": "0x1"}
		p := &SourceSyncProgress{ChainID: 1, Start: block, Current: block, Finalized: block, End: block}
		_, err := GetSourceOmnichainCheckpoint([]*SourceSyncProgress{p}, SourceSyncCurrent)
		if err != nil {
			t.Fatal(err)
		}
		block["number"] = "0x2"
		if p.Start["number"] != "0x2" || p.Current["number"] != "0x2" {
			t.Fatal("progress identity changed")
		}
	})
}
