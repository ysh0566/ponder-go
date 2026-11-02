package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
)

// These are native real-SQL controls, not original-test mappings. PGlite's
// native SQL transaction guarantee is stronger than the source callback API.
func TestManagedLiveDatabaseClosure(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			t.Run("global_suffix_replay_finalize_and_empty_reorg", func(t *testing.T) {
				ctx := context.Background()
				_, opts, open := liveDBFixture(t, dialect)
				opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
				s, err := open(opts)
				if err != nil {
					t.Fatal(err)
				}
				m, st, q := s.ManagedLive(), s.BusinessStore(), s.businessBackend
				batch := liveSegmentsUnitBatch(m)
				acquired := liveBatchPGRetain(t, m, batch)
				calls := 0
				var saved *Writer
				first, err := m.ApplyBatch(ctx, batch, acquired, liveBatchPGSharedHandler(t, &calls, &saved))
				if err != nil || calls != 3 || len(first.Segments) != 3 || first.Segments[2].Journal.Count != 0 {
					t.Fatalf("batch=%+v calls=%d err=%v", first, calls, err)
				}
				liveBatchPGCounts(t, q, st, 3, 5, 2, 2)
				liveBatchPGBalance(t, q, st, "20")
				public, err := checkpointStates(ctx, q, st)
				if err != nil {
					t.Fatal(err)
				}
				control, err := m.Control(ctx)
				if err != nil {
					t.Fatal(err)
				}
				noop := ManagedLiveReorg{ID: "no-affected-active", Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail, Ancestor: control.Cursors[1]}
				na, err := m.RetainReorg(ctx, noop)
				if err != nil {
					t.Fatal(err)
				}
				noresult, err := m.ApplyReorg(ctx, noop, na)
				if err != nil || !noresult.Applied || len(noresult.Removed) != 0 || len(noresult.Replay) != 0 || noresult.SyncRewind != noop.Ancestor {
					t.Fatalf("empty business rewind=%+v %v", noresult, err)
				}
				liveBatchPGCounts(t, q, st, 3, 5, 3, 2)
				control, err = m.Control(ctx)
				if err != nil {
					t.Fatal(err)
				}
				r := ManagedLiveReorg{ID: "rewind-a", Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail, Ancestor: batch.Baselines[1]}
				ra, err := m.RetainReorg(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				liveClosureRollback(t, m, func() error { _, err := m.ApplyReorg(ctx, r, ra); return err })
				result, err := m.ApplyReorg(ctx, r, ra)
				if err != nil || !result.Applied || len(result.Removed) != 2 || len(result.Replay) != 1 || result.Replay[0] != batch.Blocks[2] {
					t.Fatalf("global undo=%+v %v", result, err)
				}
				liveBatchPGCounts(t, q, st, 1, 4, 4, 1)
				liveBatchPGBalance(t, q, st, "10")
				afterPublic, err := checkpointStates(ctx, q, st)
				if err != nil || managedHash(public) != managedHash(afterPublic) {
					t.Fatalf("undo changed public heads: %v %v %v", public, afterPublic, err)
				}
				control, err = m.Control(ctx)
				if err != nil || control.Next != 4 || control.Tail != 1 || control.Cursors[1] != batch.Baselines[1] || control.Cursors[10] != batch.Blocks[0].Head {
					t.Fatalf("undo private projection=%+v %v", control, err)
				}
				replacement := batch.Blocks[1]
				replacement.Head.Hash = fmt.Sprintf("0x%064x", 55555)
				replacement.InputDigest = managedHash("new-a5-branch")
				replay := ManagedLiveBatch{ID: "replay-new-branch", Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail, Blocks: []ManagedLiveBlock{replacement, batch.Blocks[2]}}
				applied, err := m.ApplyBatch(ctx, replay, liveBatchPGRetain(t, m, replay), func(w *Writer, b ManagedLiveBlock) error {
					cp, _ := checkpoint.Decode(b.Head.Checkpoint)
					if cp.ChainID != 1 {
						return nil
					}
					row, err := w.Find(ctx, "account", Row{"id": "shared"})
					if err != nil || fmt.Sprint(row["balance"]) != "10" {
						return fmt.Errorf("replay did not see global undo: %v %v", row, err)
					}
					return w.Write(ctx, "account", Row{"id": "shared", "balance": "30"}, Update)
				})
				if err != nil || len(applied.Segments) != 2 || applied.Segments[0].Ordinal != 4 || applied.Segments[1].Ordinal != 5 || applied.Segments[1].Journal.Count != 0 {
					t.Fatalf("replay reused ordinals or lost empty proof: %+v %v", applied, err)
				}
				liveBatchPGCounts(t, q, st, 3, 5, 5, 2)
				liveBatchPGBalance(t, q, st, "30")
				control, err = m.Control(ctx)
				if err != nil {
					t.Fatal(err)
				}
				f := ManagedLiveFinalize{ID: "finalize-replay", Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail, ExpectedSafeOrdinal: control.SafeOrdinal, Finalized: cloneLiveHeads(control.Cursors)}
				fa, err := m.RetainFinalize(ctx, f)
				if err != nil {
					t.Fatal(err)
				}
				liveClosureRollback(t, m, func() error { _, err := m.ApplyFinalize(ctx, f, fa); return err })
				finalized, err := m.ApplyFinalize(ctx, f, fa)
				if err != nil || !finalized.Applied || len(finalized.Pruned) != 3 || finalized.SafeOrdinal != 5 {
					t.Fatalf("finalize=%+v %v", finalized, err)
				}
				liveBatchPGCounts(t, q, st, 0, 5, 6, 0)
				liveBatchPGBalance(t, q, st, "30")
				before, err := m.snapshotHash(ctx, q, f.ID)
				if err != nil {
					t.Fatal(err)
				}
				if retry, err := m.ApplyBatch(ctx, batch, acquired, func(*Writer, ManagedLiveBlock) error { t.Fatal("old exact receipt reran business handler"); return nil }); err != nil || retry.Applied {
					t.Fatalf("old receipt retry=%+v %v", retry, err)
				}
				if retry, err := m.ApplyFinalize(ctx, f, fa); err != nil || retry.Applied {
					t.Fatalf("finalize retry=%+v %v", retry, err)
				}
				after, err := m.snapshotHash(ctx, q, f.ID)
				if err != nil || before != after {
					t.Fatalf("retries changed complete snapshot: %s %s %v", before, after, err)
				}
				control, err = m.Control(ctx)
				if err != nil {
					t.Fatal(err)
				}
				noop = ManagedLiveReorg{ID: "no-affected-safe", Generation: control.Generation, ExpectedLastOperation: control.LastOperation, ExpectedTail: control.Tail, Ancestor: control.Cursors[1]}
				na, err = m.RetainReorg(ctx, noop)
				if err != nil {
					t.Fatal(err)
				}
				if result, err := m.ApplyReorg(ctx, noop, na); err != nil || !result.Applied || len(result.Removed) != 0 {
					t.Fatalf("safe empty rewind=%+v %v", result, err)
				}
				liveBatchPGCounts(t, q, st, 0, 5, 7, 0)
				if ready, err := m.Ready(ctx); err != nil || ready {
					t.Fatalf("database closure invented runtime readiness: %v %v", ready, err)
				}
			})
			for _, mode := range []string{"before_commit", "after_commit", "readback_failure"} {
				t.Run("batch_ack/"+mode, func(t *testing.T) {
					ctx := context.Background()
					_, opts, open := liveDBFixture(t, dialect)
					opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
					s, err := open(opts)
					if err != nil {
						t.Fatal(err)
					}
					m, st, q := s.ManagedLive(), s.BusinessStore(), s.businessBackend
					fault := &liveBatchPGFaultBackend{Backend: st.Backend, mode: mode}
					st.Backend = fault
					batch := liveSegmentsUnitBatch(m)
					acq := liveBatchPGRetain(t, m, batch)
					calls := 0
					var saved *Writer
					result, err := m.ApplyBatch(ctx, batch, acq, liveBatchPGSharedHandler(t, &calls, &saved))
					if !fault.fired || calls != 3 {
						t.Fatalf("fault not reached: calls=%d err=%v", calls, err)
					}
					if mode == "after_commit" {
						if err != nil || !result.Applied || m.uncertain.Load() {
							t.Fatalf("exact committed receipt not reconciled: %+v %v", result, err)
						}
						if retry, err := m.ApplyBatch(ctx, batch, acq, func(*Writer, ManagedLiveBlock) error { t.Fatal("reconciled retry reran handler"); return nil }); err != nil || retry.Applied {
							t.Fatalf("retry=%+v %v", retry, err)
						}
					} else {
						var stage *ManagedStageError
						if !errors.As(err, &stage) || stage.Outcome != "unknown" || !m.uncertain.Load() || result.Applied {
							t.Fatalf("lost outcome fabricated certainty: %+v %v", result, err)
						}
						if _, err := m.ApplyBatch(ctx, batch, acq, nil); !errors.Is(err, ErrManagedLiveUncertain) {
							t.Fatalf("uncertain handle remained writable: %v", err)
						}
					}
					if mode == "before_commit" {
						liveBatchPGCounts(t, q, st, 0, 0, 1, 0)
						liveBatchPGBalance(t, q, st, "missing")
					} else {
						liveBatchPGCounts(t, q, st, 3, 5, 2, 2)
						liveBatchPGBalance(t, q, st, "20")
					}
				})
			}
		})
	}
}

