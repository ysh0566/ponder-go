package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ponder.local/ponder-go/checkpoint"
)

// Pure model/admission tests only. No SQL engine or runtime is started.
func liveFinalizeUnitRequest(d managedLiveDurable, id string, heads map[uint64]ManagedLiveHead) ManagedLiveFinalize {
	return ManagedLiveFinalize{ID: id, Generation: d.Control.Generation, ExpectedLastOperation: d.Control.LastOperation, ExpectedTail: d.Control.Tail, ExpectedSafeOrdinal: d.Control.SafeOrdinal, Finalized: cloneLiveHeads(heads)}
}

func liveFinalizeUnitApply(t *testing.T, m *ManagedLive, d *managedLiveDurable, r ManagedLiveFinalize) managedLiveFinalizePlan {
	t.Helper()
	baseline, err := liveDurableBaselines(d.Operations)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planLiveFinalize(m.opts, d.Control, baseline, d.Segments, d.Journal, d.Proofs, r)
	if err != nil {
		t.Fatal(err)
	}
	e, err := makeLiveFinalizeEvidence(r, m.finalizeHash(r), d.Control, baseline, plan)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	previous := d.Operations[len(d.Operations)-1]
	d.Operations = append(d.Operations, managedLiveOperation{ID: r.ID, Generation: r.Generation, OwnerToken: m.owner.s.leaseToken, Kind: "finalize", RequestHash: m.finalizeHash(r), PreviousOperation: r.ExpectedLastOperation, BeforeHash: previous.AfterHash, AfterHash: managedHash("unit-finalize:" + r.ID), Payload: raw})
	d.Segments = append([]ManagedLiveSegment(nil), d.Segments[len(plan.Pruned):]...)
	first := 0
	for first < len(d.Journal) && d.Journal[first].ID <= plan.After.SafeJournalBoundary {
		first++
	}
	d.Journal = append([]managedLiveJournalEntry(nil), d.Journal[first:]...)
	d.Control = plan.After
	safe := liveSafeHeads(d.Control, baseline)
	for i := range d.States {
		d.States[i].Finalized = d.Control.Finality[d.States[i].ChainID].Checkpoint
		d.States[i].Safe = safe[d.States[i].ChainID].Checkpoint
	}
	return plan
}

func liveFinalizeUnitAppend(t *testing.T, m *ManagedLive, d *managedLiveDurable, b ManagedLiveBatch, writes [][]managedLiveJournalEntry) {
	t.Helper()
	normalized, err := normalizeLiveBatch(m.opts, b)
	if err != nil {
		t.Fatal(err)
	}
	segments, before, after, err := planLiveBatch(m.opts, d.Control, normalized)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.validateBatchFinality(*d, after, segments); err != nil {
		t.Fatal(err)
	}
	boundary := d.Control.SafeJournalBoundary
	if len(d.Journal) > 0 {
		boundary = d.Journal[len(d.Journal)-1].ID
	}
	proofs := map[string]ManagedLiveHead{}
	for _, h := range d.Proofs {
		proofs[liveProofKey(h)] = h
	}
	for i := range segments {
		var rows []managedLiveJournalEntry
		if i < len(writes) {
			rows = writes[i]
		}
		segments[i].Journal, err = liveJournalSpan(boundary, segments[i].Block.Head, rows)
		if err != nil {
			t.Fatal(err)
		}
		boundary = segments[i].Journal.After
		d.Journal = append(d.Journal, rows...)
		h := segments[i].Block.Head
		k := liveProofKey(h)
		if prior, ok := proofs[k]; ok {
			if prior != h {
				t.Fatal("retargeted proof")
			}
		} else {
			d.Proofs = append(d.Proofs, h)
			proofs[k] = h
		}
	}
	e := managedLiveBatchEvidence{Version: managedLiveBatchVersion, Batch: normalized, AcquisitionHash: m.batchHash(normalized), BeforeCursors: before, Segments: segments, AfterControl: after}
	raw, _ := json.Marshal(e)
	previous := d.Operations[len(d.Operations)-1]
	d.Operations = append(d.Operations, managedLiveOperation{ID: b.ID, Generation: b.Generation, OwnerToken: m.owner.s.leaseToken, Kind: "batch", RequestHash: m.batchHash(normalized), PreviousOperation: previous.ID, BeforeHash: previous.AfterHash, AfterHash: managedHash("unit-batch:" + b.ID), Payload: raw})
	d.Segments = append(d.Segments, segments...)
	d.Control = after
	for i := range d.States {
		d.States[i].Cursor = after.Cursors[d.States[i].ChainID].Checkpoint
		for _, block := range b.Blocks {
			cp, _ := checkpoint.Decode(block.Head.Checkpoint)
			if cp.ChainID == d.States[i].ChainID {
				d.States[i].Latest = block.Head.Checkpoint
			}
		}
	}
}

