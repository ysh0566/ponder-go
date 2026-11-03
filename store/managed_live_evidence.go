package store

import (
	"fmt"

	"ponder.local/ponder-go/checkpoint"
)

type managedLiveCursorState struct {
	ChainID                               uint64
	Name, Latest, Finalized, Safe, Cursor string
}

type managedLiveDurable struct {
	Control    ManagedLiveControl
	Operations []managedLiveOperation
	Segments   []ManagedLiveSegment
	Proofs     []ManagedLiveHead
	Journal    []managedLiveJournalEntry
	States     []managedLiveCursorState
}

func liveProofKey(head ManagedLiveHead) string {
	cp, _ := checkpoint.Decode(head.Checkpoint)
	return fmt.Sprintf("%d/%d/%s", cp.ChainID, cp.BlockNumber, head.Hash)
}

type managedLiveHistoricalSegment struct {
	Segment ManagedLiveSegment
	Rows    []managedLiveJournalEntry
}

// A row belongs to exactly one never-reused segment ordinal. Current physical
// rows, archived reorg suffixes and finalized prefixes are partitioned separately.
func liveHistoricalSegments(d managedLiveDurable, ordered []managedLiveOperation) (map[uint64]managedLiveHistoricalSegment, error) {
	history := map[uint64]managedLiveHistoricalSegment{}
	rowIDs := map[int64]bool{}
	add := func(segments []ManagedLiveSegment, rows []managedLiveJournalEntry, boundary int64) error {
		partition, err := partitionLiveJournal(segments, rows, boundary)
		if err != nil {
			return err
		}
		for _, segment := range segments {
			if _, exists := history[segment.Ordinal]; exists {
				return fmt.Errorf("managed segment is current and/or archived more than once")
			}
			for _, row := range partition[segment.Ordinal] {
				if rowIDs[row.ID] {
					return fmt.Errorf("managed journal ID belongs to multiple historical segments")
				}
				rowIDs[row.ID] = true
			}
			history[segment.Ordinal] = managedLiveHistoricalSegment{segment, partition[segment.Ordinal]}
		}
		return nil
	}
	if err := add(d.Segments, d.Journal, d.Control.SafeJournalBoundary); err != nil {
		return nil, err
	}
	for _, op := range ordered {
		if op.Kind == "recover" {
			var evidence managedLiveRecoveryEvidence
			if decodeLiveJSON(op.Payload, &evidence) != nil {
				return nil, ErrManagedLiveBatchConflict
			}
			rows, err := decodeLiveRecoveryUndo(evidence)
			if err != nil {
				return nil, err
			}
			if len(evidence.Removed) == 0 {
				if len(rows) != 0 {
					return nil, ErrManagedLiveBatchConflict
				}
			} else if err := add(evidence.Removed, rows, evidence.BeforeControl.SafeJournalBoundary); err != nil {
				return nil, err
			}
			continue
		}
		if op.Kind == "finalize" {
			var evidence managedLiveFinalizeEvidence
			if decodeLiveJSON(op.Payload, &evidence) != nil {
				return nil, ErrManagedLiveBatchConflict
			}
			rows, err := decodeLiveFinalizeJournal(evidence)
			if err != nil {
				return nil, err
			}
			if err := add(evidence.Pruned, rows, evidence.BeforeJournalBoundary); err != nil {
				return nil, err
			}
			continue
		}
		if op.Kind != "reorg" {
			continue
		}
		var evidence managedLiveReorgEvidence
		if decodeLiveJSON(op.Payload, &evidence) != nil {
			return nil, ErrManagedLiveBatchConflict
		}
		rows, err := decodeLiveReorgUndo(evidence)
		if err != nil {
			return nil, err
		}
		if len(evidence.Removed) == 0 {
			if len(rows) != 0 || len(evidence.Replay) != 0 {
				return nil, ErrManagedLiveBatchConflict
			}
			continue
		}
		if err := add(evidence.Removed, rows, evidence.Removed[0].Journal.Before); err != nil {
			return nil, err
		}
	}
	return history, nil
}

