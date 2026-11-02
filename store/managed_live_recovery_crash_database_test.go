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
)

func TestManagedLiveRecoveryCommitReadback(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, mode := range []string{"before_commit", "after_commit", "readback_failure"} {
				t.Run(mode, func(t *testing.T) {
					ctx := context.Background()
					o, opts, open := liveRecoveryFixture(t, dialect)
					s, err := open(opts)
					if err != nil {
						t.Fatal(err)
					}
					m := s.ManagedLive()
					batch := liveSegmentsUnitBatch(m)
					calls := 0
					var saved *Writer
					if _, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
						t.Fatal(err)
					}
					managedTestClose(t, s)
					opts.OpenID = "recover-ack-" + mode
					normalized, err := normalizeManagedLiveOptions(o, opts)
					if err != nil {
						t.Fatal(err)
					}
					normalized.recovery = true
					base := managedStorageOptions{ManagedFiniteOptions: normalized.ManagedFiniteOptions, live: &normalized}
					var fault *liveOpenFaultBackend
					base.transactionBackend = func(b Backend) Backend { fault = &liveOpenFaultBackend{Backend: b, mode: mode}; return fault }
					if dialect == "postgres" {
						s, err = openIndependentPostgres(ctx, o, &base)
					} else {
						s, err = openIndependentPGlite(ctx, o, &base)
					}
					if fault == nil || !fault.fired {
						managedTestClose(t, s)
						t.Fatalf("recovery COMMIT fault not reached: %v", err)
					}
					if mode == "after_commit" {
						if err != nil {
							t.Fatal(err)
						}
						c, e := s.ManagedLive().Control(ctx)
						if e != nil || c.Generation != 2 || c.Tail != 0 || c.Next != 4 {
							t.Fatalf("exact recovery not reconciled: %+v %v", c, e)
						}
						liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "missing")
						managedTestClose(t, s)
					} else {
						var stage *ManagedStageError
						if s != nil || !errors.As(err, &stage) || stage.Outcome != "unknown" {
							managedTestClose(t, s)
							t.Fatalf("unconfirmed recovery fabricated certainty: %v", err)
						}
					}
					opts.OpenID = "after-ack-" + mode
					s, err = open(opts)
					if err != nil {
						t.Fatal(err)
					}
					want := uint64(3)
					if mode == "before_commit" {
						want = 2
					}
					c, err := s.ManagedLive().Control(ctx)
					if err != nil || c.Generation != want || c.Next != 4 || c.Tail != 0 || c.Batches != 1 {
						t.Fatalf("ack restart=%+v %v", c, err)
					}
					liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "missing")
				})
			}
		})
	}
}

type liveRecoveryCrashBackend struct {
	Backend
	mode string
	st   *Store
	t    *testing.T
	keys []string
}
type liveRecoveryCrashTx struct {
	Transaction
	b      *liveRecoveryCrashBackend
	target bool
}

func (b *liveRecoveryCrashBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &liveRecoveryCrashTx{Transaction: tx, b: b}, nil
}
func (tx *liveRecoveryCrashTx) Exec(ctx context.Context, q string, args ...any) error {
	if strings.HasPrefix(q, "INSERT INTO ") && strings.Contains(q, "_ponder_go_live_operations") && strings.Contains(q, "'recover'") {
		tx.target = true
	}
	return tx.Transaction.Exec(ctx, q, args...)
}
func (tx *liveRecoveryCrashTx) Commit(ctx context.Context) error {
	if !tx.target {
		return tx.Transaction.Commit(ctx)
	}
	liveBatchPGCounts(tx.b.t, tx.Transaction, tx.b.st, 0, 5, 3, 0)
	liveBatchPGBalance(tx.b.t, tx.Transaction, tx.b.st, "missing")
	identities := []managedOwnerIdentity{}
	for _, key := range tx.b.keys {
		r, err := tx.Query(ctx, `SELECT a.pid::text AS pid,a.backend_start::text AS started FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND l.granted AND l.objsubid=1 AND l.classid=((hashtextextended($1,0)>>32)&4294967295)::oid AND l.objid=(hashtextextended($1,0)&4294967295)::oid AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database())`, key)
		if err != nil || len(r.Rows) != 1 {
			tx.b.t.Fatalf("owned lock identity %s: %v %v", key, r.Rows, err)
		}
		pid, err := liveInt(r.Rows[0], "pid")
		if err != nil {
			tx.b.t.Fatal(err)
		}
		identities = append(identities, managedOwnerIdentity{Key: key, PID: int(pid), Started: fmt.Sprint(r.Rows[0]["started"])})
	}
	owners, _ := json.Marshal(identities)
	fmt.Printf("RECOVERY_CRASH_OWNERS %s\n", owners)
	if tx.b.mode == "before_commit" {
		fmt.Println("RECOVERY_CRASH_BOUNDARY fully_staged_before_commit")
		os.Exit(101)
	}
	if err := tx.Transaction.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("RECOVERY_CRASH_BOUNDARY acknowledged_commit_before_return")
	os.Exit(102)
	return nil
}