func liveFinalizeUnitPartial(t *testing.T) (*ManagedLive, managedLiveDurable, ManagedLiveBatch) {
	t.Helper()
	m, d, b := liveReorgUnitHistory(t)
	liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "final-b", map[uint64]ManagedLiveHead{1: b.Blocks[9].Head}))
	liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "final-a", map[uint64]ManagedLiveHead{10: b.Blocks[0].Head}))
	if _, err := m.validateDurable(d); err != nil {
		t.Fatal(err)
	}
	return m, d, b
}

func TestManagedLiveFinalizeUnit(t *testing.T) {
	t.Run("earlier_unsafe_chain_blocks_later_finalized_chain", func(t *testing.T) {
		m, d, b := liveReorgUnitHistory(t)
		private := managedHash(d.Control.Cursors)
		latest := []string{d.States[0].Latest, d.States[1].Latest}
		proofs := managedHash(d.Proofs)
		first := liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "later-chain-first", map[uint64]ManagedLiveHead{1: b.Blocks[9].Head}))
		if len(first.Pruned) != 0 || d.Control.SafeOrdinal != 0 || len(d.Control.SafeHeads) != 0 || !first.Changed || len(d.Journal) != 7 {
			t.Fatal("later finalized chain skipped earlier unsafe segment")
		}
		if managedHash(d.Control.Cursors) != private || !reflect.DeepEqual(latest, []string{d.States[0].Latest, d.States[1].Latest}) || managedHash(d.Proofs) != proofs {
			t.Fatal("finalize changed private/latest/proofs")
		}
		second := liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "earlier-chain-safe", map[uint64]ManagedLiveHead{10: b.Blocks[0].Head}))
		if len(second.Pruned) != 2 || d.Control.SafeOrdinal != 2 || d.Control.SafeJournalBoundary != 7 || d.Control.Next != 11 || d.Control.Tail != 10 || d.Segments[0].Ordinal != 3 || d.Segments[0].Predecessor != 2 {
			t.Fatal("unsafe tail boundary or high-water changed")
		}
		for _, state := range d.States {
			if state.ChainID == 1 && (state.Finalized != b.Blocks[9].Head.Checkpoint || state.Safe != b.Blocks[1].Head.Checkpoint) {
				t.Fatal("public finalized was lowered to global safe")
			}
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("empty_segments_advance_prefix_without_journal_rows", func(t *testing.T) {
		m, d := liveSegmentsUnitOwner(t)
		b := liveSegmentsUnitBatch(m)
		liveSegmentsUnitAppend(t, m, &d, b, nil)
		liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "empty-b", map[uint64]ManagedLiveHead{1: b.Blocks[1].Head}))
		if d.Control.SafeOrdinal != 0 {
			t.Fatal("unsafe empty first segment was skipped")
		}
		liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "empty-a5", map[uint64]ManagedLiveHead{10: b.Blocks[0].Head}))
		if d.Control.SafeOrdinal != 2 || d.Control.SafeJournalBoundary != 0 || len(d.Segments) != 1 {
			t.Fatal("empty safe prefix disappeared")
		}
		liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "empty-a6", map[uint64]ManagedLiveHead{10: b.Blocks[2].Head}))
		if d.Control.SafeOrdinal != 3 || d.Control.Tail != 3 || d.Control.Next != 4 || len(d.Segments) != 0 || len(d.Journal) != 0 {
			t.Fatal("all-safe empty tail reset ordinal high-water")
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		block := liveSegmentsUnitBlock(10, 7, b.Blocks[2].Head.Hash)
		next := ManagedLiveBatch{ID: "after-empty-safe", Generation: 1, ExpectedLastOperation: d.Control.LastOperation, ExpectedTail: d.Control.Tail, Blocks: []ManagedLiveBlock{block}}
		liveFinalizeUnitAppend(t, m, &d, next, [][]managedLiveJournalEntry{{liveReorgUnitRow(12, block, "", "12")}})
		if d.Segments[0].Ordinal != 4 || d.Segments[0].Predecessor != 3 || d.Segments[0].Journal.Before != 0 {
			t.Fatal("post-safe append boundary")
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("shared_row_undo_stops_at_safe_and_replay_requalifies_same_heads", func(t *testing.T) {
		m, d, b := liveFinalizeUnitPartial(t)
		r := liveReorgUnitRequest(d, b.Blocks[0].Head)
		r.ID = "unsafe-suffix-reorg"
		undo := liveReorgUnitApply(t, m, &d, r)
		balance := "100"
		for _, row := range liveReorgUndoRows(undo.Undo).Rows {
			if row["before"] == nil {
				balance = "missing"
			} else {
				var previous map[string]any
				_ = json.Unmarshal([]byte(fmt.Sprint(row["before"])), &previous)
				balance = fmt.Sprint(previous["balance"])
			}
		}
		if balance != "20" || d.Control.Tail != 2 || d.Control.SafeOrdinal != 2 || d.Control.Next != 11 || len(d.Journal) != 0 {
			t.Fatal("shared row undo crossed safe prefix")
		}
		for _, s := range d.States {
			if s.ChainID == 1 && (s.Finalized != b.Blocks[9].Head.Checkpoint || s.Cursor != b.Blocks[1].Head.Checkpoint) {
				t.Fatal("global undo reset other chain public finalized")
			}
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		same := cloneLiveHeads(d.Control.Finality)
		noop := liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "no-active-noop", same))
		if noop.Changed || len(noop.Pruned) != 0 {
			t.Fatal("no-op invented safe advancement")
		}
		blocks := []ManagedLiveBlock{b.Blocks[3], b.Blocks[5], b.Blocks[7], b.Blocks[9]}
		replay := ManagedLiveBatch{ID: "confirmed-b-replay", Generation: 1, ExpectedLastOperation: d.Control.LastOperation, ExpectedTail: 2, Blocks: blocks}
		writes := [][]managedLiveJournalEntry{{liveReorgUnitRow(40, blocks[0], "20", "40")}, nil, {liveReorgUnitRow(50, blocks[2], "40", "80")}, {liveReorgUnitRow(60, blocks[3], "80", "100")}}
		liveFinalizeUnitAppend(t, m, &d, replay, writes)
		if d.Segments[0].Ordinal != 11 || d.Segments[0].Predecessor != 2 || d.Segments[0].Journal.Before != 7 {
			t.Fatal("replay lost absolute safe journal boundary or ordinal gap")
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		proofsBeforeAgain := managedHash(d.Proofs)
		again := liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "same-heads-new-safe-prefix", same))
		if !again.Changed || len(again.Pruned) != 4 || d.Control.SafeOrdinal != 14 || d.Control.SafeJournalBoundary != 60 || d.Control.Tail != 14 || d.Control.Next != 15 || len(d.Segments) != 0 || len(d.Journal) != 0 {
			t.Fatal("equal finalized inputs incorrectly treated as no-op after replay")
		}
		if managedHash(d.Proofs) != proofsBeforeAgain {
			t.Fatal("finalize pruned retained replay proofs")
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		replacement := liveSegmentsUnitBlock(10, 6, b.Blocks[0].Head.Hash)
		replacement.Head.Hash = fmt.Sprintf("0x%064x", 999)
		replacement.InputDigest = managedHash("new-fork")
		next := ManagedLiveBatch{ID: "after-safe-gap", Generation: 1, ExpectedLastOperation: d.Control.LastOperation, ExpectedTail: 14, Blocks: []ManagedLiveBlock{replacement}}
		liveFinalizeUnitAppend(t, m, &d, next, [][]managedLiveJournalEntry{{liveReorgUnitRow(80, replacement, "100", "110")}})
		if d.Segments[0].Ordinal != 15 || d.Segments[0].Predecessor != 14 || d.Segments[0].Journal.Before != 60 {
			t.Fatal("high-water reused after prefix pruning")
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no_op_and_exact_old_receipt_survive_segment_pruning", func(t *testing.T) {
		m, d, b := liveReorgUnitHistory(t)
		r := liveFinalizeUnitRequest(d, "explicit-noop", b.Baselines)
		p := liveFinalizeUnitApply(t, m, &d, r)
		if p.Changed || d.Control.SafeOrdinal != 0 || d.Control.Finalizations != 1 {
			t.Fatal("no-op receipt changed business projection")
		}
		op := d.Operations[len(d.Operations)-1]
		liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "fully-safe", map[uint64]ManagedLiveHead{10: b.Blocks[8].Head, 1: b.Blocks[9].Head}))
		ordered, err := m.validateDurable(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Segments) != 0 || len(d.Journal) != 0 || len(d.Operations) != 4 {
			t.Fatal("safe cleanup removed independent receipts")
		}
		if _, err := exactLiveFinalizeRetry(r, m.finalizeHash(r), m.owner.s.leaseToken, op); err != nil {
			t.Fatal(err)
		}
		if err := confirmLiveSnapshot(ordered, op.AfterHash); err == nil {
			t.Fatal("old receipt after-hash accepted as full current state")
		}
		changed := r
		changed.Finalized = map[uint64]ManagedLiveHead{10: b.Blocks[0].Head}
		if _, err := exactLiveFinalizeRetry(changed, m.finalizeHash(changed), m.owner.s.leaseToken, op); err == nil {
			t.Fatal("changed no-op payload accepted")
		}
		var e managedLiveFinalizeEvidence
		_ = json.Unmarshal(op.Payload, &e)
		result := liveFinalizeResult(e, false)
		result.Finalized[10] = ManagedLiveHead{}
		result.Safe[1] = ManagedLiveHead{}
		if e.AfterControl.Finality[10].Checkpoint == "" || e.BeforeSafeHeads[1].Checkpoint == "" {
			t.Fatal("returned maps alias receipt state")
		}
	})
	t.Run("persistent_finality_rejects_later_competing_replay", func(t *testing.T) {
		m, d, b := liveReorgUnitHistory(t)
		liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "b9-confirmed", map[uint64]ManagedLiveHead{1: b.Blocks[9].Head}))
		r := liveReorgUnitRequest(d, b.Baselines[10])
		liveReorgUnitApply(t, m, &d, r)
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		competing := b.Blocks[3]
		competing.Head.Hash = fmt.Sprintf("0x%064x", 888)
		competing.InputDigest = managedHash("competing")
		candidate := ManagedLiveBatch{ID: "wrong-replay", Generation: 1, ExpectedLastOperation: r.ID, Blocks: []ManagedLiveBlock{b.Blocks[1], competing}}
		segments, _, after, err := planLiveBatch(m.opts, d.Control, candidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.validateBatchFinality(d, after, segments); err == nil {
			t.Fatal("finality conflict reached user handler admission")
		}
		// Bypass admission in the pure fixture to ensure full durable replay rejects it too.
		liveReorgUnitReplay(t, m, &d, candidate, nil)
		if _, err := m.validateDurable(d); err == nil {
			t.Fatal("terminal validator accepted competing replay below durable finalized head")
		}
	})
	t.Run("safe_and_own_finality_fences_after_prefix_pruning", func(t *testing.T) {
		m, d, b := liveFinalizeUnitPartial(t)
		baseline, _ := liveDurableBaselines(d.Operations)
		for _, ancestor := range []ManagedLiveHead{b.Baselines[10], b.Blocks[1].Head} {
			r := liveReorgUnitRequest(d, ancestor)
			if _, err := planLiveReorg(m.opts, d.Control, baseline, d.Segments, d.Journal, d.Proofs, r); !errors.Is(err, ErrManagedLiveDeepReorg) {
				t.Fatalf("deep finalized/safe reorg accepted: %v", err)
			}
		}
		r := liveReorgUnitRequest(d, b.Blocks[0].Head)
		if _, err := planLiveReorg(m.opts, d.Control, baseline, d.Segments, d.Journal, d.Proofs, r); err != nil {
			t.Fatalf("exact fault-chain safe/finalized ancestor rejected: %v", err)
		}
	})
}

