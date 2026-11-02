package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"ponder.local/ponder-go/checkpoint"
)

// Supplemental pure model/admission tests only. No SQL engine is started and
// these tests do not establish PostgreSQL/PGlite transaction durability.
func liveSegmentsUnitOwner(t *testing.T) (*ManagedLive, managedLiveDurable) {
	t.Helper()
	o, opts := liveTestOptions()
	opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
	var err error
	opts, err = normalizeManagedLiveOptions(o, opts)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{leaseToken: "unit-owner"}
	m := &ManagedLive{owner: &ManagedFinite{s: s, identity: managedHash("unit-plan")}, opts: opts}
	s.managedLive = m
	c := ManagedLiveControl{Version: managedLiveVersion, Generation: 1, Phase: "history", Next: 1, SafeAnchors: map[uint64]string{}, FinalizedHeads: cloneLiveHeads(opts.FinalizedHeads), HistoryCuts: m.historyCuts(), LastOperation: opts.OpenID, CatalogHash: managedHash("unit-bootstrap-catalog")}
	m.opened = c
	raw, _ := json.Marshal(c)
	op := managedLiveOperation{ID: opts.OpenID, Generation: 1, OwnerToken: s.leaseToken, Kind: "open", RequestHash: m.requestHash(), AfterHash: managedHash("unit-open-snapshot"), Payload: raw}
	return m, managedLiveDurable{Control: c, Operations: []managedLiveOperation{op}}
}

func liveSegmentsUnitBlock(chain, number uint64, parent string) ManagedLiveBlock {
	return ManagedLiveBlock{Head: ManagedLiveHead{Checkpoint: checkpoint.MustEncode(checkpoint.Block(100+number-4, chain, number, true)), Hash: fmt.Sprintf("0x%064x", chain*1000+number), ParentHash: parent}, InputDigest: managedHash(fmt.Sprintf("extracted:%d/%d", chain, number))}
}

func liveSegmentsUnitBatch(m *ManagedLive) ManagedLiveBatch {
	a := liveSegmentsUnitBlock(10, 5, m.opts.FinalizedHeads[10].Hash)
	b := liveSegmentsUnitBlock(1, 5, m.opts.FinalizedHeads[1].Hash)
	c := liveSegmentsUnitBlock(10, 6, a.Head.Hash)
	return ManagedLiveBatch{ID: "batch-a", Generation: 1, ExpectedLastOperation: m.opts.OpenID, Baselines: cloneLiveHeads(m.opts.FinalizedHeads), Blocks: []ManagedLiveBlock{a, b, c}}
}

func liveSegmentsUnitAppend(t *testing.T, m *ManagedLive, d *managedLiveDurable, request ManagedLiveBatch, writes [][]managedLiveJournalEntry) {
	t.Helper()
	batch, err := normalizeLiveBatch(m.opts, request)
	if err != nil {
		t.Fatal(err)
	}
	segments, before, after, err := planLiveBatch(m.opts, d.Control, batch)
	if err != nil {
		t.Fatal(err)
	}
	if d.Control.Batches == 0 {
		for _, chain := range m.opts.Chains {
			d.Proofs = append(d.Proofs, batch.Baselines[chain.ID])
			d.States = append(d.States, managedLiveCursorState{ChainID: chain.ID, Name: chain.Name, Finalized: batch.Baselines[chain.ID].Checkpoint, Safe: batch.Baselines[chain.ID].Checkpoint})
		}
	}
	var boundary int64
	if len(d.Journal) > 0 {
		boundary = d.Journal[len(d.Journal)-1].ID
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
		d.Proofs = append(d.Proofs, segments[i].Block.Head)
	}
	after.CatalogHash = managedHash("unit-capture-catalog")
	evidence := managedLiveBatchEvidence{Version: managedLiveBatchVersion, Batch: batch, AcquisitionHash: m.batchHash(batch), BeforeCursors: before, Segments: segments, AfterControl: after}
	raw, _ := json.Marshal(evidence)
	previous := d.Operations[len(d.Operations)-1]
	d.Operations = append(d.Operations, managedLiveOperation{ID: batch.ID, Generation: batch.Generation, OwnerToken: m.owner.s.leaseToken, Kind: "batch", RequestHash: m.batchHash(batch), PreviousOperation: previous.ID, BeforeHash: previous.AfterHash, AfterHash: managedHash("unit-snapshot:" + batch.ID), Payload: raw})
	d.Segments = append(d.Segments, segments...)
	d.Control = after
	for i := range d.States {
		d.States[i].Latest = after.Cursors[d.States[i].ChainID].Checkpoint
		d.States[i].Cursor = d.States[i].Latest
	}
}

