package store

import "ponder.local/ponder-go/checkpoint"

// Proofs are a projection of the authenticated receipt prefix. The physical
// retained set can legitimately lose old proofs during a later reorg, so it is
// never an input to validation of an older cross-domain acknowledgement.
func liveDomainProofPrefix(ordered []managedLiveOperation) ([]ManagedLiveHead, error) {
	proofs := map[string]ManagedLiveHead{}
	for _, op := range ordered {
		switch op.Kind {
		case "batch":
			var e managedLiveBatchEvidence
			if decodeLiveJSON(op.Payload, &e) != nil {
				return nil, ErrManagedLiveReceiptConflict
			}
			for _, h := range e.Batch.Baselines {
				proofs[liveProofKey(h)] = h
			}
			for _, segment := range e.Segments {
				h := segment.Block.Head
				proofs[liveProofKey(h)] = h
			}
		case "reorg":
			var e managedLiveReorgEvidence
			if decodeLiveJSON(op.Payload, &e) != nil {
				return nil, ErrManagedLiveReceiptConflict
			}
			ancestor, err := checkpoint.Decode(e.Request.Ancestor.Checkpoint)
			if err != nil {
				return nil, err
			}
			for key, h := range proofs {
				cp, err := checkpoint.Decode(h.Checkpoint)
				if err != nil {
					return nil, err
				}
				if cp.ChainID == ancestor.ChainID && cp.BlockNumber > ancestor.BlockNumber {
					delete(proofs, key)
				}
			}
		case "open", "recover", "finalize", "serve": // These retain the acquired proof set.
		default:
			return nil, ErrManagedLiveReceiptConflict
		}
	}
	out := make([]ManagedLiveHead, 0, len(proofs))
	for _, head := range proofs {
		out = append(out, head)
	}
	return out, nil
}

func validateDomainOrphans(in ManagedLiveDomainReorg, control ManagedLiveControl, proofs []ManagedLiveHead) error {
	_, ancestor, _ := normalizeLiveHead(in.Reorg.Ancestor)
	_, tip, err := normalizeLiveHead(control.Cursors[ancestor.ChainID])
	if err != nil {
		return err
	}
	headers := map[uint64]ManagedLiveHead{}
	for _, h := range in.Orphaned {
		cp, _ := checkpoint.Decode(h.Checkpoint)
		headers[cp.BlockNumber] = h
	}
	if tip.BlockNumber > ancestor.BlockNumber && uint64(len(in.Orphaned)) < tip.BlockNumber-ancestor.BlockNumber {
		return ErrManagedLiveBatchConflict
	}
	byKey, err := liveProofMap(proofs)
	if err != nil {
		return err
	}
	lineage, err := liveHeadLineage(control.Cursors[ancestor.ChainID], in.Reorg.Ancestor, byKey)
	if err != nil {
		return err
	}
	for number, h := range lineage {
		if number > ancestor.BlockNumber && headers[number] != h {
			return ErrManagedLiveBatchConflict
		}
	}
	return nil
}