// Acquire both B forks before finalizing either, then use an A reorg to leave
// B's private cursor at their common ancestor while retaining both proof sets.
func liveFinalizeUnitRetainedForks(t *testing.T) (*ManagedLive, managedLiveDurable, ManagedLiveHead, ManagedLiveHead) {
	t.Helper()
	m, d, b := liveReorgUnitHistory(t)
	original := liveSegmentsUnitBlock(1, 10, b.Blocks[9].Head.Hash)
	liveFinalizeUnitAppend(t, m, &d, ManagedLiveBatch{ID: "original-b10", Generation: 1, ExpectedLastOperation: d.Control.LastOperation, ExpectedTail: d.Control.Tail, Blocks: []ManagedLiveBlock{original}}, nil)
	r := liveReorgUnitRequest(d, b.Blocks[0].Head)
	r.ID = "rewind-before-alternate"
	liveReorgUnitApply(t, m, &d, r)
	blocks := []ManagedLiveBlock{b.Blocks[2]}
	parent := b.Blocks[1].Head.Hash
	for number := uint64(6); number <= 10; number++ {
		block := liveSegmentsUnitBlock(1, number, parent)
		block.Head.Hash = fmt.Sprintf("0x%064x", 100000+number)
		block.InputDigest = managedHash(fmt.Sprintf("alternate-b:%d", number))
		blocks = append(blocks, block)
		parent = block.Head.Hash
	}
	liveFinalizeUnitAppend(t, m, &d, ManagedLiveBatch{ID: "alternate-b10", Generation: 1, ExpectedLastOperation: d.Control.LastOperation, ExpectedTail: d.Control.Tail, Blocks: blocks}, nil)
	r = liveReorgUnitRequest(d, b.Blocks[0].Head)
	r.ID = "rewind-after-alternate"
	liveReorgUnitApply(t, m, &d, r)
	liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "original-b9-finalized", map[uint64]ManagedLiveHead{1: b.Blocks[9].Head}))
	if _, err := m.validateDurable(d); err != nil {
		t.Fatal(err)
	}
	if d.Control.Cursors[1] != b.Blocks[1].Head || d.Control.SafeOrdinal != 0 || d.Control.Finality[1] != b.Blocks[9].Head {
		t.Fatal("fixture must retain B9 finality above private B5 and safe B4")
	}
	proofs, err := liveProofMap(d.Proofs)
	if err != nil {
		t.Fatal(err)
	}
	alternate := blocks[len(blocks)-1].Head
	line, err := liveHeadLineage(alternate, b.Baselines[1], proofs)
	if err != nil || line[5] != b.Blocks[1].Head || line[9] == b.Blocks[9].Head || proofs[liveProofKey(b.Blocks[9].Head)] != b.Blocks[9].Head || proofs[liveProofKey(original.Head)] != original.Head {
		t.Fatal("fixture must contain both complete acquired forks sharing B5")
	}
	return m, d, original.Head, alternate
}

