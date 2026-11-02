package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

// These are supplemental native format controls, not upstream test mappings.
// Every case keeps its new task-owned data directory for audit.
func liveDBFixture(t *testing.T, dialect string) (IndependentStorageOptions, ManagedLiveOptions, func(ManagedLiveOptions) (*IndependentStorage, error)) {
	t.Helper()
	root := os.Getenv("PONDER_M45_TEST_ROOT")
	if root == "" {
		t.Fatal("requires retained task-owned PONDER_M45_TEST_ROOT")
	}
	dir, err := os.MkdirTemp(root, "live-"+dialect+"-")
	if err != nil {
		t.Fatal(err)
	}
	o, m := liveTestOptions()
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	o.BusinessNamespace, o.SyncNamespace = "ponder_go_ml_"+suffix, "ponder_go_mc_"+suffix
	o.BusinessDirectory, o.SyncDirectory = filepath.Join(dir, "business"), filepath.Join(dir, "sync")
	o.Schema, o.Ordering = model(false), schema.Multichain
	m.ViewsSchema = o.BusinessNamespace + "_view"
	if dialect == "postgres" {
		o.PostgresURL = os.Getenv("PONDER_TEST_POSTGRES")
		expected := os.Getenv("PONDER_M45_POSTGRES_DIRECTORY")
		if o.PostgresURL == "" || expected == "" || os.Getenv("PONDER_M45_POSTGRES_DATABASE") == "" {
			t.Fatal("requires explicitly verified fresh PostgreSQL fixture identity")
		}
		b, e := OpenPostgres(context.Background(), o.PostgresURL)
		if e != nil {
			t.Fatal(e)
		}
		r, e := b.Query(context.Background(), "SELECT current_setting('data_directory') AS directory,current_database() AS database")
		closeErr := b.Close()
		if e != nil || closeErr != nil || len(r.Rows) != 1 || r.Rows[0]["directory"] != expected || r.Rows[0]["database"] != os.Getenv("PONDER_M45_POSTGRES_DATABASE") {
			t.Fatalf("fresh fixture identity mismatch: %v %v %v", r.Rows, e, closeErr)
		}
	}
	owners := []*IndependentStorage{}
	t.Cleanup(func() {
		for _, owner := range owners {
			managedTestClose(t, owner)
		}
	})
	open := func(opts ManagedLiveOptions) (*IndependentStorage, error) {
		var s *IndependentStorage
		var e error
		if dialect == "postgres" {
			s, e = OpenManagedLiveIndependentPostgres(context.Background(), o, opts)
		} else {
			s, e = OpenManagedLiveIndependentPGlite(context.Background(), o, opts)
		}
		if e == nil {
			owners = append(owners, s)
		}
		return s, e
	}
	t.Logf("retained %s namespaces=%s,%s directory=%s", dialect, o.BusinessNamespace, o.SyncNamespace, dir)
	return o, m, open
}