type liveClosureFaultBackend struct {
	Backend
	fired   bool
	failure error
}
type liveClosureFaultTx struct {
	Transaction
	b *liveClosureFaultBackend
}

func (b *liveClosureFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &liveClosureFaultTx{tx, b}, nil
}
func (tx *liveClosureFaultTx) Exec(ctx context.Context, q string, args ...any) error {
	if err := tx.Transaction.Exec(ctx, q, args...); err != nil {
		return err
	}
	if !tx.b.fired && strings.HasPrefix(q, "DELETE FROM ") && strings.Contains(q, "_ponder_go_live_segments") {
		tx.b.fired = true
		return tx.b.failure
	}
	return nil
}
func liveClosureRollback(t *testing.T, m *ManagedLive, action func() error) {
	t.Helper()
	ctx := context.Background()
	st := m.owner.s
	control, err := m.Control(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.snapshotHash(ctx, st.Backend, control.LastOperation)
	if err != nil {
		t.Fatal(err)
	}
	original := st.Backend
	fault := &liveClosureFaultBackend{Backend: original, failure: errors.New("after actual segment DELETE")}
	func() { st.Backend = fault; defer func() { st.Backend = original }(); err = action() }()
	if !fault.fired || !errors.Is(err, fault.failure) {
		t.Fatalf("real SQL rollback boundary not reached: %v", err)
	}
	after, err := m.snapshotHash(ctx, original, control.LastOperation)
	if err != nil || before != after {
		t.Fatalf("rollback changed business/journal/proof/cursor/receipt snapshot: %s %s %v", before, after, err)
	}
}

type liveCrashSpec struct{ BusinessNamespace, SyncNamespace, BusinessDirectory, SyncDirectory, PostgresURL, OpenID string }
type liveCrashBackend struct {
	Backend
	mode string
	st   *Store
	t    *testing.T
}
type liveCrashTx struct {
	Transaction
	b      *liveCrashBackend
	target bool
}

func (b *liveCrashBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &liveCrashTx{Transaction: tx, b: b}, nil
}
func (tx *liveCrashTx) Exec(ctx context.Context, q string, args ...any) error {
	if strings.HasPrefix(q, "INSERT INTO ") && strings.Contains(q, "_ponder_go_live_operations") && strings.Contains(q, "'batch'") {
		tx.target = true
	}
	return tx.Transaction.Exec(ctx, q, args...)
}
func (tx *liveCrashTx) Commit(ctx context.Context) error {
	if !tx.target {
		return tx.Transaction.Commit(ctx)
	}
	liveBatchPGCounts(tx.b.t, tx.Transaction, tx.b.st, 3, 5, 2, 2)
	liveBatchPGBalance(tx.b.t, tx.Transaction, tx.b.st, "20")
	owners, _ := json.Marshal(tx.b.st.managedLive.owner.owners)
	fmt.Printf("CRASH_OWNERS %s\n", owners)
	if tx.b.mode == "before_commit" {
		fmt.Println("CRASH_BOUNDARY fully_staged_before_commit")
		os.Exit(91)
	}
	if err := tx.Transaction.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("CRASH_BOUNDARY acknowledged_commit_before_return")
	os.Exit(92)
	return nil
}

// This is an abrupt application process exit with PostgreSQL still running,
// not graceful owner Close or database-server power-loss recovery.
func TestManagedLivePostgresCrashBoundary(t *testing.T) {
	if mode := os.Getenv("PONDER_M45_CRASH_CASE"); mode != "" {
		var spec liveCrashSpec
		if err := json.Unmarshal([]byte(os.Getenv("PONDER_M45_CRASH_SPEC")), &spec); err != nil {
			t.Fatal(err)
		}
		o, opts := liveTestOptions()
		o.BusinessNamespace, o.SyncNamespace = spec.BusinessNamespace, spec.SyncNamespace
		o.BusinessDirectory, o.SyncDirectory = spec.BusinessDirectory, spec.SyncDirectory
		o.PostgresURL = spec.PostgresURL
		o.Schema = model(false)
		opts.ViewsSchema = o.BusinessNamespace + "_view"
		opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
		opts.OpenID = spec.OpenID
		s, err := OpenManagedLiveIndependentPostgres(context.Background(), o, opts)
		if err != nil {
			t.Fatal(err)
		}
		m, st := s.ManagedLive(), s.BusinessStore()
		st.Backend = &liveCrashBackend{Backend: st.Backend, mode: mode, st: st, t: t}
		batch := liveSegmentsUnitBatch(m)
		calls := 0
		var saved *Writer
		if _, err := m.ApplyBatch(context.Background(), batch, liveBatchPGRetain(t, m, batch), liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash boundary did not exit")
		return
	}
	for _, mode := range []string{"before_commit", "after_commit"} {
		t.Run(mode, func(t *testing.T) {
			o, opts, open := liveDBFixture(t, "postgres")
			opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
			spec, _ := json.Marshal(liveCrashSpec{o.BusinessNamespace, o.SyncNamespace, o.BusinessDirectory, o.SyncDirectory, o.PostgresURL, opts.OpenID})
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestManagedLivePostgresCrashBoundary$", "-test.v", "-test.timeout=90s")
			cmd.Env = append(os.Environ(), "PONDER_M45_CRASH_CASE="+mode, "PONDER_M45_CRASH_SPEC="+string(spec))
			trace, err := cmd.CombinedOutput()
			t.Logf("actual crashed child:\n%s", trace)
			var exit *exec.ExitError
			want := 91
			if mode == "after_commit" {
				want = 92
			}
			if !errors.As(err, &exit) || exit.ExitCode() != want || !strings.Contains(string(trace), "CRASH_BOUNDARY ") {
				t.Fatalf("wrong crash outcome: %v", err)
			}
			probe := liveBatchPGProbe(t, o)
			st, err := describeStore(probe, o.Schema, o.BusinessNamespace, o.Ordering, true)
			if err != nil {
				t.Fatal(err)
			}
			var owners []managedOwnerIdentity
			for _, line := range strings.Split(string(trace), "\n") {
				if strings.HasPrefix(line, "CRASH_OWNERS ") {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "CRASH_OWNERS ")), &owners); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(owners) != 9 {
				t.Fatalf("missing actual owned-session identities: %v", owners)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				remaining := 0
				for _, owner := range owners {
					r, err := probe.Query(context.Background(), "SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND backend_start::text=$2", owner.PID, owner.Started)
					if err != nil {
						t.Fatal(err)
					}
					remaining += len(r.Rows)
				}
				if remaining == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("crashed owner sessions not joined: %d", remaining)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode == "before_commit" {
				liveBatchPGCounts(t, probe, st, 0, 0, 1, 0)
				liveBatchPGBalance(t, probe, st, "missing")
				opts.OpenID = "after-real-uncommitted-crash"
				s, err := open(opts)
				if err != nil {
					t.Fatal(err)
				}
				if c, err := s.ManagedLive().Control(context.Background()); err != nil || c.Generation != 2 || c.Batches != 0 {
					t.Fatalf("empty crash reopen=%+v %v", c, err)
				}
			} else {
				liveBatchPGCounts(t, probe, st, 3, 5, 2, 2)
				liveBatchPGBalance(t, probe, st, "20")
				before := liveCrashSQLSnapshot(t, probe, st)
				opts.OpenID = "after-real-committed-crash"
				if s, err := open(opts); !errors.Is(err, ErrManagedLivePendingActions) {
					managedTestClose(t, s)
					t.Fatalf("unimplemented nonempty recovery was silently adopted: %v", err)
				}
				if after := liveCrashSQLSnapshot(t, probe, st); before != after {
					t.Fatal("rejected nonempty crash reopen mutated durable state")
				}
			}
		})
	}
}
func liveCrashSQLSnapshot(t *testing.T, q Querier, s *Store) string {
	t.Helper()
	parts := []any{}
	for _, name := range []string{"_ponder_go_live_control", "_ponder_go_live_operations", "_ponder_go_live_segments", "_ponder_go_live_proofs", "_ponder_go_journal", "_ponder_go_progress", "_ponder_go_finalized", "_ponder_go_runtime_cursor", "_ponder_go_app"} {
		r, err := q.Query(context.Background(), "SELECT COALESCE(jsonb_agg(to_jsonb(q) ORDER BY to_jsonb(q)::text),'[]'::jsonb)::text AS rows FROM "+s.internal(name)+" q")
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, r.Rows)
	}
	r, err := q.Query(context.Background(), "SELECT id,balance::text AS balance FROM "+s.table("account")+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	parts = append(parts, r.Rows)
	return managedHash(parts)
}