func TestManagedLiveFinalizeUnitRetainedFinalityLineage(t *testing.T) {
	t.Run("higher_competing_fork_rejected_above_low_private_cursor", func(t *testing.T) {
		m, d, _, alternate := liveFinalizeUnitRetainedForks(t)
		baseline, err := liveDurableBaselines(d.Operations)
		if err != nil {
			t.Fatal(err)
		}
		before := managedHash(d)
		r := liveFinalizeUnitRequest(d, "competing-b10-finality", map[uint64]ManagedLiveHead{1: alternate})
		if _, err := planLiveFinalize(m.opts, d.Control, baseline, d.Segments, d.Journal, d.Proofs, r); !errors.Is(err, ErrManagedLiveBatchConflict) {
			t.Fatalf("B9 finality switched to retained B10 prime above private B5: %v", err)
		}
		if managedHash(d) != before {
			t.Fatal("rejected finality mutated durable input")
		}
	})
	t.Run("same_fork_extension_accepted_above_low_private_cursor", func(t *testing.T) {
		m, d, original, _ := liveFinalizeUnitRetainedForks(t)
		beforeCursor := managedHash(d.Control.Cursors)
		plan := liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "original-b10-finality", map[uint64]ManagedLiveHead{1: original}))
		if !plan.Changed || len(plan.Pruned) != 0 || d.Control.SafeOrdinal != 0 || d.Control.Finality[1] != original || managedHash(d.Control.Cursors) != beforeCursor {
			t.Fatal("same-fork finality must advance without crossing unsafe A5 or changing private cursors")
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("full_receipt_replay_rejects_historical_fork_switch", func(t *testing.T) {
		m, d, _, alternate := liveFinalizeUnitRetainedForks(t)
		baseline, err := liveDurableBaselines(d.Operations)
		if err != nil {
			t.Fatal(err)
		}
		r := liveFinalizeUnitRequest(d, "forged-competing-b10", map[uint64]ManagedLiveHead{1: alternate})
		// Reconstruct the pre-fix accepted transition without calling admission.
		// Keep owner, generation, request, receipt chain and terminal state valid;
		// only replay's comparison with the earlier B9 finality can reject it.
		after := d.Control
		after.Finalizations++
		after.LastOperation = r.ID
		after.Finality = cloneLiveHeads(d.Control.Finality)
		after.Finality[1] = alternate
		plan := managedLiveFinalizePlan{After: after, Changed: true}
		e, err := makeLiveFinalizeEvidence(r, m.finalizeHash(r), d.Control, baseline, plan)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		previous := d.Operations[len(d.Operations)-1]
		op := managedLiveOperation{ID: r.ID, Generation: r.Generation, OwnerToken: m.owner.s.leaseToken, Kind: "finalize", RequestHash: m.finalizeHash(r), PreviousOperation: previous.ID, BeforeHash: previous.AfterHash, AfterHash: managedHash("unit-finalize:" + r.ID), Payload: raw}
		d.Operations = append(d.Operations, op)
		d.Control = after
		for i := range d.States {
			d.States[i].Finalized = after.Finality[d.States[i].ChainID].Checkpoint
		}
		if _, err := exactLiveFinalizeRetry(r, m.finalizeHash(r), m.owner.s.leaseToken, op); err != nil {
			t.Fatalf("fixture receipt must be locally exact: %v", err)
		}
		if err := validateLiveFinalityBranch(m.opts, baseline, after.Finality, liveSafeHeads(after, baseline), after.Cursors, d.Segments, d.Proofs); err != nil {
			t.Fatalf("fixture terminal branch must agree with low private/safe heads: %v", err)
		}
		if _, err := m.validateDurable(d); !errors.Is(err, ErrManagedLiveBatchConflict) {
			t.Fatalf("full receipt replay accepted historical B9 to B10 prime fork switch: %v", err)
		}
	})
}

func TestManagedLiveFinalizeUnitRejectInputs(t *testing.T) {
	mutations := map[string]func(*ManagedLiveFinalize){
		"unknown_chain":      func(r *ManagedLiveFinalize) { r.Finalized[99] = r.Finalized[1] },
		"empty_heads":        func(r *ManagedLiveFinalize) { r.Finalized = nil },
		"wrong_chain":        func(r *ManagedLiveFinalize) { r.Finalized[10] = r.Finalized[1] },
		"generation":         func(r *ManagedLiveFinalize) { r.Generation++ },
		"previous_operation": func(r *ManagedLiveFinalize) { r.ExpectedLastOperation = "other" },
		"tail":               func(r *ManagedLiveFinalize) { r.ExpectedTail++ },
		"safe":               func(r *ManagedLiveFinalize) { r.ExpectedSafeOrdinal++ },
		"missing_id":         func(r *ManagedLiveFinalize) { r.ID = "" },
		"not_block_end": func(r *ManagedLiveFinalize) {
			h := r.Finalized[1]
			h.Checkpoint = checkpoint.MustEncode(checkpoint.Block(105, 1, 9, false))
			r.Finalized[1] = h
		},
		"bad_hash": func(r *ManagedLiveFinalize) { h := r.Finalized[1]; h.Hash = "0x1"; r.Finalized[1] = h },
		"same_block_conflicting_hash": func(r *ManagedLiveFinalize) {
			h := r.Finalized[1]
			h.Hash = fmt.Sprintf("0x%064x", 777)
			r.Finalized[1] = h
		},
		"backwards": func(r *ManagedLiveFinalize) {
			h := r.Finalized[1]
			h.Checkpoint = checkpoint.MustEncode(checkpoint.Block(104, 1, 8, true))
			r.Finalized[1] = h
		},
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			m, d, b := liveFinalizeUnitPartial(t)
			baseline, _ := liveDurableBaselines(d.Operations)
			r := liveFinalizeUnitRequest(d, "bad-input", map[uint64]ManagedLiveHead{1: b.Blocks[9].Head})
			change(&r)
			if _, err := planLiveFinalize(m.opts, d.Control, baseline, d.Segments, d.Journal, d.Proofs, r); err == nil {
				t.Fatal("accepted malformed/regressing/conflicting finalize input")
			}
		})
	}
	t.Run("retained_competing_fork_cannot_confirm_active_branch", func(t *testing.T) {
		m, d, b := liveReorgUnitHistory(t)
		r := liveReorgUnitRequest(d, b.Baselines[1])
		liveReorgUnitApply(t, m, &d, r)
		alternate := b.Blocks[2]
		alternate.Head.Hash = fmt.Sprintf("0x%064x", 444)
		alternate.InputDigest = managedHash("alternate")
		replay := ManagedLiveBatch{ID: "alternate-unconfirmed-a6", Generation: 1, ExpectedLastOperation: r.ID, ExpectedTail: d.Control.Tail, Blocks: []ManagedLiveBlock{alternate}}
		liveFinalizeUnitAppend(t, m, &d, replay, nil)
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		baseline, _ := liveDurableBaselines(d.Operations)
		request := liveFinalizeUnitRequest(d, "wrong-retained-finality", map[uint64]ManagedLiveHead{10: b.Blocks[8].Head})
		if _, err := planLiveFinalize(m.opts, d.Control, baseline, d.Segments, d.Journal, d.Proofs, request); err == nil {
			t.Fatal("known retained proof on another fork confirmed current branch")
		}
	})
}

