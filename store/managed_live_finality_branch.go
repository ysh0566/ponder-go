package store

// Check an acquired batch's proposed branch before invoking user callbacks.
// The full durable validator repeats this check after the actual writes.
func (m *ManagedLive) validateBatchFinality(d managedLiveDurable, after ManagedLiveControl, segments []ManagedLiveSegment) error {
	if d.Control.Finalizations == 0 {
		return nil
	}
	baseline, err := liveDurableBaselines(d.Operations)
	if err != nil {
		return err
	}
	byKey, err := liveProofMap(d.Proofs)
	if err != nil {
		return err
	}
	proofs := append([]ManagedLiveHead(nil), d.Proofs...)
	for _, segment := range segments {
		head := segment.Block.Head
		key := liveProofKey(head)
		if old, exists := byKey[key]; exists {
			if old != head {
				return ErrManagedLiveBatchConflict
			}
		} else {
			byKey[key] = head
			proofs = append(proofs, head)
		}
	}
	active := append(append([]ManagedLiveSegment(nil), d.Segments...), segments...)
	return validateLiveFinalityBranch(m.opts, baseline, d.Control.Finality, liveSafeHeads(d.Control, baseline), after.Cursors, active, proofs)
}