func liveSegmentsUnitJournal(id int64, block ManagedLiveBlock, before string, operation int) managedLiveJournalEntry {
	cp, _ := checkpoint.Decode(block.Head.Checkpoint)
	return managedLiveJournalEntry{ID: id, Table: "account", Key: json.RawMessage(`{"id":"shared"}`), Before: json.RawMessage(before), Row: json.RawMessage(`{"id":"shared","value":1}`), Operation: operation, Checkpoint: block.Head.Checkpoint, ChainID: cp.ChainID}
}

func TestManagedLiveSegmentsUnitPlan(t *testing.T) {
	t.Run("global_input_order_and_empty_blocks", func(t *testing.T) {
		m, d := liveSegmentsUnitOwner(t)
		batch := liveSegmentsUnitBatch(m)
		segments, before, after, err := planLiveBatch(m.opts, d.Control, batch)
		if err != nil || len(segments) != 3 || after.Tail != 3 || after.Next != 4 || after.Batches != 1 {
			t.Fatalf("plan=%+v %v", after, err)
		}
		for i, wantChain := range []uint64{10, 1, 10} {
			segment := segments[i]
			cp, _ := checkpoint.Decode(segment.Block.Head.Checkpoint)
			if segment.Ordinal != uint64(i)+1 || segment.Predecessor != uint64(i) || cp.ChainID != wantChain {
				t.Fatal("segments were grouped/sorted by chain or journal instead of execution")
			}
		}
		before[10] = ManagedLiveHead{}
		if after.Cursors[10] != batch.Blocks[2].Head || batch.Baselines[10] != m.opts.FinalizedHeads[10] {
			t.Fatal("cursor containers alias")
		}
		liveSegmentsUnitAppend(t, m, &d, batch, nil)
		if _, err := m.validateDurable(d); err != nil {
			t.Fatal(err)
		}
		for _, segment := range d.Segments {
			if segment.Journal.Count != 0 || segment.Journal.Before != 0 || segment.Journal.After != 0 || segment.Journal.Digest != managedHash([]managedLiveJournalEntry{}) {
				t.Fatal("empty/no-DML block lost its explicit journal evidence")
			}
		}
	})
	t.Run("numeric_ordinals_9_10_11", func(t *testing.T) {
		m, d := liveSegmentsUnitOwner(t)
		c := d.Control
		c.Tail, c.Next, c.Batches, c.LastOperation, c.Cursors = 8, 9, 1, "previous-batch", cloneLiveHeads(m.opts.FinalizedHeads)
		batch := liveSegmentsUnitBatch(m)
		batch.Baselines, batch.ExpectedTail, batch.ExpectedLastOperation = nil, 8, c.LastOperation
		segments, _, after, err := planLiveBatch(m.opts, c, batch)
		if err != nil || after.Tail != 11 || after.Next != 12 {
			t.Fatal(err)
		}
		for i, segment := range segments {
			if segment.Ordinal != uint64(9+i) || segment.Predecessor != uint64(8+i) {
				t.Fatal("ordinal used text ordering or was reused")
			}
		}
	})
}