// SQL snapshot equality is checked separately. Replay proves the exact current
// projection AND every archived segment, rather than trusting latest>=target.
func (m *ManagedLive) validateDurable(d managedLiveDurable) ([]managedLiveOperation, error) {
	return m.validateDurableOwner(d, m.owner.s.leaseToken, m.opened.Generation, m.opts.FinalizedHeads)
}

func (m *ManagedLive) validateDurableOwner(d managedLiveDurable, terminalToken string, terminalGeneration uint64, terminalHeads map[uint64]ManagedLiveHead) ([]managedLiveOperation, error) {
	if d.Control.Version != m.version() {
		return nil, ErrManagedLiveBatchConflict
	}
	if err := validateLiveControl(d.Control); err != nil {
		return nil, err
	}
	ordered, err := orderLiveOperations(d.Operations, d.Control.LastOperation)
	if err != nil {
		return nil, err
	}
	history, err := liveHistoricalSegments(d, ordered)
	if err != nil {
		return nil, err
	}
	var rolling ManagedLiveControl
	var generation uint64
	var token string
	var baseline map[uint64]ManagedLiveHead
	latest := map[uint64]string{}
	active := []ManagedLiveSegment{}
	activeJournal := []managedLiveJournalEntry{}
	expectedProofs := map[string]ManagedLiveHead{}
	seenSegments := map[uint64]bool{}
	var historicalJournalHigh int64
	rollingOpts := m.opts
	executedHeads := map[string]ManagedLiveHead{}
	for _, op := range ordered {
		switch op.Kind {
		case "open":
			if rolling.Batches != 0 || op.Generation != generation+1 {
				return nil, ErrManagedLivePendingActions
			}
			rolling = ManagedLiveControl{}
			if err := decodeLiveJSON(op.Payload, &rolling); err != nil {
				return nil, err
			}
			if validateLiveControl(rolling) != nil || rolling.Generation != op.Generation || rolling.LastOperation != op.ID || rolling.Tail != 0 || rolling.SafeOrdinal != 0 || len(rolling.SafeAnchors) != 0 || len(rolling.SafeHeads) != 0 || rolling.SafeJournalBoundary != 0 || rolling.Finalizations != 0 || len(rolling.Finality) != 0 || rolling.Reorgs != 0 || len(rolling.FinalizedHeads) != len(m.opts.Chains) || len(rolling.HistoryCuts) != len(m.opts.Chains) {
				return nil, ErrManagedLiveBatchConflict
			}
			for _, chain := range m.opts.Chains {
				head, ok := rolling.FinalizedHeads[chain.ID]
				normalized, cp, err := normalizeLiveHead(head)
				if !ok || err != nil || normalized != head || cp.ChainID != chain.ID || rolling.HistoryCuts[chain.ID] != min(cp.BlockNumber, m.opts.EndBlocks[chain.ID]) {
					return nil, ErrManagedLiveBatchConflict
				}
			}
			request := managedHash(struct {
				Identity, OpenID string
				Heads            map[uint64]ManagedLiveHead
				Cuts             map[uint64]uint64
			}{m.owner.identity, op.ID, rolling.FinalizedHeads, rolling.HistoryCuts})
			if op.RequestHash != request {
				return nil, ErrManagedLiveReceiptConflict
			}
			rollingOpts.FinalizedHeads = cloneLiveHeads(rolling.FinalizedHeads)
			generation, token = op.Generation, op.OwnerToken
		case "serve":
			var e managedRuntimeServe
			if !m.opts.liveRuntime || op.Generation != generation || op.OwnerToken != token || decodeLiveJSON(op.Payload, &e) != nil || e.ID != op.ID || op.PreviousOperation != rolling.LastOperation || op.RequestHash != m.runtimeServeHash(e, token) {
				return nil, ErrManagedLiveReceiptConflict
			}
			next, err := m.planRuntimeServe(rolling, e)
			if err != nil || managedHash(next) != managedHash(e.After) {
				return nil, ErrManagedLiveReceiptConflict
			}
			rolling = next
		case "batch":
			if generation == 0 || op.Generation != generation || op.OwnerToken != token {
				return nil, ErrManagedLiveBatchConflict
			}
			var evidence managedLiveBatchEvidence
			if err := decodeLiveJSON(op.Payload, &evidence); err != nil {
				return nil, err
			}
			normalized, err := normalizeLiveBatch(m.opts, evidence.Batch)
			if err != nil || managedHash(normalized) != managedHash(evidence.Batch) || normalized.ExpectedLastOperation != op.PreviousOperation {
				return nil, ErrManagedLiveBatchConflict
			}
			if _, err := exactLiveRetry(normalized, liveBatchHash(m.owner.identity, token, normalized), token, op); err != nil {
				return nil, err
			}
			planned, before, after, err := planLiveBatch(rollingOpts, rolling, normalized)
			if err != nil || len(planned) != len(evidence.Segments) || managedHash(before) != managedHash(evidence.BeforeCursors) {
				return nil, ErrManagedLiveBatchConflict
			}
			if rolling.Batches == 0 {
				baseline = cloneLiveHeads(normalized.Baselines)
				for id, head := range baseline {
					expectedProofs[liveProofKey(head)] = head
					latest[id] = head.Checkpoint
					if m.opts.runtimeInitial {
						latest[id] = normalized.InitialCheckpoints[id]
					}
				}
			}
			after.CatalogHash = evidence.AfterControl.CatalogHash
			if validateLiveControl(after) != nil || managedHash(after) != managedHash(evidence.AfterControl) {
				return nil, ErrManagedLiveBatchConflict
			}
			boundary := rolling.SafeJournalBoundary
			if len(activeJournal) > 0 {
				boundary = activeJournal[len(activeJournal)-1].ID
			}
			for i, segment := range evidence.Segments {
				planned[i].Journal = segment.Journal
				archived, exists := history[segment.Ordinal]
				if planned[i] != segment || !exists || archived.Segment != segment || seenSegments[segment.Ordinal] || segment.Journal.Before != boundary {
					return nil, fmt.Errorf("managed segment predecessor/acquisition/cursor or history differs")
				}
				span, err := liveJournalSpan(boundary, segment.Block.Head, archived.Rows)
				if err != nil || span != segment.Journal {
					return nil, ErrManagedLiveBatchConflict
				}
				for _, row := range archived.Rows {
					if row.ID <= historicalJournalHigh {
						return nil, fmt.Errorf("managed historical journal ID was reused or reordered")
					}
					historicalJournalHigh = row.ID
				}
				seenSegments[segment.Ordinal] = true
				active = append(active, segment)
				activeJournal = append(activeJournal, archived.Rows...)
				boundary = segment.Journal.After
				key := liveProofKey(segment.Block.Head)
				if old, exists := expectedProofs[key]; exists && old != segment.Block.Head {
					return nil, ErrManagedLiveBatchConflict
				}
				expectedProofs[key] = segment.Block.Head
				cp, _ := checkpoint.Decode(segment.Block.Head.Checkpoint)
				if liveReplayPublishes(segment.Block.Head, rolling.ReplayTargets) {
					latest[cp.ChainID] = segment.Block.Head.Checkpoint
					if rollingOpts.runtimeOmnichain && segment.Block.SourceCheckpoint != "" {
						latest[cp.ChainID] = segment.Block.SourceCheckpoint
					}
				}
			}
			if rolling.Finalizations > 0 {
				proofs := make([]ManagedLiveHead, 0, len(expectedProofs))
				for _, head := range expectedProofs {
					proofs = append(proofs, head)
				}
				if err := validateLiveFinalityBranch(m.opts, baseline, rolling.Finality, liveSafeHeads(rolling, baseline), after.Cursors, active, proofs); err != nil {
					return nil, err
				}
			}
			for _, segment := range evidence.Segments {
				executedHeads[liveProofKey(segment.Block.Head)] = segment.Block.Head
			}
			rolling = after
		case "recover":
			var e managedLiveRecoveryEvidence
			if !m.opts.recovery || decodeLiveJSON(op.Payload, &e) != nil || e.Version != managedLiveRecoveryReceiptVersion || e.OpenID != op.ID || e.BeforeOwner != token || op.Generation != generation+1 || managedHash(e.BeforeControl) != managedHash(rolling) || op.PreviousOperation != rolling.LastOperation {
				return nil, ErrManagedLiveBatchConflict
			}
			states := make([]managedLiveCursorState, 0, len(m.opts.Chains))

			for _, chain := range m.opts.Chains {
				states = append(states, managedLiveCursorState{ChainID: chain.ID, Name: chain.Name, Latest: latest[chain.ID], Finalized: liveFinalCheckpoint(rolling, baseline, chain.ID), Safe: liveSafeCheckpoint(rolling, baseline, chain.ID), Cursor: liveCursorCheckpoint(rolling, chain.ID)})
			}
			recoveryOpts := m.opts
			recoveryOpts.OpenID, recoveryOpts.FinalizedHeads = op.ID, e.Heads
			expected, err := planLiveRecovery(recoveryOpts, rolling, baseline, active, activeJournal, states, token)
			expected.AfterControl.CatalogHash = e.AfterControl.CatalogHash
			if err != nil || managedHash(expected) != managedHash(e) || op.RequestHash != m.recoveryHash(e, op.OwnerToken) {
				return nil, fmt.Errorf("managed exact recovery input/finality/undo/projection differs")
			}
			rolling, generation, token = e.AfterControl, op.Generation, op.OwnerToken
			rollingOpts.FinalizedHeads = cloneLiveHeads(e.Heads)
			active, activeJournal = nil, nil
		case "reorg":
			if generation == 0 || op.Generation != generation || op.OwnerToken != token {
				return nil, ErrManagedLiveBatchConflict
			}
			var evidence managedLiveReorgEvidence
			if decodeLiveJSON(op.Payload, &evidence) != nil {
				return nil, ErrManagedLiveBatchConflict
			}
			normalized, err := normalizeLiveReorg(m.opts, evidence.Request)
			if err != nil || managedHash(normalized) != managedHash(evidence.Request) || normalized.ExpectedLastOperation != op.PreviousOperation {
				return nil, ErrManagedLiveBatchConflict
			}
			if _, err := exactLiveReorgRetry(normalized, liveReorgHash(m.owner.identity, token, normalized), token, op); err != nil {
				return nil, err
			}
			proofs := make([]ManagedLiveHead, 0, len(expectedProofs))
			for _, head := range expectedProofs {
				proofs = append(proofs, head)
			}
			plan, err := planLiveReorg(m.opts, rolling, baseline, active, activeJournal, proofs, normalized)
			if err != nil {
				return nil, err
			}
			expected, err := makeLiveReorgEvidence(normalized, liveReorgHash(m.owner.identity, token, normalized), rolling, plan)
			if err != nil || managedHash(expected) != managedHash(evidence) {
				return nil, fmt.Errorf("managed exact reorg undo/cursor/receipt evidence differs")
			}
			active = active[:len(active)-len(plan.Removed)]
			keepRows := 0
			for keepRows < len(activeJournal) && activeJournal[keepRows].ID <= plan.JournalBoundary {
				keepRows++
			}
			activeJournal = activeJournal[:keepRows]
			expectedProofs = map[string]ManagedLiveHead{}
			for _, head := range plan.Proofs {
				expectedProofs[liveProofKey(head)] = head
			}
			rolling = plan.After // Public latest/finalized are deliberately unchanged.
		case "finalize":
			if generation == 0 || op.Generation != generation || op.OwnerToken != token {
				return nil, ErrManagedLiveBatchConflict
			}
			var evidence managedLiveFinalizeEvidence
			if decodeLiveJSON(op.Payload, &evidence) != nil {
				return nil, ErrManagedLiveBatchConflict
			}
			normalized, err := normalizeLiveFinalize(m.opts, evidence.Request)
			if err != nil || managedHash(normalized) != managedHash(evidence.Request) || normalized.ExpectedLastOperation != op.PreviousOperation {
				return nil, ErrManagedLiveBatchConflict
			}
			if _, err := exactLiveFinalizeRetry(normalized, liveFinalizeHash(m.owner.identity, token, normalized), token, op); err != nil {
				return nil, err
			}
			proofs := make([]ManagedLiveHead, 0, len(expectedProofs))
			for _, head := range expectedProofs {
				proofs = append(proofs, head)
			}
			plan, err := planLiveFinalize(m.opts, rolling, baseline, active, activeJournal, proofs, normalized, executedHeads)
			if err != nil {
				return nil, err
			}
			expected, err := makeLiveFinalizeEvidence(normalized, liveFinalizeHash(m.owner.identity, token, normalized), rolling, baseline, plan)
			if err != nil || managedHash(expected) != managedHash(evidence) {
				return nil, fmt.Errorf("managed exact finalized prefix/receipt evidence differs")
			}
			active = active[len(plan.Pruned):]
			prunedRows := 0
			for prunedRows < len(activeJournal) && activeJournal[prunedRows].ID <= plan.After.SafeJournalBoundary {
				prunedRows++
			}
			activeJournal = activeJournal[prunedRows:]
			rolling = plan.After
		default:
			return nil, ErrManagedLivePendingActions
		}
	}
	if generation != d.Control.Generation || token != terminalToken || generation != terminalGeneration || managedHash(rolling) != managedHash(d.Control) || managedHash(rolling.FinalizedHeads) != managedHash(terminalHeads) || len(active) != len(d.Segments) || len(seenSegments) != len(history) {
		return nil, fmt.Errorf("managed exact terminal control/owner/generation or history differs")
	}
	for i, segment := range active {
		if segment != d.Segments[i] {
			return nil, fmt.Errorf("managed active segment suffix differs")
		}
	}
	if err := liveActiveJournal(d.Control, d.Segments, d.Journal); err != nil {
		return nil, err
	}
	if managedHash(activeJournal) != managedHash(d.Journal) {
		// SQL decoders return a nonnil empty journal; permit nil in pure fixtures.
		if len(activeJournal) != 0 || len(d.Journal) != 0 {
			return nil, fmt.Errorf("managed current journal differs from operation projection")
		}
	}
	if len(expectedProofs) != len(d.Proofs) {
		return nil, fmt.Errorf("managed retained proof set differs")
	}
	seen := map[string]bool{}
	for _, head := range d.Proofs {
		key := liveProofKey(head)
		if seen[key] || expectedProofs[key] != head {
			return nil, fmt.Errorf("managed retained acquisition differs")
		}
		seen[key] = true
	}
	if d.Control.Finalizations > 0 {
		if err := validateLiveFinalityBranch(m.opts, baseline, d.Control.Finality, liveSafeHeads(d.Control, baseline), d.Control.Cursors, d.Segments, d.Proofs); err != nil {
			return nil, err
		}
	}
	if d.Control.Batches == 0 {
		if len(d.States) != 0 {
			return nil, ErrManagedLivePendingActions
		}
	} else {
		if len(d.States) != len(m.opts.Chains) || len(d.Control.Cursors) != len(m.opts.Chains) {
			return nil, ErrManagedLiveBatchConflict
		}
		states := map[uint64]managedLiveCursorState{}
		for _, state := range d.States {
			if _, exists := states[state.ChainID]; exists {
				return nil, ErrManagedLiveBatchConflict
			}
			states[state.ChainID] = state
		}
		for _, chain := range m.opts.Chains {
			state, ok := states[chain.ID]
			finalized := liveFinalCheckpoint(d.Control, baseline, chain.ID)
			safe := liveSafeCheckpoint(d.Control, baseline, chain.ID)
			if !ok || state.Name != chain.Name || state.Latest != latest[chain.ID] || state.Cursor != liveCursorCheckpoint(d.Control, chain.ID) || state.Finalized != finalized || state.Safe != safe {
				return nil, fmt.Errorf("managed public/private projection or safe baseline differs")
			}
		}
	}
	return ordered, nil
}

func confirmLiveSnapshot(ordered []managedLiveOperation, actual string) error {
	if len(ordered) == 0 || !liveDigest(actual) || ordered[len(ordered)-1].AfterHash != actual {
		return fmt.Errorf("managed exact terminal snapshot differs")
	}
	return nil
}
