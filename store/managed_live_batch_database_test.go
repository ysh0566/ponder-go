package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ponder.local/ponder-go/checkpoint"
)

// Supplemental native PostgreSQL controls. The fixture verifies a fresh,
// explicitly named cluster before constructing any owner; no source credit.
func liveBatchPGFixture(t *testing.T) (IndependentStorageOptions, ManagedLiveOptions, func(ManagedLiveOptions) (*IndependentStorage, error)) {
	t.Helper()
	o, opts, open := liveDBFixture(t, "postgres")
	opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
	return o, opts, open
}

func liveBatchPGProbe(t *testing.T, o IndependentStorageOptions) Backend {
	t.Helper()
	b, err := OpenPostgres(context.Background(), o.PostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	return b
}

func liveBatchPGRetain(t *testing.T, m *ManagedLive, batch ManagedLiveBatch) *ManagedLiveAcquisition {
	t.Helper()
	acquired, err := m.RetainBatch(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	return acquired
}

func liveBatchPGCounts(t *testing.T, q Querier, s *Store, segments, proofs, operations, journal int) {
	t.Helper()
	for _, item := range []struct {
		table string
		want  int
	}{{"_ponder_go_live_segments", segments}, {"_ponder_go_live_proofs", proofs}, {"_ponder_go_live_operations", operations}, {"_ponder_go_journal", journal}} {
		r, err := q.Query(context.Background(), "SELECT count(*)::text AS count FROM "+s.internal(item.table))
		if err != nil || len(r.Rows) != 1 || r.Rows[0]["count"] != fmt.Sprint(item.want) {
			t.Fatalf("durable %s want=%d rows=%v err=%v", item.table, item.want, r.Rows, err)
		}
	}
}

func liveBatchPGBalance(t *testing.T, q Querier, s *Store, want string) {
	t.Helper()
	r, err := q.Query(context.Background(), "SELECT balance::text AS balance FROM "+s.table("account")+" WHERE id='shared'")
	if err != nil {
		t.Fatal(err)
	}
	if want == "missing" {
		if len(r.Rows) != 0 {
			t.Fatalf("rolled-back business row survived: %v", r.Rows)
		}
	} else if len(r.Rows) != 1 || r.Rows[0]["balance"] != want {
		t.Fatalf("business balance want=%s rows=%v", want, r.Rows)
	}
}

func liveBatchPGSharedHandler(t *testing.T, calls *int, saved **Writer) func(*Writer, ManagedLiveBlock) error {
	t.Helper()
	return func(w *Writer, block ManagedLiveBlock) error {
		*calls++
		*saved = w
		cp, _ := checkpoint.Decode(block.Head.Checkpoint)
		if cp.ChainID == 10 && cp.BlockNumber == 5 {
			_, err := w.Indexing().InsertOne(context.Background(), "account", Row{"id": "shared", "balance": "10"}, Conflict{})
			return err
		}
		if cp.ChainID == 1 {
			row, err := w.Find(context.Background(), "account", Row{"id": "shared"})
			if err != nil || fmt.Sprint(row["balance"]) != "10" {
				return fmt.Errorf("later chain cannot see earlier block flush: row=%v err=%v", row, err)
			}
			return w.Write(context.Background(), "account", Row{"id": "shared", "balance": "20"}, Update)
		}
		return nil // Explicit no-DML block still gets its own segment.
	}
}

func TestManagedLiveBatchPostgres(t *testing.T) {
	ctx := context.Background()
	t.Run("empty_blocks_persist_after_owner_close", func(t *testing.T) {
		o, opts, open := liveBatchPGFixture(t)
		s, err := open(opts)
		if err != nil {
			t.Fatal(err)
		}
		m, st := s.ManagedLive(), s.BusinessStore()
		batch := liveSegmentsUnitBatch(m)
		result, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), nil)
		if err != nil || !result.Applied || len(result.Segments) != 3 {
			t.Fatalf("empty apply=%+v err=%v", result, err)
		}
		for i, segment := range result.Segments {
			if segment.Ordinal != uint64(i+1) || segment.Predecessor != uint64(i) || segment.Journal.Count != 0 || segment.Journal.Before != 0 || segment.Journal.After != 0 || !liveDigest(segment.Journal.Digest) {
				t.Fatalf("empty segment %d lost its explicit boundary: %+v", i, segment)
			}
		}
		c, err := m.Control(ctx)
		if err != nil || c.Tail != 3 || c.Next != 4 || c.Batches != 1 || c.Cursors[1] != batch.Blocks[1].Head || c.Cursors[10] != batch.Blocks[2].Head {
			t.Fatalf("empty durable control=%+v err=%v", c, err)
		}
		probe := liveBatchPGProbe(t, o)
		liveBatchPGCounts(t, probe, st, 3, 5, 2, 0)
		states, err := checkpointStates(ctx, probe, st)
		if err != nil || len(states) != 2 {
			t.Fatalf("empty checkpoint states=%v err=%v", states, err)
		}
		for _, state := range states {
			if state.LatestCheckpoint != c.Cursors[state.ChainID].Checkpoint || state.SafeCheckpoint != batch.Baselines[state.ChainID].Checkpoint || state.FinalizedCheckpoint != state.SafeCheckpoint {
				t.Fatalf("empty block did not advance public latest alone: %+v", state)
			}
		}
		managedTestClose(t, s)
		liveBatchPGCounts(t, probe, st, 3, 5, 2, 0)
		opts.OpenID = "unsupported-nonempty-reopen"
		if replacement, err := open(opts); !errors.Is(err, ErrManagedLivePendingActions) {
			managedTestClose(t, replacement)
			t.Fatalf("nonempty recovery unexpectedly adopted: %v", err)
		}
		liveBatchPGCounts(t, probe, st, 3, 5, 2, 0)
	})
	t.Run("shared_rows_ordered_journal_and_exact_older_retry", func(t *testing.T) {
		o, opts, open := liveBatchPGFixture(t)
		s, err := open(opts)
		if err != nil {
			t.Fatal(err)
		}
		m, st := s.ManagedLive(), s.BusinessStore()
		batch := liveSegmentsUnitBatch(m)
		acquired := liveBatchPGRetain(t, m, batch)
		calls := 0
		var saved *Writer
		result, err := m.ApplyBatch(ctx, batch, acquired, liveBatchPGSharedHandler(t, &calls, &saved))
		if err != nil || !result.Applied || calls != 3 || len(result.Segments) != 3 {
			t.Fatalf("shared apply=%+v calls=%d err=%v", result, calls, err)
		}
		if err := saved.Write(ctx, "account", Row{"id": "late", "balance": "99"}, Insert); err == nil {
			t.Fatal("escaped Writer remained usable")
		}
		probe := liveBatchPGProbe(t, o)
		liveBatchPGCounts(t, probe, st, 3, 5, 2, 2)
		liveBatchPGBalance(t, probe, st, "20")
		rows, err := probe.Query(ctx, "SELECT operation_id::text AS id,chain_id::text AS chain,checkpoint,before_row->>'balance' AS before FROM "+st.internal("_ponder_go_journal")+" ORDER BY operation_id")
		if err != nil || len(rows.Rows) != 2 || rows.Rows[0]["chain"] != "10" || rows.Rows[1]["chain"] != "1" || rows.Rows[0]["before"] != nil || rows.Rows[1]["before"] != "10" || rows.Rows[0]["checkpoint"] != batch.Blocks[0].Head.Checkpoint || rows.Rows[1]["checkpoint"] != batch.Blocks[1].Head.Checkpoint {
			t.Fatalf("shared-row journal ordering/before images=%v err=%v", rows.Rows, err)
		}
		if result.Segments[0].Journal.Count != 1 || result.Segments[1].Journal.Count != 1 || result.Segments[2].Journal.Count != 0 || result.Segments[2].Journal.Before != result.Segments[1].Journal.After || result.Segments[2].Journal.After != result.Segments[1].Journal.After {
			t.Fatalf("per-block journal partition=%+v", result.Segments)
		}
		next := ManagedLiveBatch{ID: "batch-b", Generation: 1, ExpectedLastOperation: batch.ID, ExpectedTail: 3, Blocks: []ManagedLiveBlock{liveSegmentsUnitBlock(1, 6, batch.Blocks[1].Head.Hash)}}
		if _, err := m.ApplyBatch(ctx, next, liveBatchPGRetain(t, m, next), func(w *Writer, _ ManagedLiveBlock) error {
			return w.Write(ctx, "account", Row{"id": "shared", "balance": "30"}, Update)
		}); err != nil {
			t.Fatal(err)
		}
		before, err := m.snapshotHash(ctx, probe, next.ID)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := m.ApplyBatch(ctx, batch, acquired, func(*Writer, ManagedLiveBlock) error { t.Fatal("exact older retry reran handler"); return nil })
		if err != nil || retry.Applied || managedHash(retry.Segments) != managedHash(result.Segments) {
			t.Fatalf("exact older retry=%+v err=%v", retry, err)
		}
		changed := batch
		changed.Blocks = append([]ManagedLiveBlock(nil), batch.Blocks...)
		changed.Blocks[0].InputDigest = managedHash("different extracted payload")
		if _, err := m.ApplyBatch(ctx, changed, liveBatchPGRetain(t, m, changed), func(*Writer, ManagedLiveBlock) error { t.Fatal("changed payload ran handler"); return nil }); !errors.Is(err, ErrManagedLiveBatchConflict) {
			t.Fatalf("same op ID accepted different payload: %v", err)
		}
		after, err := m.snapshotHash(ctx, probe, next.ID)
		if err != nil || before != after {
			t.Fatalf("retry/rejection mutated durable snapshot: %s %s %v", before, after, err)
		}
		liveBatchPGCounts(t, probe, st, 4, 6, 3, 3)
		liveBatchPGBalance(t, probe, st, "30")
	})
	t.Run("handler_failure_rolls_back_all_blocks_and_capture_DDL", func(t *testing.T) {
		o, opts, open := liveBatchPGFixture(t)
		s, err := open(opts)
		if err != nil {
			t.Fatal(err)
		}
		m, st := s.ManagedLive(), s.BusinessStore()
		probe := liveBatchPGProbe(t, o)
		before, err := m.snapshotHash(ctx, probe, opts.OpenID)
		if err != nil {
			t.Fatal(err)
		}
		batch := liveSegmentsUnitBatch(m)
		acquired := liveBatchPGRetain(t, m, batch)
		sentinel := errors.New("fail second block after earlier business/journal/proof/cursor/segment writes")
		calls := 0
		_, err = m.ApplyBatch(ctx, batch, acquired, func(w *Writer, _ ManagedLiveBlock) error {
			calls++
			if err := w.Write(ctx, "account", Row{"id": "shared", "balance": fmt.Sprint(calls)}, Upsert); err != nil {
				return err
			}
			if calls == 2 {
				return sentinel
			}
			return nil
		})
		if !errors.Is(err, sentinel) || calls != 2 {
			t.Fatalf("handler rollback calls=%d err=%v", calls, err)
		}
		after, err := m.snapshotHash(ctx, probe, opts.OpenID)
		if err != nil || before != after {
			t.Fatalf("failed batch changed full bootstrap snapshot: %s %s %v", before, after, err)
		}
		liveBatchPGCounts(t, probe, st, 0, 0, 1, 0)
		liveBatchPGBalance(t, probe, st, "missing")
		calls = 0
		var saved *Writer
		if _, err := m.ApplyBatch(ctx, batch, acquired, liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
			t.Fatalf("same retained batch after known rollback: %v", err)
		}
		next := ManagedLiveBatch{ID: "ignored-error", Generation: 1, ExpectedLastOperation: batch.ID, ExpectedTail: 3, Blocks: []ManagedLiveBlock{liveSegmentsUnitBlock(1, 6, batch.Blocks[1].Head.Hash)}}
		if _, err := m.ApplyBatch(ctx, next, liveBatchPGRetain(t, m, next), func(w *Writer, _ ManagedLiveBlock) error {
			_ = w.Write(ctx, "account", Row{"id": "shared", "balance": "99"}, Insert)
			return nil
		}); err == nil {
			t.Fatal("ignored Writer error committed a segment")
		}
		liveBatchPGCounts(t, probe, st, 3, 5, 2, 2)
		liveBatchPGBalance(t, probe, st, "20")
	})
	t.Run("retained_batch_cannot_cross_actual_owner_generation", func(t *testing.T) {
		o, opts, open := liveBatchPGFixture(t)
		first, err := open(opts)
		if err != nil {
			t.Fatal(err)
		}
		batch := liveSegmentsUnitBatch(first.ManagedLive())
		old := liveBatchPGRetain(t, first.ManagedLive(), batch)
		managedTestClose(t, first)
		opts.OpenID = "open-generation-two"
		second, err := open(opts)
		if err != nil {
			t.Fatal(err)
		}
		m := second.ManagedLive()
		if c, err := m.Control(ctx); err != nil || c.Generation != 2 {
			t.Fatalf("actual reopened generation=%+v err=%v", c, err)
		}
		if _, err := m.ApplyBatch(ctx, batch, old, func(*Writer, ManagedLiveBlock) error { t.Fatal("old owner acquisition ran handler"); return nil }); !errors.Is(err, ErrManagedLiveBatchConflict) {
			t.Fatalf("foreign generation acquisition accepted: %v", err)
		}
		if _, err := m.RetainBatch(ctx, batch); !errors.Is(err, ErrManagedLiveBatchConflict) {
			t.Fatalf("old generation request retained by new owner: %v", err)
		}
		batch.Generation, batch.ExpectedLastOperation = 2, opts.OpenID
		if _, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), nil); err != nil {
			t.Fatal(err)
		}
		liveBatchPGCounts(t, liveBatchPGProbe(t, o), second.BusinessStore(), 3, 5, 3, 0)
	})
	t.Run("catalog_tamper_rejects_new_batch_and_old_retry", func(t *testing.T) {
		o, opts, open := liveBatchPGFixture(t)
		s, err := open(opts)
		if err != nil {
			t.Fatal(err)
		}
		m, st := s.ManagedLive(), s.BusinessStore()
		batch := liveSegmentsUnitBatch(m)
		acquired := liveBatchPGRetain(t, m, batch)
		calls := 0
		var saved *Writer
		if _, err := m.ApplyBatch(ctx, batch, acquired, liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
			t.Fatal(err)
		}
		next := ManagedLiveBatch{ID: "after-tamper", Generation: 1, ExpectedLastOperation: batch.ID, ExpectedTail: 3, Blocks: []ManagedLiveBlock{liveSegmentsUnitBlock(1, 6, batch.Blocks[1].Head.Hash)}}
		nextAcquired := liveBatchPGRetain(t, m, next)
		probe := liveBatchPGProbe(t, o)
		if err := probe.Exec(ctx, "ALTER TABLE "+st.table("account")+" ADD COLUMN unreceipted text"); err != nil {
			t.Fatal(err)
		}
		for _, item := range []struct {
			batch ManagedLiveBatch
			acq   *ManagedLiveAcquisition
		}{{batch, acquired}, {next, nextAcquired}} {
			if _, err := m.ApplyBatch(ctx, item.batch, item.acq, func(*Writer, ManagedLiveBlock) error { t.Fatal("catalog tamper reached handler"); return nil }); err == nil {
				t.Fatal("catalog tamper accepted as current state")
			}
		}
		if _, err := m.Control(ctx); err == nil {
			t.Fatal("catalog tamper passed read-only Control")
		}
		liveBatchPGCounts(t, probe, st, 3, 5, 2, 2)
		liveBatchPGBalance(t, probe, st, "20")
	})
	for _, mode := range []string{"before_commit", "after_commit", "readback_failure", "after_commit_receipt_change", "after_commit_session_lock_loss"} {
		t.Run("commit/"+mode, func(t *testing.T) {
			o, opts, _ := liveBatchPGFixture(t)
			normalized, err := normalizeManagedLiveOptions(o, opts)
			if err != nil {
				t.Fatal(err)
			}
			var concrete *independentPGBackend
			var fault *liveBatchPGFaultBackend
			base := managedStorageOptions{ManagedFiniteOptions: normalized.ManagedFiniteOptions, live: &normalized}
			base.transactionBackend = func(backend Backend) Backend {
				var ok bool
				concrete, ok = backend.(*independentPGBackend)
				if !ok {
					t.Fatal("lost concrete production PG owner backend")
				}
				fault = &liveBatchPGFaultBackend{Backend: backend, mode: mode}
				return fault
			}
			s, err := openIndependentPostgres(ctx, o, &base)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { managedTestFaultClose(t, s) })
			m, st := s.ManagedLive(), s.BusinessStore()
			if s.businessBackend != concrete || m.owner.control != concrete.pgBackend || m.owner.mode != "postgres-session-lock-and-heartbeat" || len(m.owner.owners) != 9 || fault.fired {
				t.Fatal("fault seam replaced actual owner/control lanes or targeted bootstrap")
			}
			probe := liveBatchPGProbe(t, o)
			before, err := m.snapshotHash(ctx, probe, opts.OpenID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "after_commit_receipt_change" {
				fault.after = func() error {
					return probe.Exec(ctx, "UPDATE "+st.internal("_ponder_go_live_operations")+" SET after_hash='corrupt' WHERE kind='batch'")
				}
			}
			if mode == "after_commit_session_lock_loss" {
				fault.after = func() error {
					var unlocked bool
					err := concrete.connection.QueryRowContext(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", o.BusinessNamespace+":independent-owner").Scan(&unlocked)
					if err == nil && !unlocked {
						return errors.New("did not release this fixture's actual owned session lock")
					}
					return err
				}
			}
			batch := liveSegmentsUnitBatch(m)
			acquired := liveBatchPGRetain(t, m, batch)
			calls := 0
			var saved *Writer
			result, err := m.ApplyBatch(ctx, batch, acquired, liveBatchPGSharedHandler(t, &calls, &saved))
			if !fault.fired || calls != 3 {
				t.Fatalf("batch COMMIT fault not reached mode=%s calls=%d err=%v", mode, calls, err)
			}
			if mode == "after_commit" {
				if err != nil || !result.Applied || m.uncertain.Load() {
					t.Fatalf("exact committed batch not reconciled: %+v %v", result, err)
				}
				if retry, err := m.ApplyBatch(ctx, batch, acquired, func(*Writer, ManagedLiveBlock) error { t.Fatal("reconciled retry reran handler"); return nil }); err != nil || retry.Applied {
					t.Fatalf("reconciled retry=%+v %v", retry, err)
				}
				if _, err := m.Control(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				var stage *ManagedStageError
				if !errors.As(err, &stage) || stage.Outcome != "unknown" || !m.uncertain.Load() || result.Applied {
					t.Fatalf("uncertain COMMIT fabricated certainty: %+v %v", result, err)
				}
				if mode == "after_commit_session_lock_loss" && !errors.Is(err, ErrLeaseLost) {
					t.Fatalf("actual owned PG lock loss not fenced: %v", err)
				}
				wantFence := ErrManagedLiveUncertain
				if mode == "after_commit_session_lock_loss" {
					// Actual owner loss has precedence over the uncertainty fence.
					wantFence = ErrLeaseLost
				}
				if _, err := m.ApplyBatch(ctx, batch, acquired, func(*Writer, ManagedLiveBlock) error { t.Fatal("uncertain owner reran handler"); return nil }); !errors.Is(err, wantFence) {
					t.Fatalf("uncertain owner remained writable: %v", err)
				}
			}
			if mode == "before_commit" {
				liveBatchPGCounts(t, probe, st, 0, 0, 1, 0)
				liveBatchPGBalance(t, probe, st, "missing")
				after, err := m.snapshotHash(ctx, probe, opts.OpenID)
				if err != nil || before != after {
					t.Fatalf("pre-COMMIT failure leaked durable changes: %s %s %v", before, after, err)
				}
			} else {
				liveBatchPGCounts(t, probe, st, 3, 5, 2, 2)
				liveBatchPGBalance(t, probe, st, "20")
				r, err := probe.Query(ctx, "SELECT generation::text AS generation,owner_token,request_hash,payload::text AS payload FROM "+st.internal("_ponder_go_live_operations")+" WHERE id=$1 AND kind='batch'", batch.ID)
				var evidence managedLiveBatchEvidence
				if err != nil || len(r.Rows) != 1 || r.Rows[0]["generation"] != "1" || r.Rows[0]["owner_token"] != st.leaseToken || r.Rows[0]["request_hash"] != acquired.requestHash || json.Unmarshal([]byte(fmt.Sprint(r.Rows[0]["payload"])), &evidence) != nil || len(evidence.Segments) != 3 {
					t.Fatalf("real durable receipt lost batch/owner/generation/evidence: %v %v", r.Rows, err)
				}
			}
		})
	}
}

// Decorates only real Store transactions after constructor owner capture.
// The SQL transaction, fixed owner sessions, control pool and COMMIT are real.
type liveBatchPGFaultBackend struct {
	Backend
	mode                string
	fired, failReadback bool
	after               func() error
}

type liveBatchPGFaultTx struct {
	Transaction
	b      *liveBatchPGFaultBackend
	target bool
}

func (b *liveBatchPGFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	if b.failReadback {
		b.failReadback = false
		return nil, errors.New("injected batch readback unavailable")
	}
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &liveBatchPGFaultTx{Transaction: tx, b: b}, nil
}

func (tx *liveBatchPGFaultTx) Exec(ctx context.Context, query string, args ...any) error {
	if strings.HasPrefix(query, "INSERT INTO ") && strings.Contains(query, "_ponder_go_live_operations") && strings.Contains(query, "'batch'") {
		tx.target = true
	}
	return tx.Transaction.Exec(ctx, query, args...)
}

func (tx *liveBatchPGFaultTx) Commit(ctx context.Context) error {
	if !tx.target || tx.b.fired {
		return tx.Transaction.Commit(ctx)
	}
	tx.b.fired = true
	if tx.b.mode == "before_commit" {
		if err := tx.Transaction.Rollback(ctx); err != nil {
			return err
		}
		return errors.New("injected batch before COMMIT")
	}
	if err := tx.Transaction.Commit(ctx); err != nil {
		return err
	}
	if tx.b.after != nil {
		if err := tx.b.after(); err != nil {
			return err
		}
	}
	if tx.b.mode == "readback_failure" {
		tx.b.failReadback = true
	}
	return errors.New("injected lost batch COMMIT acknowledgement")
}