func TestManagedLiveSegmentsUnitRejectPlan(t *testing.T) {
	cases := map[string]func(*ManagedLive, *ManagedLiveControl, *ManagedLiveBatch){
		"generation":       func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) { b.Generation++ },
		"tail":             func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) { b.ExpectedTail++ },
		"predecessor":      func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) { b.ExpectedLastOperation = "other" },
		"missing_baseline": func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) { delete(b.Baselines, 1) },
		"foreign_baseline": func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) { b.Baselines[99] = b.Baselines[1] },
		"baseline_hash": func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) {
			h := b.Baselines[1]
			h.Hash = "0x" + strings.Repeat("11", 32)
			b.Baselines[1] = h
		},
		"gap": func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) {
			b.Blocks[0] = liveSegmentsUnitBlock(10, 6, b.Baselines[10].Hash)
		},
		"parent": func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) {
			b.Blocks[0].Head.ParentHash = "0x" + strings.Repeat("11", 32)
		},
		"end_bound": func(m *ManagedLive, _ *ManagedLiveControl, _ *ManagedLiveBatch) { m.opts.EndBlocks[10] = 4 },
		"not_block_end": func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) {
			b.Blocks[0].Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(101, 10, 5, false))
		},
		"empty_batch": func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) { b.Blocks = nil },
		"duplicate_block": func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) {
			b.Blocks = append(b.Blocks, b.Blocks[0])
		},
		"input_digest":      func(_ *ManagedLive, _ *ManagedLiveControl, b *ManagedLiveBatch) { b.Blocks[0].InputDigest = "" },
		"unsupported_safe":  func(_ *ManagedLive, c *ManagedLiveControl, _ *ManagedLiveBatch) { c.SafeOrdinal = 1 },
		"unsupported_phase": func(_ *ManagedLive, c *ManagedLiveControl, _ *ManagedLiveBatch) { c.Phase = "ready" },
		"next_tail":         func(_ *ManagedLive, c *ManagedLiveControl, _ *ManagedLiveBatch) { c.Next = 2 },
		"ordinal_exhaustion": func(m *ManagedLive, c *ManagedLiveControl, b *ManagedLiveBatch) {
			c.Tail, c.Next, c.Batches, c.Cursors = 9223372036854775805, 9223372036854775806, 1, cloneLiveHeads(m.opts.FinalizedHeads)
			b.ExpectedTail, b.Baselines = c.Tail, nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, d := liveSegmentsUnitOwner(t)
			batch := liveSegmentsUnitBatch(m)
			mutate(m, &d.Control, &batch)
			normalized, err := normalizeLiveBatch(m.opts, batch)
			if err == nil {
				_, _, _, err = planLiveBatch(m.opts, d.Control, normalized)
			}
			if err == nil {
				t.Fatal("invalid batch was admitted")
			}
		})
	}
}

func TestManagedLiveSegmentsUnitAcquisition(t *testing.T) {
	for _, field := range []string{"block_hash", "checkpoint", "input", "baseline", "op_id", "generation", "predecessor", "tail", "owner"} {
		t.Run(field, func(t *testing.T) {
			m, _ := liveSegmentsUnitOwner(t)
			batch := liveSegmentsUnitBatch(m)
			acquired, err := m.RetainBatch(context.Background(), batch)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "block_hash":
				batch.Blocks[0].Head.Hash = "0x" + strings.Repeat("11", 32)
			case "checkpoint":
				batch.Blocks[0].Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(102, 10, 5, true))
			case "input":
				batch.Blocks[0].InputDigest = managedHash("different-events")
			case "baseline":
				delete(batch.Baselines, 1)
			case "op_id":
				batch.ID = "different-op"
			case "generation":
				batch.Generation++
			case "predecessor":
				batch.ExpectedLastOperation = "different-predecessor"
			case "tail":
				batch.ExpectedTail++
			case "owner":
				other, _ := liveSegmentsUnitOwner(t)
				m = other
			}
			normalized, err := normalizeLiveBatch(m.opts, batch)
			if err == nil {
				err = m.requireAcquisition(normalized, acquired)
			}
			if err == nil {
				t.Fatal("retained acquisition was retargeted")
			}
		})
	}
}

func liveSegmentsUnitHistory(t *testing.T) (*ManagedLive, managedLiveDurable, ManagedLiveBatch) {
	t.Helper()
	m, d := liveSegmentsUnitOwner(t)
	batch := liveSegmentsUnitBatch(m)
	liveSegmentsUnitAppend(t, m, &d, batch, [][]managedLiveJournalEntry{{liveSegmentsUnitJournal(5, batch.Blocks[0], "null", 0)}, {liveSegmentsUnitJournal(12, batch.Blocks[1], `{"id":"shared","value":1}`, 1)}, nil})
	next := ManagedLiveBatch{ID: "batch-b", Generation: 1, ExpectedLastOperation: batch.ID, ExpectedTail: 3, Blocks: []ManagedLiveBlock{liveSegmentsUnitBlock(1, 6, batch.Blocks[1].Head.Hash)}}
	liveSegmentsUnitAppend(t, m, &d, next, [][]managedLiveJournalEntry{{liveSegmentsUnitJournal(27, next.Blocks[0], `{"id":"shared","value":2}`, 1)}})
	return m, d, batch
}