func TestManagedLiveRecoveryPostgresCrash(t *testing.T) {
	ctx := context.Background()
	if mode := os.Getenv("PONDER_M45_RECOVERY_CRASH_CASE"); mode != "" {
		var spec liveCrashSpec
		if err := json.Unmarshal([]byte(os.Getenv("PONDER_M45_RECOVERY_CRASH_SPEC")), &spec); err != nil {
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
		s, err := OpenManagedLiveRecoveryIndependentPostgres(ctx, o, opts)
		if err != nil {
			t.Fatal(err)
		}
		m := s.ManagedLive()
		batch := liveSegmentsUnitBatch(m)
		calls := 0
		var saved *Writer
		if _, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
			t.Fatal(err)
		}
		public, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(public)
		fmt.Printf("RECOVERY_CRASH_PUBLIC %s\n", raw)
		managedTestClose(t, s)
		opts.OpenID = "crashed-recovery"
		normalized, err := normalizeManagedLiveOptions(o, opts)
		if err != nil {
			t.Fatal(err)
		}
		normalized.recovery = true
		base := managedStorageOptions{ManagedFiniteOptions: normalized.ManagedFiniteOptions, live: &normalized}
		base.transactionBackend = func(b Backend) Backend {
			st, err := describeStore(b, o.Schema, o.BusinessNamespace, o.Ordering, true)
			if err != nil {
				t.Fatal(err)
			}
			keys := []string{}
			for _, domain := range []string{o.BusinessNamespace, o.SyncNamespace} {
				for _, namespace := range independentNamespaceFamily(domain) {
					keys = append(keys, namespace+":independent-owner")
				}
			}
			keys = append(keys, opts.ViewsSchema+":independent-owner")
			return &liveRecoveryCrashBackend{Backend: b, mode: mode, t: t, st: st, keys: keys}
		}
		if _, err := openIndependentPostgres(ctx, o, &base); err != nil {
			t.Fatal(err)
		}
		t.Fatal("recovery crash boundary not reached")
		return
	}
	for _, mode := range []string{"before_commit", "after_commit"} {
		t.Run(mode, func(t *testing.T) {
			o, opts, open := liveRecoveryFixture(t, "postgres")
			spec, _ := json.Marshal(liveCrashSpec{o.BusinessNamespace, o.SyncNamespace, o.BusinessDirectory, o.SyncDirectory, o.PostgresURL, opts.OpenID})
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-test.run=^TestManagedLiveRecoveryPostgresCrash$", "-test.v", "-test.timeout=120s")
			cmd.Env = append(os.Environ(), "PONDER_M45_RECOVERY_CRASH_CASE="+mode, "PONDER_M45_RECOVERY_CRASH_SPEC="+string(spec))
			trace, err := cmd.CombinedOutput()
			t.Logf("actual recovery crash child:\n%s", trace)
			var exited *exec.ExitError
			want := 101
			if mode == "after_commit" {
				want = 102
			}
			if !errors.As(err, &exited) || exited.ExitCode() != want || !strings.Contains(string(trace), "RECOVERY_CRASH_BOUNDARY ") {
				t.Fatalf("wrong recovery crash outcome: %v", err)
			}
			probe := liveBatchPGProbe(t, o)
			st, err := describeStore(probe, o.Schema, o.BusinessNamespace, o.Ordering, true)
			if err != nil {
				t.Fatal(err)
			}
			var owners []managedOwnerIdentity
			var public []CheckpointState
			for _, line := range strings.Split(string(trace), "\n") {
				if strings.HasPrefix(line, "RECOVERY_CRASH_OWNERS ") {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "RECOVERY_CRASH_OWNERS ")), &owners); err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasPrefix(line, "RECOVERY_CRASH_PUBLIC ") {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "RECOVERY_CRASH_PUBLIC ")), &public); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(owners) != 9 || len(public) != 2 {
				t.Fatal("missing exact child ownership/public evidence")
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				left := 0
				for _, owner := range owners {
					r, err := probe.Query(ctx, "SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND backend_start::text=$2", owner.PID, owner.Started)
					if err != nil {
						t.Fatal(err)
					}
					left += len(r.Rows)
				}
				if left == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("owned child sessions not reaped: %d", left)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode == "before_commit" {
				liveBatchPGCounts(t, probe, st, 3, 5, 2, 2)
				liveBatchPGBalance(t, probe, st, "20")
			} else {
				liveBatchPGCounts(t, probe, st, 0, 5, 3, 0)
				liveBatchPGBalance(t, probe, st, "missing")
			}
			actual, err := checkpointStates(ctx, probe, st)
			if err != nil || managedHash(actual) != managedHash(public) {
				t.Fatalf("crash changed public projection: %v %v", actual, err)
			}
			opts.OpenID = "restart-recovery-" + mode
			s, err := open(opts)
			if err != nil {
				t.Fatal(err)
			}
			m := s.ManagedLive()
			c, err := m.Control(ctx)
			generation := uint64(2)
			if mode == "after_commit" {
				generation = 3
			}
			if err != nil || c.Generation != generation || c.Next != 4 || c.Tail != 0 || len(c.ReplayTargets) != 2 {
				t.Fatalf("crash restart=%+v %v", c, err)
			}
			batch := liveSegmentsUnitBatch(m)
			batch.ID = "replay-after-real-crash"
			batch.Generation = c.Generation
			batch.ExpectedLastOperation = c.LastOperation
			batch.ExpectedTail = c.Tail
			batch.Baselines = nil
			calls := 0
			var saved *Writer
			result, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), liveBatchPGSharedHandler(t, &calls, &saved))
			if err != nil || calls != 3 || len(result.Segments) != 3 || result.Segments[0].Ordinal != 4 || result.Segments[2].Journal.Count != 0 {
				t.Fatalf("crash replay=%+v calls=%d %v", result, calls, err)
			}
			liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "20")
		})
	}
}
