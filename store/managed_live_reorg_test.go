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

// These are pure projection, archive and admission tests. No SQL engine is
// started; passing these does not establish new reorg SQL durability.
func liveReorgUnitRow(id int64, block ManagedLiveBlock, before, value string) managedLiveJournalEntry {
	cp, _ := checkpoint.Decode(block.Head.Checkpoint)
	previous := json.RawMessage("null")
	if before != "" {
		previous = json.RawMessage(fmt.Sprintf(`{"balance":%q,"id":"shared"}`, before))
	}
	operation := 1
	if before == "" {
		operation = 0
	}
	return managedLiveJournalEntry{ID: id, Table: "account", Key: json.RawMessage(`{"id":"shared"}`), Before: previous, Row: json.RawMessage(fmt.Sprintf(`{"balance":%q,"id":"shared"}`, value)), Operation: operation, Checkpoint: block.Head.Checkpoint, ChainID: cp.ChainID}
}

func liveReorgUnitHistory(t *testing.T) (*ManagedLive, managedLiveDurable, ManagedLiveBatch) {
	t.Helper()
	m, d := liveSegmentsUnitOwner(t)
	batch := ManagedLiveBatch{ID: "ten-segments", Generation: 1, ExpectedLastOperation: m.opts.OpenID, Baselines: cloneLiveHeads(m.opts.FinalizedHeads)}
	heads := cloneLiveHeads(batch.Baselines)
	writes := make([][]managedLiveJournalEntry, 10)
	ids := map[int]int64{0: 2, 1: 7, 3: 10, 4: 14, 6: 20, 7: 27, 9: 31}
	previous := ""
	for i := 0; i < 10; i++ {
		chain := uint64(10)
		if i%2 == 1 {
			chain = 1
		}
		prior, _ := checkpoint.Decode(heads[chain].Checkpoint)
		block := liveSegmentsUnitBlock(chain, prior.BlockNumber+1, heads[chain].Hash)
		batch.Blocks = append(batch.Blocks, block)
		heads[chain] = block.Head
		if id, ok := ids[i]; ok {
			value := fmt.Sprint((i + 1) * 10)
			writes[i] = []managedLiveJournalEntry{liveReorgUnitRow(id, block, previous, value)}
			previous = value
		}
	}
	liveSegmentsUnitAppend(t, m, &d, batch, writes)
	if _, err := m.validateDurable(d); err != nil {
		t.Fatal(err)
	}
	return m, d, batch
}

func liveReorgUnitRequest(d managedLiveDurable, ancestor ManagedLiveHead) ManagedLiveReorg {
	return ManagedLiveReorg{ID: "reorg-a", Generation: d.Control.Generation, ExpectedLastOperation: d.Control.LastOperation, ExpectedTail: d.Control.Tail, Ancestor: ancestor}
}

func liveReorgUnitApply(t *testing.T, m *ManagedLive, d *managedLiveDurable, r ManagedLiveReorg) managedLiveReorgPlan {
	t.Helper()
	baseline, err := liveDurableBaselines(d.Operations)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planLiveReorg(m.opts, d.Control, baseline, d.Segments, d.Journal, d.Proofs, r)
	if err != nil {
		t.Fatal(err)
	}
	e, err := makeLiveReorgEvidence(r, m.reorgHash(r), d.Control, plan)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	prior := d.Operations[len(d.Operations)-1]
	d.Operations = append(d.Operations, managedLiveOperation{ID: r.ID, Generation: r.Generation, OwnerToken: m.owner.s.leaseToken, Kind: "reorg", RequestHash: m.reorgHash(r), PreviousOperation: r.ExpectedLastOperation, BeforeHash: prior.AfterHash, AfterHash: managedHash("unit-reorg:" + r.ID), Payload: raw})
	d.Segments = append([]ManagedLiveSegment(nil), d.Segments[:len(d.Segments)-len(plan.Removed)]...)
	keep := 0
	for keep < len(d.Journal) && d.Journal[keep].ID <= plan.JournalBoundary {
		keep++
	}
	d.Journal = append([]managedLiveJournalEntry(nil), d.Journal[:keep]...)
	d.Proofs = append([]ManagedLiveHead(nil), plan.Proofs...)
	d.Control = plan.After
	for i := range d.States {
		d.States[i].Cursor = plan.After.Cursors[d.States[i].ChainID].Checkpoint
	}
	return plan
}