func TestManagedLiveSegmentsUnitExactRetry(t *testing.T) {
	m, d, first := liveSegmentsUnitHistory(t)
	// SQL query order is not operation order, even within one generation.
	d.Operations[0], d.Operations[2] = d.Operations[2], d.Operations[0]
	ordered, err := m.validateDurable(d)
	if err != nil || len(ordered) != 3 || ordered[0].Kind != "open" || ordered[1].ID != "batch-a" || ordered[2].ID != "batch-b" {
		t.Fatalf("invalid operation chain: %v", err)
	}
	if err := confirmLiveSnapshot(ordered, ordered[2].AfterHash); err != nil {
		t.Fatal(err)
	}
	if err := confirmLiveSnapshot(ordered, ordered[1].AfterHash); err == nil {
		t.Fatal("older snapshot was accepted just because latest passed the target")
	}
	evidence, err := exactLiveRetry(first, m.batchHash(first), m.owner.s.leaseToken, ordered[1])
	if err != nil || len(evidence.Segments) != 3 || evidence.Segments[2].Journal.Count != 0 {
		t.Fatal("exact old operation did not retain independent empty-segment evidence", err)
	}
	first.Blocks[0].InputDigest = managedHash("new input")
	if _, err := exactLiveRetry(first, m.batchHash(first), m.owner.s.leaseToken, ordered[1]); err == nil {
		t.Fatal("same opID with different evidence was accepted")
	}
}

func TestManagedLiveSegmentsUnitCorruption(t *testing.T) {
	cases := map[string]func(*managedLiveDurable){
		"missing_segment":     func(d *managedLiveDurable) { d.Segments = d.Segments[1:] },
		"text_segment_order":  func(d *managedLiveDurable) { d.Segments[0], d.Segments[1] = d.Segments[1], d.Segments[0] },
		"segment_predecessor": func(d *managedLiveDurable) { d.Segments[2].Predecessor = 0 },
		"missing_acquisition": func(d *managedLiveDurable) { d.Proofs = d.Proofs[1:] },
		"proof_checkpoint": func(d *managedLiveDurable) {
			d.Proofs[2].Checkpoint = checkpoint.MustEncode(checkpoint.Block(109, 10, 5, true))
		},
		"journal_before_row":  func(d *managedLiveDurable) { d.Journal[1].Before = json.RawMessage(`{"id":"shared","value":999}`) },
		"journal_wrong_chain": func(d *managedLiveDurable) { d.Journal[1].ChainID = 10 },
		"journal_order":       func(d *managedLiveDurable) { d.Journal[0], d.Journal[1] = d.Journal[1], d.Journal[0] },
		"empty_segment_span":  func(d *managedLiveDurable) { d.Segments[2].Journal.Count = 1 },
		"public_latest_ge_target": func(d *managedLiveDurable) {
			d.States[0].Latest = checkpoint.MustEncode(checkpoint.Block(999, d.States[0].ChainID, 19, true))
		},
		"public_finalized":    func(d *managedLiveDurable) { d.States[0].Finalized = d.States[0].Latest },
		"private_cursor":      func(d *managedLiveDurable) { d.States[1].Cursor = d.States[1].Safe },
		"receipt_owner":       func(d *managedLiveDurable) { d.Operations[1].OwnerToken = "foreign-owner" },
		"receipt_generation":  func(d *managedLiveDurable) { d.Operations[1].Generation = 2 },
		"receipt_before_hash": func(d *managedLiveDurable) { d.Operations[2].BeforeHash = managedHash("other-state") },
		"receipt_unbound": func(d *managedLiveDurable) {
			op := d.Operations[0]
			op.ID = "unbound"
			d.Operations = append(d.Operations, op)
		},
		"receipt_cycle":      func(d *managedLiveDurable) { d.Operations[2].PreviousOperation = d.Operations[2].ID },
		"control_generation": func(d *managedLiveDurable) { d.Control.Generation++ },
		"control_cursor":     func(d *managedLiveDurable) { d.Control.Cursors[1] = d.Control.FinalizedHeads[1] },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, d, _ := liveSegmentsUnitHistory(t)
			if _, err := m.validateDurable(d); err != nil {
				t.Fatal("invalid positive fixture", err)
			}
			mutate(&d)
			if _, err := m.validateDurable(d); err == nil {
				t.Fatal("unreceipted durable mutation was accepted")
			}
		})
	}
	t.Run("segment_loss_preserves_independent_operation_proof", func(t *testing.T) {
		m, d, _ := liveSegmentsUnitHistory(t)
		payload := append([]byte(nil), d.Operations[1].Payload...)
		d.Segments = nil
		if _, err := m.validateDurable(d); err == nil {
			t.Fatal("unsupported pruning was admitted")
		}
		if string(payload) != string(d.Operations[1].Payload) {
			t.Fatal("operation evidence depended on the segment container")
		}
		var e managedLiveBatchEvidence
		if err := decodeLiveJSON(payload, &e); err != nil || len(e.Segments) != 3 {
			t.Fatal("independent receipt did not retain segment/journal/proof evidence", err)
		}
	})
}