func TestManagedLiveFinalizeUnitTamper(t *testing.T) {
	changes := map[string]func(*managedLiveDurable){
		"safe_ordinal":                     func(d *managedLiveDurable) { d.Control.SafeOrdinal++ },
		"absolute_journal_boundary":        func(d *managedLiveDurable) { d.Control.SafeJournalBoundary++ },
		"lost_replay_proof":                func(d *managedLiveDurable) { d.Proofs = d.Proofs[1:] },
		"public_finalized_lowered_to_safe": func(d *managedLiveDurable) { d.States[0].Finalized = d.States[0].Safe },
		"public_latest_reset":              func(d *managedLiveDurable) { d.States[0].Latest = d.States[0].Safe },
		"private_cursor_reset":             func(d *managedLiveDurable) { d.States[0].Cursor = d.States[0].Safe },
		"ordinal_high_water":               func(d *managedLiveDurable) { d.Control.Next++ },
		"receipt_owner":                    func(d *managedLiveDurable) { d.Operations[3].OwnerToken = "other" },
		"receipt_generation":               func(d *managedLiveDurable) { d.Operations[3].Generation++ },
		"receipt_request_hash":             func(d *managedLiveDurable) { d.Operations[3].RequestHash = managedHash("other") },
		"receipt_before_hash":              func(d *managedLiveDurable) { d.Operations[3].BeforeHash = managedHash("other") },
	}
	for _, field := range []string{"pruned_empty_segment", "journal_before_row", "journal_order", "safe_head_hash", "after_catalog", "input_head", "changed_flag"} {
		field := field
		changes[field] = func(d *managedLiveDurable) {
			var e managedLiveFinalizeEvidence
			_ = json.Unmarshal(d.Operations[3].Payload, &e)
			switch field {
			case "pruned_empty_segment":
				e.Pruned = e.Pruned[:2]
			case "journal_before_row":
				var row managedLiveJournalEntry
				_ = json.Unmarshal([]byte(e.JournalJSON[0]), &row)
				row.Before = json.RawMessage(`{"id":"shared","balance":"wrong"}`)
				raw, _ := json.Marshal(row)
				e.JournalJSON[0] = string(raw)
			case "journal_order":
				e.JournalJSON[0], e.JournalJSON[1] = e.JournalJSON[1], e.JournalJSON[0]
			case "safe_head_hash":
				h := e.AfterControl.SafeHeads[10]
				h.Hash = fmt.Sprintf("0x%064x", 111)
				e.AfterControl.SafeHeads[10] = h
			case "after_catalog":
				e.AfterControl.CatalogHash = managedHash("unreceipted-catalog")
			case "input_head":
				e.Request.Finalized[10] = d.Control.FinalizedHeads[10]
			case "changed_flag":
				e.Changed = !e.Changed
			}
			d.Operations[3].Payload, _ = json.Marshal(e)
		}
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			m, d, b := liveReorgUnitHistory(t)
			liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "final-b", map[uint64]ManagedLiveHead{1: b.Blocks[9].Head}))
			liveFinalizeUnitApply(t, m, &d, liveFinalizeUnitRequest(d, "final-a6", map[uint64]ManagedLiveHead{10: b.Blocks[2].Head}))
			if d.Control.SafeOrdinal != 4 {
				t.Fatal("fixture lacks empty segment in prefix")
			}
			if _, err := m.validateDurable(d); err != nil {
				t.Fatal(err)
			}
			change(&d)
			if _, err := m.validateDurable(d); err == nil {
				t.Fatal("accepted corrupt finalized prefix/receipt")
			}
		})
	}
}