func TestManagedLivePersistence(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			t.Run("numeric_generations_exact_receipts_and_closed_guards", func(t *testing.T) {
				ctx := context.Background()
				_, opts, open := liveDBFixture(t, dialect)
				for generation := uint64(1); generation <= 11; generation++ {
					opts.OpenID = fmt.Sprintf("open-%d", generation)
					s, err := open(opts)
					if err != nil {
						t.Fatalf("generation %d: %v", generation, err)
					}
					if s.ManagedFinite() != nil || s.ManagedLive() == nil {
						t.Fatal("live owner leaked the finite activation interface")
					}
					c, err := s.ManagedLive().Control(ctx)
					if err != nil || c.Generation != generation || c.LastOperation != opts.OpenID || c.Next != 1 || c.Tail != 0 || c.HistoryCuts[1] != 2 || c.HistoryCuts[10] != 4 {
						t.Fatalf("control=%+v err=%v", c, err)
					}
					c.FinalizedHeads[1] = ManagedLiveHead{}
					c.HistoryCuts[1] = 999
					if ready, err := s.ManagedLive().Ready(ctx); err != nil || ready {
						t.Fatalf("bootstrap readiness=%v %v", ready, err)
					}
					if err := s.BusinessActions().CrashRecovery(ctx); !errors.Is(err, ErrManagedLiveLegacyMutation) {
						t.Fatalf("legacy recovery bypass: %v", err)
					}
					managedTestClose(t, s)
					if _, err := s.ManagedLive().Control(ctx); !errors.Is(err, ErrManagedLiveClosed) {
						t.Fatalf("closed control=%v", err)
					}
					if err := s.BusinessActions().CreateTriggers(ctx); !errors.Is(err, ErrManagedLiveClosed) {
						t.Fatalf("closed legacy guard precedence=%v", err)
					}
				}
				if s, err := open(opts); !errors.Is(err, ErrManagedLiveOpenIDUsed) {
					if s != nil {
						managedTestClose(t, s)
					}
					t.Fatalf("reused open ID: %v", err)
				}
				h := opts.FinalizedHeads[1]
				h.Checkpoint = checkpoint.MustEncode(checkpoint.Block(101, 1, 5, true))
				opts.FinalizedHeads[1] = h
				if s, err := open(opts); !errors.Is(err, ErrManagedLiveReceiptConflict) {
					if s != nil {
						managedTestClose(t, s)
					}
					t.Fatalf("changed heads stole an existing receipt: %v", err)
				}
				opts.OpenID = "explicit-next-fresh-snapshot"
				s, err := open(opts)
				if err != nil {
					t.Fatal(err)
				}
				c, err := s.ManagedLive().Control(ctx)
				if err != nil || c.Generation != 12 || c.FinalizedHeads[1] != h {
					t.Fatalf("new snapshot=%+v %v", c, err)
				}
				managedTestClose(t, s)
			})
			for _, defect := range []string{"base_column", "receipt_hash", "unsupported_segment"} {
				t.Run(defect, func(t *testing.T) {
					ctx := context.Background()
					_, opts, open := liveDBFixture(t, dialect)
					s, err := open(opts)
					if err != nil {
						t.Fatal(err)
					}
					st := s.BusinessStore()
					query := "ALTER TABLE " + st.table("account") + " ADD COLUMN unreceipted text"
					if defect == "receipt_hash" {
						query = "UPDATE " + st.internal("_ponder_go_live_operations") + " SET after_hash='corrupt'"
					}
					if defect == "unsupported_segment" {
						query = "INSERT INTO " + st.internal("_ponder_go_live_segments") + " VALUES(1,'{}')"
					}
					if err = s.businessBackend.Exec(ctx, query); err != nil {
						t.Fatal(err)
					}
					if _, err = s.ManagedLive().Control(ctx); err == nil {
						t.Fatal("unreceipted durable state was accepted")
					}
					managedTestClose(t, s)
					opts.OpenID = "reopen-after-" + defect
					if replacement, err := open(opts); err == nil {
						managedTestClose(t, replacement)
						t.Fatal("corrupt/unsupported state was silently adopted")
					}
				})
			}
			t.Run("finite_namespace_is_not_migrated", func(t *testing.T) {
				o, opts, liveOpen := liveDBFixture(t, dialect)
				finiteOpen := func() (*IndependentStorage, error) {
					if dialect == "postgres" {
						return OpenManagedIndependentPostgres(context.Background(), o, opts.ManagedFiniteOptions)
					}
					return OpenManagedIndependentPGlite(context.Background(), o, opts.ManagedFiniteOptions)
				}
				finite, err := finiteOpen()
				if err != nil {
					t.Fatal(err)
				}
				managedTestClose(t, finite)
				if replacement, err := liveOpen(opts); err == nil {
					managedTestClose(t, replacement)
					t.Fatal("finite namespace adopted as live")
				}
				finite, err = finiteOpen()
				if err != nil {
					t.Fatalf("failed live open altered finite namespace: %v", err)
				}
				managedTestClose(t, finite)
			})
		})
	}
}