func TestManagedLiveSegmentsUnitGenerations(t *testing.T) {
	m, d := liveSegmentsUnitOwner(t)
	d.Operations = nil
	before := ""
	for generation := uint64(1); generation <= 12; generation++ {
		c := d.Control
		c.Generation, c.LastOperation = generation, fmt.Sprintf("open-%d", generation)
		payload, _ := json.Marshal(c)
		opts := m.opts
		opts.OpenID = c.LastOperation
		request := (&ManagedLive{owner: m.owner, opts: opts}).requestHash()
		op := managedLiveOperation{ID: c.LastOperation, Generation: generation, Kind: "open", OwnerToken: fmt.Sprintf("owner-%d", generation), RequestHash: request, BeforeHash: before, AfterHash: managedHash(c.LastOperation), Payload: payload}
		if len(d.Operations) > 0 {
			op.PreviousOperation = d.Operations[len(d.Operations)-1].ID
		}
		d.Operations = append(d.Operations, op)
		before = op.AfterHash
		d.Control = c
	}
	m.owner.s.leaseToken, m.opened.Generation, m.opts.OpenID = "owner-12", 12, "open-12"
	sort.Slice(d.Operations, func(i, j int) bool { return d.Operations[i].ID < d.Operations[j].ID })
	ordered, err := m.validateDurable(d)
	if err != nil || len(ordered) != 12 {
		t.Fatal(err)
	}
	for i, op := range ordered {
		if op.Generation != uint64(i)+1 {
			t.Fatal("generation used lexical receipt order")
		}
	}
}

func TestManagedLiveSegmentsUnitAdmissionFences(t *testing.T) {
	for _, state := range []string{"closed", "uncertain", "lost", "busy", "foreign_acquisition"} {
		t.Run(state, func(t *testing.T) {
			m, _ := liveSegmentsUnitOwner(t)
			batch := liveSegmentsUnitBatch(m)
			acquired, err := m.RetainBatch(context.Background(), batch)
			if err != nil {
				t.Fatal(err)
			}
			want := ErrManagedLiveBatchConflict
			switch state {
			case "closed":
				m.owner.closed.Store(true)
				want = ErrManagedLiveClosed
			case "uncertain":
				m.uncertain.Store(true)
				want = ErrManagedLiveUncertain
			case "lost":
				m.owner.lost.Store(true)
				want = ErrLeaseLost
			case "busy":
				m.owner.s.mu.Lock()
				defer m.owner.s.mu.Unlock()
				want = ErrManagedLiveBusy
			case "foreign_acquisition":
				other, _ := liveSegmentsUnitOwner(t)
				acquired.owner = other
			}
			// Backend is nil: any SQL admission here would fail this pure test.
			_, err = m.ApplyBatch(context.Background(), batch, acquired, nil)
			if !errors.Is(err, want) {
				t.Fatalf("admission=%v, want %v", err, want)
			}
		})
	}
	t.Run("legacy_writer_and_session_controls", func(t *testing.T) {
		m, _ := liveSegmentsUnitOwner(t)
		w := &Writer{s: m.owner.s}
		if err := w.CommitBlock(context.Background(), "account", checkpoint.Checkpoint{}); !errors.Is(err, ErrManagedLiveLegacyMutation) {
			t.Fatal(err)
		}
		if err := w.AdvanceLatest(context.Background(), checkpoint.Checkpoint{}); !errors.Is(err, ErrManagedLiveLegacyMutation) {
			t.Fatal(err)
		}
		if _, err := NewIndexingSession(m.owner.s).Bind(w); !errors.Is(err, ErrManagedLiveLegacyMutation) {
			t.Fatal(err)
		}
		w.closed.Store(true)
		if err := w.CommitBlock(context.Background(), "account", checkpoint.Checkpoint{}); errors.Is(err, ErrManagedLiveLegacyMutation) || err == nil {
			t.Fatal("closed guard precedence", err)
		}
	})
}

func TestManagedLiveSegmentsUnitStrictPayload(t *testing.T) {
	for _, raw := range []string{`{"ordinal":-1}`, `{"ordinal":18446744073709551616}`, `{"unknown":1}`, `{} {}`} {
		var segment ManagedLiveSegment
		if err := decodeLiveJSON([]byte(raw), &segment); err == nil {
			t.Fatal("invalid payload accepted", raw)
		}
	}
}
