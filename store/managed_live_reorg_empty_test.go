package store

import (
	"testing"
)

// Supplemental controls for the pinned revertMultichain NULL-minimum path.
// Empty affected history still needs a receipt for independent sync cleanup.
func TestManagedLiveReorgUnitNoAffectedSegment(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		name := "other_chain_segments_remain"
		if finalized {
			name = "all_segments_already_safe"
		}
		t.Run(name, func(t *testing.T) {
			m, d, _ := liveReorgUnitHistory(t)
			if finalized {
				liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "finalize-all-before-empty-reorg", d.Control.Cursors))
			}
			before := d.Control
			states, proofs := managedHash(d.States), managedHash(d.Proofs)
			segments, journal := managedHash(d.Segments), managedHash(d.Journal)
			r := liveReorgUnitRequest(d, d.Control.Cursors[1])
			r.ID = "no-affected-segment"
			plan := liveReorgUnitApply(t, m, &d, r)
			if len(plan.Removed) != 0 || len(plan.Undo) != 0 || len(plan.Replay) != 0 || plan.After.Next != before.Next || plan.After.Tail != before.Tail || plan.After.Reorgs != before.Reorgs+1 || plan.After.LastOperation != r.ID || managedHash(plan.After.Cursors) != managedHash(before.Cursors) {
				t.Fatalf("empty reorg changed business projection or lost its receipt: %+v", plan)
			}
			if managedHash(d.States) != states || managedHash(d.Proofs) != proofs || managedHash(d.Segments) != segments || (len(d.Journal) != 0 && managedHash(d.Journal) != journal) {
				t.Fatal("no affected segment changed public/private state or retained evidence")
			}
			if _, err := m.validateDurable(d); err != nil {
				t.Fatalf("empty reorg receipt failed full historical replay: %v", err)
			}
		})
	}
}