func TestManagedLiveCatalogCorruption(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, defect := range []string{"dropped_segments", "dropped_events", "dropped_stages", "missing_fingerprint", "missing_sync_owner", "guard_update_columns", "replaced_empty_table"} {
				t.Run(defect, func(t *testing.T) {
					ctx := context.Background()
					_, opts, open := liveDBFixture(t, dialect)
					s, err := open(opts)
					if err != nil {
						t.Fatal(err)
					}
					st := s.BusinessStore()
					statements := map[string][]string{
						"dropped_segments":     {"DROP TABLE " + st.internal("_ponder_go_live_segments")},
						"dropped_events":       {"DROP TABLE " + st.internal("_ponder_go_events")},
						"dropped_stages":       {"DROP TABLE " + st.internal("_ponder_go_managed_stages")},
						"missing_fingerprint":  {"DELETE FROM " + st.internal("_ponder_go_meta") + " WHERE key='fingerprint'"},
						"missing_sync_owner":   {"DELETE FROM " + st.syncTable("_ponder_go_meta") + " WHERE key='owner'"},
						"guard_update_columns": {"CREATE OR REPLACE TRIGGER \"_ponder_go_guard_row\" BEFORE INSERT OR UPDATE OF id OR DELETE ON " + st.table("account") + " FOR EACH ROW EXECUTE FUNCTION " + st.internal("_ponder_go_guard") + "()"},
						"replaced_empty_table": {"DROP TABLE " + st.internal("_ponder_go_live_segments"), "CREATE TABLE IF NOT EXISTS " + st.internal("_ponder_go_live_segments") + " (ordinal bigint PRIMARY KEY CHECK(ordinal>0),payload jsonb NOT NULL)"},
					}[defect]
					for _, statement := range statements {
						if err := s.businessBackend.Exec(ctx, statement); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := s.ManagedLive().Control(ctx); err == nil {
						t.Error("control accepted catalog/metadata corruption")
					} else {
						t.Logf("control rejected corruption: %v", err)
					}
					managedTestClose(t, s)
					opts.OpenID = "reopen-after-" + defect
					if replacement, err := open(opts); err == nil {
						managedTestClose(t, replacement)
						t.Error("reopen repaired or adopted unreceipted catalog/metadata corruption")
					} else {
						t.Logf("reopen rejected corruption: %v", err)
					}
				})
			}
		})
	}
}

type liveOpenFaultBackend struct {
	Backend
	mode                string
	fired, failReadback bool
	after               func() error
}
type liveOpenFaultTx struct {
	Transaction
	b      *liveOpenFaultBackend
	target bool
}

func (b *liveOpenFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	if b.failReadback {
		b.failReadback = false
		return nil, errors.New("injected readback unavailable")
	}
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &liveOpenFaultTx{Transaction: tx, b: b}, nil
}
func (t *liveOpenFaultTx) Exec(ctx context.Context, sql string, args ...any) error {
	if strings.HasPrefix(sql, "INSERT INTO ") && strings.Contains(sql, "_ponder_go_live_operations") {
		t.target = true
	}
	return t.Transaction.Exec(ctx, sql, args...)
}
func (t *liveOpenFaultTx) Commit(ctx context.Context) error {
	if !t.target || t.b.fired {
		return t.Transaction.Commit(ctx)
	}
	t.b.fired = true
	if t.b.mode == "before_commit" {
		_ = t.Transaction.Rollback(ctx)
		return errors.New("injected before COMMIT")
	}
	if err := t.Transaction.Commit(ctx); err != nil {
		return err
	}
	if t.b.after != nil {
		if err := t.b.after(); err != nil {
			return err
		}
	}
	if t.b.mode == "readback_failure" {
		t.b.failReadback = true
	}
	return errors.New("injected lost COMMIT acknowledgement")
}