func liveReorgUnitReplay(t *testing.T, m *ManagedLive, d *managedLiveDurable, b ManagedLiveBatch, writes [][]managedLiveJournalEntry) {
	t.Helper()
	priorLatest := map[uint64]string{}
	for _, state := range d.States {
		priorLatest[state.ChainID] = state.Latest
	}
	liveSegmentsUnitAppend(t, m, d, b, writes)
	// The legacy append helper predates retained-proof replay. Model the actual
	// strict insert: reuse an equal proof and reject differing same-key evidence.
	byKey := map[string]ManagedLiveHead{}
	unique := []ManagedLiveHead{}
	for _, head := range d.Proofs {
		key := liveProofKey(head)
		if old, exists := byKey[key]; exists {
			if old != head {
				t.Fatal("replay retargeted retained proof")
			}
			continue
		}
		byKey[key] = head
		unique = append(unique, head)
	}
	d.Proofs = unique
	for _, block := range b.Blocks {
		cp, _ := checkpoint.Decode(block.Head.Checkpoint)
		priorLatest[cp.ChainID] = block.Head.Checkpoint
	}
	for i := range d.States {
		d.States[i].Latest = priorLatest[d.States[i].ChainID]
	}
}

func TestManagedLiveReorgUnit(t *testing.T) {
	t.Run("empty_orphan_starts_global_shared_row_suffix_numeric_10_7", func(t *testing.T) {
		m, d, batch := liveReorgUnitHistory(t)
		oldLatest := map[uint64]string{}
		for _, s := range d.States {
			oldLatest[s.ChainID] = s.Latest
		}
		r := liveReorgUnitRequest(d, batch.Blocks[0].Head)
		plan := liveReorgUnitApply(t, m, &d, r)
		if len(plan.Removed) != 8 || plan.Removed[0].Ordinal != 3 || plan.Removed[0].Journal.Count != 0 || plan.Removed[7].Ordinal != 10 || plan.After.Tail != 2 || plan.After.Next != 11 || plan.After.Reorgs != 1 || len(plan.Replay) != 4 {
			t.Fatalf("global empty-segment cut=%+v", plan)
		}
		if plan.After.Cursors[10] != batch.Blocks[0].Head || plan.After.Cursors[1] != batch.Blocks[1].Head {
			t.Fatal("only fault chain cursor was rewound")
		}
		undo := liveReorgUndoRows(plan.Undo)
		ids := []string{}
		balance := "100"
		for _, row := range undo.Rows {
			ids = append(ids, fmt.Sprint(row["id"]))
			if row["before"] == nil {
				balance = "missing"
			} else {
				var b map[string]any
				if json.Unmarshal([]byte(fmt.Sprint(row["before"])), &b) != nil {
					t.Fatal("invalid before row")
				}
				balance = fmt.Sprint(b["balance"])
			}
		}
		if !reflect.DeepEqual(ids, []string{"31", "27", "20", "14", "10"}) || balance != "20" {
			t.Fatalf("global reverse execution undo IDs=%v balance=%s", ids, balance)
		}
		for _, state := range d.States {
			if state.Latest != oldLatest[state.ChainID] || state.Finalized != batch.Baselines[state.ChainID].Checkpoint || state.Safe != state.Finalized {
				t.Fatal("reorg changed public latest/finalized/safe")
			}
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		var original managedLiveBatchEvidence
		if json.Unmarshal(d.Operations[1].Payload, &original) != nil || len(original.Segments) != 10 || len(d.Operations) != 3 {
			t.Fatal("independent batch receipt was pruned with segments")
		}
		// Public result maps must never alias receipt state.
		evidence, _ := makeLiveReorgEvidence(r, m.reorgHash(r), ManagedLiveControl{}, plan)
		result := liveReorgResult(evidence, false)
		result.Cursors[10] = ManagedLiveHead{}
		if evidence.AfterControl.Cursors[10].Checkpoint == "" {
			t.Fatal("public result aliases receipt cursors")
		}
	})
	t.Run("all_empty_suffix_keeps_baselines_receipts_and_high_water", func(t *testing.T) {
		m, d := liveSegmentsUnitOwner(t)
		b := liveSegmentsUnitBatch(m)
		liveSegmentsUnitAppend(t, m, &d, b, nil)
		r := liveReorgUnitRequest(d, b.Baselines[10])
		plan := liveReorgUnitApply(t, m, &d, r)
		if len(plan.Undo) != 0 || len(plan.Removed) != 3 || d.Control.Tail != 0 || d.Control.Next != 4 || d.Control.Batches != 1 || len(d.States) != 2 {
			t.Fatal("empty suffix lost explicit processed baseline")
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		replay := ManagedLiveBatch{ID: "replay-empty", Generation: 1, ExpectedLastOperation: r.ID, Blocks: []ManagedLiveBlock{b.Blocks[1]}}
		liveReorgUnitReplay(t, m, &d, replay, nil)
		if d.Segments[0].Ordinal != 4 || d.Segments[0].Predecessor != 0 {
			t.Fatal("high-water ordinal reused after full undo")
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("replay_does_not_mix_archived_ID_range_or_reuse_ordinals", func(t *testing.T) {
		m, d, b := liveReorgUnitHistory(t)
		r := liveReorgUnitRequest(d, b.Blocks[0].Head)
		liveReorgUnitApply(t, m, &d, r)
		replay := ManagedLiveBatch{ID: "replay-1", Generation: 1, ExpectedLastOperation: r.ID, ExpectedTail: 2, Blocks: []ManagedLiveBlock{b.Blocks[3]}}
		liveReorgUnitReplay(t, m, &d, replay, [][]managedLiveJournalEntry{{liveReorgUnitRow(40, b.Blocks[3], "20", "40")}})
		last := d.Segments[len(d.Segments)-1]
		if last.Ordinal != 11 || last.Predecessor != 2 || last.Journal.Before != 7 || last.Journal.After != 40 || last.Journal.Count != 1 {
			t.Fatalf("replay range=%+v", last)
		}
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		// A replay can move only that chain's public latest below its stale pre-reorg latest.
		for _, state := range d.States {
			if state.ChainID == 1 && state.Latest != b.Blocks[3].Head.Checkpoint {
				t.Fatal("public replay latest shortcut")
			}
			if state.ChainID == 10 && (state.Latest != b.Blocks[8].Head.Checkpoint || state.Cursor != b.Blocks[0].Head.Checkpoint) {
				t.Fatal("other chain public/private were conflated")
			}
		}
		r2 := liveReorgUnitRequest(d, b.Blocks[1].Head)
		r2.ID = "reorg-b"
		liveReorgUnitApply(t, m, &d, r2)
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		replay.ID, replay.ExpectedLastOperation = "replay-2", r2.ID
		liveReorgUnitReplay(t, m, &d, replay, [][]managedLiveJournalEntry{{liveReorgUnitRow(60, b.Blocks[3], "20", "40")}})
		if d.Segments[len(d.Segments)-1].Ordinal != 12 {
			t.Fatal("second reorg reused ordinal 11")
		}
		ordered, err := m.validateDurable(d)
		if err != nil {
			t.Fatal(err)
		}
		op := d.Operations[2]
		if _, err := exactLiveReorgRetry(r, m.reorgHash(r), m.owner.s.leaseToken, op); err != nil {
			t.Fatal(err)
		}
		if err := confirmLiveSnapshot(ordered, ordered[len(ordered)-1].AfterHash); err != nil {
			t.Fatal(err)
		}
		if err := confirmLiveSnapshot(ordered, op.AfterHash); err == nil {
			t.Fatal("old reorg after-hash accepted as current durable state")
		}
	})
	t.Run("retained_other_chain_proof_is_not_executed_ancestor", func(t *testing.T) {
		m, d, b := liveReorgUnitHistory(t)
		liveReorgUnitApply(t, m, &d, liveReorgUnitRequest(d, b.Blocks[0].Head))
		r := liveReorgUnitRequest(d, b.Blocks[9].Head)
		baselines, _ := liveDurableBaselines(d.Operations)
		if _, err := planLiveReorg(m.opts, d.Control, baselines, d.Segments, d.Journal, d.Proofs, r); err == nil {
			t.Fatal("retained B9 proof promoted rewound B5 cursor")
		}
	})
	t.Run("durable_safe_baseline_and_global_ordinal_fences", func(t *testing.T) {
		m, d, b := liveReorgUnitHistory(t)
		baselines, _ := liveDurableBaselines(d.Operations)
		r := liveReorgUnitRequest(d, liveSegmentsUnitBlock(10, 3, b.Baselines[10].ParentHash).Head)
		if _, err := planLiveReorg(m.opts, d.Control, baselines, d.Segments, d.Journal, d.Proofs, r); !errors.Is(err, ErrManagedLiveDeepReorg) {
			t.Fatalf("deep baseline=%v", err)
		}
		c := d.Control
		c.SafeOrdinal = 2
		c.SafeAnchors = map[uint64]string{10: b.Blocks[0].Head.Checkpoint, 1: b.Blocks[1].Head.Checkpoint}
		c.SafeHeads = map[uint64]ManagedLiveHead{10: b.Blocks[0].Head, 1: b.Blocks[1].Head}
		c.SafeJournalBoundary = 7
		c.Finalizations = 1
		c.Finality = cloneLiveHeads(c.SafeHeads)
		r = liveReorgUnitRequest(d, b.Blocks[0].Head)
		if _, err := planLiveReorg(m.opts, c, baselines, d.Segments[2:], d.Journal[2:], d.Proofs, r); err != nil {
			t.Fatalf("exact modeled safe boundary rejected: %v", err)
		}
		c.SafeOrdinal = 3
		c.SafeAnchors[10] = b.Blocks[2].Head.Checkpoint
		c.SafeHeads[10] = b.Blocks[2].Head
		c.Finality[10] = b.Blocks[2].Head
		if _, err := planLiveReorg(m.opts, c, baselines, d.Segments[3:], d.Journal[2:], d.Proofs, r); !errors.Is(err, ErrManagedLiveDeepReorg) {
			t.Fatalf("global ordinal fence=%v", err)
		}
		// Without an actual finalize receipt, modeled safe structure is not adoptable.
		d.Control = c
		if _, err := m.validateDurable(d); err == nil {
			t.Fatal("unimplemented safe advancement silently adopted")
		}
	})
	t.Run("archive_text_preserves_noncanonical_JSON_key_order", func(t *testing.T) {
		m, d, b := liveReorgUnitHistory(t)
		r := liveReorgUnitRequest(d, b.Blocks[0].Head)
		baselines, _ := liveDurableBaselines(d.Operations)
		plan, err := planLiveReorg(m.opts, d.Control, baselines, d.Segments, d.Journal, d.Proofs, r)
		if err != nil {
			t.Fatal(err)
		}
		e, err := makeLiveReorgEvidence(r, m.reorgHash(r), d.Control, plan)
		if err != nil {
			t.Fatal(err)
		}
		before := managedHash(plan.Undo)
		raw, _ := json.Marshal(e)
		var outer map[string]any
		if json.Unmarshal(raw, &outer) != nil {
			t.Fatal("outer payload")
		}
		raw, _ = json.Marshal(outer)
		var round managedLiveReorgEvidence
		if json.Unmarshal(raw, &round) != nil {
			t.Fatal("round trip payload")
		}
		rows, err := decodeLiveReorgUndo(round)
		if err != nil || managedHash(rows) != before {
			t.Fatalf("archive reordered nested raw JSON: %v", err)
		}
	})
}

func TestManagedLiveReorgUnitTamper(t *testing.T) {
	mutations := map[string]func(*managedLiveDurable){
		"missing_active_segment": func(d *managedLiveDurable) { d.Segments = d.Segments[1:] },
		"numeric_active_order":   func(d *managedLiveDurable) { d.Segments[0], d.Segments[1] = d.Segments[1], d.Segments[0] },
		"lost_retained_proof":    func(d *managedLiveDurable) { d.Proofs = d.Proofs[1:] },
		"reset_public_latest":    func(d *managedLiveDurable) { d.States[0].Latest = d.States[0].Cursor },
		"reset_public_finalized": func(d *managedLiveDurable) { d.States[0].Finalized = d.States[0].Cursor },
		"private_cursor_tamper":  func(d *managedLiveDurable) { d.States[0].Cursor = d.States[0].Latest },
		"high_water_rewind":      func(d *managedLiveDurable) { d.Control.Next = d.Control.Tail + 1 },
		"receipt_owner":          func(d *managedLiveDurable) { d.Operations[2].OwnerToken = "another" },
		"receipt_generation":     func(d *managedLiveDurable) { d.Operations[2].Generation++ },
		"receipt_request_hash":   func(d *managedLiveDurable) { d.Operations[2].RequestHash = managedHash("other") },
		"receipt_chain":          func(d *managedLiveDurable) { d.Operations[2].BeforeHash = managedHash("other") },
	}
	for _, field := range []string{"removed_empty_segment", "undo_before_row", "undo_order", "duplicate_undo_ID", "after_cursor", "acquired_ancestor"} {
		field := field
		mutations[field] = func(d *managedLiveDurable) {
			var e managedLiveReorgEvidence
			_ = json.Unmarshal(d.Operations[2].Payload, &e)
			switch field {
			case "removed_empty_segment":
				e.Removed = e.Removed[1:]
			case "undo_before_row":
				var row managedLiveJournalEntry
				_ = json.Unmarshal([]byte(e.UndoJSON[0]), &row)
				row.Before = json.RawMessage(`{"id":"shared","balance":"wrong"}`)
				raw, _ := json.Marshal(row)
				e.UndoJSON[0] = string(raw)
			case "undo_order":
				e.UndoJSON[0], e.UndoJSON[1] = e.UndoJSON[1], e.UndoJSON[0]
			case "duplicate_undo_ID":
				e.UndoJSON[1] = e.UndoJSON[0]
			case "after_cursor":
				e.AfterControl.Cursors[1] = e.Request.Ancestor
			case "acquired_ancestor":
				e.Request.Ancestor.Hash = fmt.Sprintf("0x%064x", 99)
			}
			d.Operations[2].Payload, _ = json.Marshal(e)
		}
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			m, d, b := liveReorgUnitHistory(t)
			liveReorgUnitApply(t, m, &d, liveReorgUnitRequest(d, b.Blocks[0].Head))
			change(&d)
			if _, err := m.validateDurable(d); err == nil {
				t.Fatal("accepted corrupted reorg durable state")
			}
		})
	}
}

func TestManagedLiveReorgUnitRetryAndAdmission(t *testing.T) {
	m, d, b := liveReorgUnitHistory(t)
	r := liveReorgUnitRequest(d, b.Blocks[0].Head)
	acquired, err := m.RetainReorg(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	liveReorgUnitApply(t, m, &d, r)
	op := d.Operations[2]
	for name, change := range map[string]func(*ManagedLiveReorg){
		"id": func(r *ManagedLiveReorg) { r.ID = "other" }, "generation": func(r *ManagedLiveReorg) { r.Generation++ }, "tail": func(r *ManagedLiveReorg) { r.ExpectedTail++ }, "previous": func(r *ManagedLiveReorg) { r.ExpectedLastOperation = "other" }, "hash": func(r *ManagedLiveReorg) { r.Ancestor.Hash = fmt.Sprintf("0x%064x", 111) }, "checkpoint": func(r *ManagedLiveReorg) { r.Ancestor.Checkpoint = b.Blocks[1].Head.Checkpoint }, "parent": func(r *ManagedLiveReorg) { r.Ancestor.ParentHash = fmt.Sprintf("0x%064x", 222) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := r
			change(&changed)
			if m.requireReorgAcquisition(changed, acquired) == nil {
				t.Fatal("retargeted retained acquisition")
			}
			if _, err := exactLiveReorgRetry(changed, m.reorgHash(changed), m.owner.s.leaseToken, op); err == nil {
				t.Fatal("retargeted exact retry")
			}
		})
	}
	t.Run("busy_before_backend", func(t *testing.T) {
		m.owner.s.mu.Lock()
		defer m.owner.s.mu.Unlock()
		if _, err := m.ApplyReorg(context.Background(), r, acquired); !errors.Is(err, ErrManagedLiveBusy) {
			t.Fatal(err)
		}
	})
	t.Run("uncertain_before_backend", func(t *testing.T) {
		m.uncertain.Store(true)
		defer m.uncertain.Store(false)
		if _, err := m.ApplyReorg(context.Background(), r, acquired); !errors.Is(err, ErrManagedLiveUncertain) {
			t.Fatal(err)
		}
	})
	t.Run("closed_priority", func(t *testing.T) {
		m.owner.closed.Store(true)
		defer m.owner.closed.Store(false)
		if _, err := m.ApplyReorg(context.Background(), ManagedLiveReorg{}, nil); !errors.Is(err, ErrManagedLiveClosed) {
			t.Fatal(err)
		}
	})
}