func TestManagedLiveFinalizeUnitRetryAndAdmission(t *testing.T) {
	m, d, b := liveReorgUnitHistory(t)
	r := liveFinalizeUnitRequest(d, "finalize-retry", map[uint64]ManagedLiveHead{1: b.Blocks[9].Head})
	acquired, err := m.RetainFinalize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	liveFinalizeUnitApply(t, m, &d, r)
	op := d.Operations[len(d.Operations)-1]
	for name, change := range map[string]func(*ManagedLiveFinalize){"id": func(r *ManagedLiveFinalize) { r.ID = "other" }, "generation": func(r *ManagedLiveFinalize) { r.Generation++ }, "tail": func(r *ManagedLiveFinalize) { r.ExpectedTail++ }, "safe": func(r *ManagedLiveFinalize) { r.ExpectedSafeOrdinal++ }, "previous": func(r *ManagedLiveFinalize) { r.ExpectedLastOperation = "other" }, "head": func(r *ManagedLiveFinalize) { r.Finalized[1] = b.Blocks[7].Head }} {
		t.Run(name, func(t *testing.T) {
			changed := r
			changed.Finalized = cloneLiveHeads(r.Finalized)
			change(&changed)
			if m.requireFinalizeAcquisition(changed, acquired) == nil {
				t.Fatal("retargeted acquired finality")
			}
			if _, err := exactLiveFinalizeRetry(changed, m.finalizeHash(changed), m.owner.s.leaseToken, op); err == nil {
				t.Fatal("retargeted exact receipt")
			}
		})
	}
	t.Run("busy", func(t *testing.T) {
		m.owner.s.mu.Lock()
		defer m.owner.s.mu.Unlock()
		if _, err := m.ApplyFinalize(context.Background(), r, acquired); !errors.Is(err, ErrManagedLiveBusy) {
			t.Fatal(err)
		}
	})
	t.Run("uncertain", func(t *testing.T) {
		m.uncertain.Store(true)
		defer m.uncertain.Store(false)
		if _, err := m.ApplyFinalize(context.Background(), r, acquired); !errors.Is(err, ErrManagedLiveUncertain) {
			t.Fatal(err)
		}
	})
	t.Run("lost", func(t *testing.T) {
		m.owner.lost.Store(true)
		defer m.owner.lost.Store(false)
		if _, err := m.ApplyFinalize(context.Background(), r, acquired); !errors.Is(err, ErrLeaseLost) {
			t.Fatal(err)
		}
	})
	t.Run("closed", func(t *testing.T) {
		m.owner.closed.Store(true)
		defer m.owner.closed.Store(false)
		if _, err := m.ApplyFinalize(context.Background(), ManagedLiveFinalize{}, nil); !errors.Is(err, ErrManagedLiveClosed) {
			t.Fatal(err)
		}
	})
}