// Direct one-process PGlite bootstrap isolates SQL/receipt fault semantics. The
// production-constructor tests above separately cover actual pair ownership.
func TestManagedLiveOpenCommitReadback(t *testing.T) {
	root := os.Getenv("PONDER_M45_TEST_ROOT")
	if root == "" {
		t.Fatal("requires retained task-owned fixture root")
	}
	for _, mode := range []string{"before_commit", "after_commit", "readback_failure", "after_commit_state_change", "after_commit_owner_loss", "after_commit_identity_change", "after_commit_metadata_change"} {
		t.Run(mode, func(t *testing.T) {
			dir, err := os.MkdirTemp(root, "open-fault-")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := OpenPGlite(context.Background(), filepath.Join(dir, "business"))
			if err != nil {
				t.Fatal(err)
			}
			fault := &liveOpenFaultBackend{Backend: raw, mode: mode}
			holder := &IndependentStorage{businessBackend: fault, closeDone: make(chan struct{})}
			defer managedTestClose(t, holder)
			o, opts := liveTestOptions()
			o.Schema = model(false)
			if mode == "after_commit_state_change" {
				fault.after = func() error {
					return raw.Exec(context.Background(), "UPDATE "+schema.Quote(o.BusinessNamespace)+".\"_ponder_go_app\" SET value=jsonb_set(value,'{is_ready}','1'::jsonb)")
				}
			}
			if mode == "after_commit_owner_loss" {
				fault.after = func() error {
					return raw.Exec(context.Background(), "UPDATE "+schema.Quote(o.BusinessNamespace)+".\"_ponder_go_app\" SET lease_token='another-owner'")
				}
			}
			if mode == "after_commit_identity_change" {
				fault.after = func() error {
					return raw.Exec(context.Background(), "UPDATE "+schema.Quote(o.BusinessNamespace)+".\"_ponder_go_managed_live_identity\" SET identity='unreceipted-identity'")
				}
			}
			if mode == "after_commit_metadata_change" {
				fault.after = func() error {
					return raw.Exec(context.Background(), "UPDATE "+schema.Quote(o.BusinessNamespace)+".\"_ponder_go_app\" SET value=jsonb_set(value,'{table_names}','[\"unreceipted-table\"]'::jsonb)")
				}
			}
			normalized, err := normalizeManagedLiveOptions(o, opts)
			if err != nil {
				t.Fatal(err)
			}
			base := managedStorageOptions{ManagedFiniteOptions: normalized.ManagedFiniteOptions, live: &normalized}
			owner, err := holder.initializeManagedFinite(context.Background(), o, base)
			if mode == "after_commit" {
				if err != nil {
					t.Fatal(err)
				}
				holder.managed = owner
				holder.live = owner.s.managedLive
				if _, err := holder.live.Control(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				var stage *ManagedStageError
				if !errors.As(err, &stage) || stage.Outcome != "unknown" {
					t.Fatalf("failed readback fabricated certainty: %v", err)
				}
			}
		})
	}
}

// Exercise the production constructor, retaining its concrete fixed PG owner
// connections and separate control pool. Only Store transactions are decorated.
func TestManagedLivePostgresOpenCommitReadback(t *testing.T) {
	for _, mode := range []string{"before_commit", "after_commit", "readback_failure", "after_commit_state_change", "after_commit_owner_loss", "after_commit_session_lock_loss", "after_commit_identity_change", "after_commit_metadata_change", "after_commit_receipt_change"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			o, opts, _ := liveDBFixture(t, "postgres")
			normalized, err := normalizeManagedLiveOptions(o, opts)
			if err != nil {
				t.Fatal(err)
			}
			var concrete *independentPGBackend
			var fault *liveOpenFaultBackend
			base := managedStorageOptions{ManagedFiniteOptions: normalized.ManagedFiniteOptions, live: &normalized}
			base.transactionBackend = func(backend Backend) Backend {
				var ok bool
				concrete, ok = backend.(*independentPGBackend)
				if !ok {
					t.Error("transaction decorator did not receive the concrete fixed PG backend")
					return backend
				}
				fault = &liveOpenFaultBackend{Backend: backend, mode: mode}
				namespace := schema.Quote(o.BusinessNamespace) + "."
				mutation := map[string]string{
					"after_commit_state_change":    "UPDATE " + namespace + "\"_ponder_go_app\" SET value=jsonb_set(value,'{is_ready}','1'::jsonb)",
					"after_commit_owner_loss":      "UPDATE " + namespace + "\"_ponder_go_app\" SET lease_token='another-owner'",
					"after_commit_identity_change": "UPDATE " + namespace + "\"_ponder_go_managed_live_identity\" SET identity='unreceipted-identity'",
					"after_commit_metadata_change": "UPDATE " + namespace + "\"_ponder_go_app\" SET value=jsonb_set(value,'{table_names}','[\"unreceipted-table\"]'::jsonb)",
					"after_commit_receipt_change":  "UPDATE " + namespace + "\"_ponder_go_live_operations\" SET after_hash='corrupt'",
				}[mode]
				if mutation != "" {
					fault.after = func() error { return concrete.pgBackend.Exec(ctx, mutation) }
				}
				if mode == "after_commit_session_lock_loss" {
					fault.after = func() error {
						// Test-only loss of one actual owned session lock, on this
						// invocation's verified new cluster. No other owner is touched.
						var unlocked bool
						if err := concrete.connection.QueryRowContext(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", o.BusinessNamespace+":independent-owner").Scan(&unlocked); err != nil {
							return err
						}
						if !unlocked {
							return errors.New("test did not release its own PG session lock")
						}
						return nil
					}
				}
				return fault
			}
			s, err := openIndependentPostgres(ctx, o, &base)
			if s != nil {
				defer managedTestClose(t, s)
			}
			if concrete == nil || fault == nil || !fault.fired {
				t.Fatalf("real PG initialization COMMIT fault was not reached: %v", err)
			}
			if mode == "after_commit" {
				if err != nil {
					t.Fatal(err)
				}
				owner := s.managed
				if s.businessBackend != concrete || owner.control != concrete.pgBackend || owner.mode != "postgres-session-lock-and-heartbeat" || len(owner.owners) != 9 {
					t.Fatal("reconciliation lost actual PG owner/control lanes")
				}
				if owner.owners[0].PID == owner.owners[4].PID {
					t.Fatal("business and sync owner sessions were not distinct")
				}
				if c, err := s.ManagedLive().Control(ctx); err != nil || c.Generation != 1 || c.LastOperation != opts.OpenID {
					t.Fatalf("committed open not reconciled exactly: %+v %v", c, err)
				}
			} else {
				var stage *ManagedStageError
				if s != nil || !errors.As(err, &stage) || stage.Outcome != "unknown" {
					t.Fatalf("uncertain PG open fabricated certainty: %v", err)
				}
				if mode == "after_commit_session_lock_loss" && !errors.Is(err, ErrLeaseLost) {
					t.Fatalf("actual PG session lock loss was not fenced: %v", err)
				}
			}
			probe, probeErr := OpenPostgres(ctx, o.PostgresURL)
			if probeErr != nil {
				t.Fatal(probeErr)
			}
			defer func() {
				if err := probe.Close(); err != nil {
					t.Error(err)
				}
			}()
			if mode == "before_commit" {
				r, err := probe.Query(ctx, "SELECT to_regnamespace($1)::text AS namespace", o.BusinessNamespace)
				if err != nil || len(r.Rows) != 1 || r.Rows[0]["namespace"] != nil {
					t.Fatalf("rolled-back initialization left durable state: %v %v", r.Rows, err)
				}
			} else {
				r, err := probe.Query(ctx, "SELECT i.lease_mode,r.generation::text,r.owner_token,r.request_hash FROM "+schema.Quote(o.BusinessNamespace)+".\"_ponder_go_managed_live_identity\" i CROSS JOIN "+schema.Quote(o.BusinessNamespace)+".\"_ponder_go_live_operations\" r WHERE r.id=$1", opts.OpenID)
				if err != nil || len(r.Rows) != 1 || r.Rows[0]["lease_mode"] != "postgres-session-lock-and-heartbeat" || r.Rows[0]["generation"] != "1" || r.Rows[0]["owner_token"] == "" || r.Rows[0]["request_hash"] == "" {
					t.Fatalf("fault did not bind a real durable PG open receipt: %v %v", r.Rows, err)
				}
			}
		})
	}
}
