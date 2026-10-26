package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

func TestManagedFiniteOptionsAndSchemaSnapshot(t *testing.T) {
	o := IndependentStorageOptions{BusinessNamespace: "ponder_go_m44", SyncNamespace: "ponder_go_m44cache"}
	m := ManagedFiniteOptions{BuildID: "b", PlanID: "p", Chains: []MigrationChain{{ID: 1, Name: "mainnet"}}, EndBlocks: map[uint64]uint64{1: 0}}
	got, e := normalizeManagedOptions(o, m)
	if e != nil {
		t.Fatal(e)
	}
	m.EndBlocks[1] = 99
	m.Chains[0].Name = "mutated"
	if got.EndBlocks[1] != 0 || got.Chains[0].Name != "mainnet" {
		t.Fatal("identity aliases caller memory")
	}
	for _, alias := range []string{"ponder_go_m44_sync", "ponder_go_m44cache", "foreign"} {
		bad := got
		bad.ViewsSchema = alias
		if _, e := normalizeManagedOptions(o, bad); e == nil {
			t.Fatalf("accepted alias %s", alias)
		}
	}
	n := big.NewInt(17)
	called := false
	in := schema.Schema{Tables: []schema.Table{{Name: "x", Columns: []schema.Column{{Name: "id", Type: "bigint", Default: n, DefaultFn: func() any { called = true; return 1 }}}, Indexes: []schema.Index{{Name: "idx", Columns: []string{"id"}}}}}}
	out := cloneManagedSchema(in)
	in.Tables[0].Columns[0].Name = "changed"
	in.Tables[0].Indexes[0].Columns[0] = "changed"
	n.SetInt64(99)
	if out.Tables[0].Columns[0].Name != "id" || out.Tables[0].Indexes[0].Columns[0] != "id" || out.Tables[0].Columns[0].Default.(*big.Int).Int64() != 17 {
		t.Fatal("schema snapshot mutated")
	}
	out.Tables[0].Columns[0].DefaultFn()
	if !called {
		t.Fatal("callback discarded")
	}
}

var managedSharedFixtureOnce sync.Once
var managedSharedFixtureDirectory string
var managedSharedFixtureError error
var managedSharedFixtureMu sync.Mutex
var managedSharedFixtureActive bool

// Supplemental M44 tests; no original-source equivalence credit. Persistent
// artifacts are retained under an explicitly supplied task-owned root.
func managedFixture(t *testing.T, dialect string, changes ...func(*IndependentStorageOptions, *ManagedFiniteOptions)) (IndependentStorageOptions, ManagedFiniteOptions, func() (*IndependentStorage, error)) {
	t.Helper()
	root := os.Getenv("PONDER_M44_TEST_ROOT")
	if root == "" {
		t.Fatal("requires explicit retained PONDER_M44_TEST_ROOT")
	}
	// Only ordinary cases share a NEW process-local pair. Tests whose premise
	// is directory exclusion or retained ownership keep dedicated directories.
	shared := dialect == "pglite" && !strings.Contains(t.Name(), "CrossProcessExclusion") && !strings.Contains(t.Name(), "BusyReadsAndRetainedClose")
	var dir string
	var e error
	owners := []*IndependentStorage{}
	if shared {
		managedSharedFixtureOnce.Do(func() { managedSharedFixtureDirectory, managedSharedFixtureError = os.MkdirTemp(root, "store-shared-") })
		if managedSharedFixtureError != nil {
			t.Fatal(managedSharedFixtureError)
		}
		managedSharedFixtureMu.Lock()
		if managedSharedFixtureActive {
			managedSharedFixtureMu.Unlock()
			t.Fatal("shared fixture requires serial cases and successful prior owner shutdown")
		}
		managedSharedFixtureActive = true
		managedSharedFixtureMu.Unlock()
		dir = managedSharedFixtureDirectory
		t.Cleanup(func() {
			allClosed := true
			for _, owner := range owners {
				select {
				case <-owner.closeDone:
				default:
					t.Error("shared fixture owner was not explicitly closed before next case")
					managedTestClose(t, owner)
				}
				select {
				case <-owner.closeDone:
				default:
					allClosed = false
				}
			}
			// A timed-out Close retains real filesystem ownership. Poison the
			// pair rather than allowing the next raw precheck to bypass it.
			managedSharedFixtureMu.Lock()
			managedSharedFixtureActive = !allClosed || t.Failed()
			managedSharedFixtureMu.Unlock()
		})
	} else {
		dir, e = os.MkdirTemp(root, "managed-finite-")
		if e != nil {
			t.Fatal(e)
		}
	}
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	model := model(false)
	model.Tables[0].Indexes = []schema.Index{{Name: "managed_balance_idx", Columns: []string{"balance"}}}
	o := IndependentStorageOptions{PostgresURL: os.Getenv("PONDER_TEST_POSTGRES"), BusinessNamespace: "ponder_go_mf_" + suffix, SyncNamespace: "ponder_go_mc_" + suffix, BusinessDirectory: filepath.Join(dir, "business"), SyncDirectory: filepath.Join(dir, "sync"), Schema: model, Ordering: schema.Multichain}
	m := ManagedFiniteOptions{BuildID: "m44-test", PlanID: "fixed-end-1", Chains: []MigrationChain{{ID: 1, Name: "mainnet"}, {ID: 10, Name: "optimism"}}, EndBlocks: map[uint64]uint64{1: 1, 10: 1}, ViewsSchema: o.BusinessNamespace + "_view"}
	for _, change := range changes {
		change(&o, &m)
	}
	names := append(independentNamespaceFamily(o.BusinessNamespace), independentNamespaceFamily(o.SyncNamespace)...)
	if m.ViewsSchema != "" {
		names = append(names, m.ViewsSchema)
	}
	precheck := func(b Backend) {
		defer func() {
			if e := b.Close(); e != nil {
				t.Errorf("fixture precheck Close: %v", e)
			}
		}()
		for _, name := range names {
			r, e := b.Query(context.Background(), "SELECT nspname FROM pg_namespace WHERE nspname=$1", name)
			if e != nil {
				t.Fatal(e)
			}
			if len(r.Rows) > 0 {
				t.Fatalf("pre-existing managed fixture namespace: %s", name)
			}
		}
	}
	if dialect == "postgres" {
		if o.PostgresURL == "" {
			t.Fatal("requires explicit task-only PONDER_TEST_POSTGRES")
		}
		b, e := OpenPostgres(context.Background(), o.PostgresURL)
		if e != nil {
			t.Fatal(e)
		}
		precheck(b)
	} else {
		// No managed owner exists yet. Open and close each domain serially for
		// catalog-only precheck; never open a second process over an active owner.
		for _, directory := range []string{o.BusinessDirectory, o.SyncDirectory} {
			b, e := OpenPGlite(context.Background(), directory)
			if e != nil {
				t.Fatal(e)
			}
			precheck(b)
		}
	}
	open := func() (*IndependentStorage, error) {
		if dialect == "postgres" {
			if o.PostgresURL == "" {
				t.Fatal("requires explicit task-only PONDER_TEST_POSTGRES")
			}
			return OpenManagedIndependentPostgres(context.Background(), o, m)
		}
		owner, e := OpenManagedIndependentPGlite(context.Background(), o, m)
		if e == nil && shared {
			owners = append(owners, owner)
		}
		return owner, e
	}
	t.Logf("retained managed fixture namespaces=%s,%s directory=%s shared=%v", o.BusinessNamespace, o.SyncNamespace, dir, shared)
	return o, m, open
}
func TestManagedFiniteDeferredActivationAndRestart(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			o, _, open := managedFixture(t, dialect)
			s, e := open()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { managedTestClose(t, s) }()
			if r, e := s.managed.Ready(ctx); e != nil || r {
				t.Fatalf("new ready=%v %v", r, e)
			}
			r, e := s.businessBackend.Query(ctx, "SELECT to_regclass($1)::text AS name", schema.Quote(o.BusinessNamespace)+".managed_balance_idx")
			if e != nil || r.Rows[0]["name"] != nil {
				t.Fatalf("eager index: %v %v", r, e)
			}
			states := multichainInitialStates()
			if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
				t.Fatal(e)
			}
			if e = s.managed.Activate(ctx, states); e != nil {
				t.Fatal(e)
			}
			if ready, e := s.managed.Ready(ctx); e != nil || !ready {
				t.Fatalf("ready=%v %v", ready, e)
			}
			if e = s.managed.Activate(ctx, states); e != nil {
				t.Fatalf("idempotent activation: %v", e)
			}
			if e = s.Close(ctx); e != nil {
				t.Fatal(e)
			}
			s, e = open()
			if e != nil {
				t.Fatalf("safe restart: %v", e)
			}
			if ready, e := s.managed.Ready(ctx); e != nil || ready {
				t.Fatalf("restart ready=%v %v", ready, e)
			}
			if e = s.managed.Activate(ctx, states); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestManagedFiniteRejectsUnmarkedIndex(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			o, _, open := managedFixture(t, dialect)
			s, e := open()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { managedTestClose(t, s) }()
			states := multichainInitialStates()
			if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
				t.Fatal(e)
			}
			if e = s.businessBackend.Exec(ctx, "CREATE INDEX managed_balance_idx ON "+schema.Quote(o.BusinessNamespace)+".account (balance DESC)"); e != nil {
				t.Fatal(e)
			}
			if e = s.managed.Activate(ctx, states); e == nil {
				t.Fatal("adopted unmarked descending index")
			}
			if ready, e := s.managed.Ready(ctx); e != nil || ready {
				t.Fatalf("ready=%v %v", ready, e)
			}
		})
	}
}

func TestManagedFiniteCatalogTamperFailsClosed(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			o, _, open := managedFixture(t, dialect)
			s, e := open()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { managedTestClose(t, s) }()
			states := multichainInitialStates()
			if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
				t.Fatal(e)
			}
			if e = s.managed.Activate(ctx, states); e != nil {
				t.Fatal(e)
			}
			if e = s.businessBackend.Exec(ctx, "ALTER TABLE "+schema.Quote(o.BusinessNamespace)+".account DISABLE TRIGGER live_query"); e != nil {
				t.Fatal(e)
			}
			if e = s.managed.Activate(ctx, states); e == nil {
				t.Fatal("accepted altered live trigger")
			}
			if ready, e := s.managed.Ready(ctx); e == nil || ready {
				t.Fatalf("Ready accepted altered catalog: %v %v", ready, e)
			}
			if e = s.Close(ctx); e != nil {
				t.Fatal(e)
			}
			replacement, e := open()
			if e == nil {
				managedTestClose(t, replacement)
				t.Fatal("reopened altered activation catalog")
			}
		})
	}
}

type managedCommitFaultBackend struct {
	Backend
	after      bool
	fired      bool
	cancel     context.CancelFunc
	target     string
	failMarker bool
}

func (b *managedCommitFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &managedCommitFaultTx{Transaction: tx, b: b}, nil
}

type managedCommitFaultTx struct {
	Transaction
	b     *managedCommitFaultBackend
	stage string
}

func (t *managedCommitFaultTx) Exec(ctx context.Context, query string, args ...any) error {
	if strings.HasPrefix(query, "INSERT INTO ") && strings.Contains(query, "_ponder_go_managed_stages") && len(args) > 0 {
		t.stage = fmt.Sprint(args[0])
		if t.b.failMarker && t.stage == t.b.target && !t.b.fired {
			t.b.fired = true
			if e := t.Transaction.Exec(ctx, query, args...); e != nil {
				return e
			}
			return fmt.Errorf("injected failure after stage DDL and marker")
		}
	}
	return t.Transaction.Exec(ctx, query, args...)
}

func (t *managedCommitFaultTx) Commit(ctx context.Context) error {
	if t.b.fired || (t.b.target != "" && t.stage != t.b.target) {
		return t.Transaction.Commit(ctx)
	}
	t.b.fired = true
	if t.b.after {
		if e := t.Transaction.Commit(ctx); e != nil {
			return e
		}
	}
	if t.b.cancel != nil {
		t.b.cancel()
	}
	return fmt.Errorf("injected commit acknowledgement failure")
}
func TestManagedFiniteCommitReconciliation(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after_%v", dialect, after), func(t *testing.T) {
				ctx := context.Background()
				_, _, open := managedFixture(t, dialect)
				s, e := open()
				if e != nil {
					t.Fatal(e)
				}
				defer func() { managedTestClose(t, s) }()
				states := multichainInitialStates()
				if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
					t.Fatal(e)
				}
				original := s.managed.s.Backend
				s.managed.s.Backend = &managedCommitFaultBackend{Backend: original, after: after}
				defer func() { s.managed.s.Backend = original }()
				e = s.managed.Activate(ctx, states)
				if after && e != nil {
					t.Fatalf("committed ack was not reconciled: %v", e)
				}
				if !after {
					stage, ok := e.(*ManagedStageError)
					if !ok || stage.Outcome != "not_committed" {
						t.Fatalf("expected proven not_committed: %T %v", e, e)
					}
					if e = s.managed.Activate(ctx, states); e != nil {
						t.Fatalf("retry after confirmed rollback: %v", e)
					}
				}
			})
		}
	}
}

func TestManagedFinitePostgresControlLane(t *testing.T) {
	t.Run("heartbeat_during_27_second_handler", func(t *testing.T) {
		ctx := context.Background()
		_, _, open := managedFixture(t, "postgres")
		s, e := open()
		if e != nil {
			t.Fatal(e)
		}
		defer func() { managedTestClose(t, s) }()
		e = s.business.InitializeMultichain(ctx, multichainInitialStates(), func(*Writer, func(uint64) error) error { time.Sleep(27 * time.Second); return nil })
		if e != nil {
			t.Fatalf("independent heartbeat did not preserve active handler: %v", e)
		}
		if e = s.managed.Activate(ctx, multichainInitialStates()); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("sync_owner_loss_rejects_final_commit", func(t *testing.T) {
		ctx := context.Background()
		_, _, open := managedFixture(t, "postgres")
		s, e := open()
		if e != nil {
			t.Fatal(e)
		}
		defer func() { managedTestFaultClose(t, s) }()
		syncBackend := s.syncBackend.(*independentPGBackend)
		var pid int
		if e = syncBackend.connection.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); e != nil {
			t.Fatal(e)
		}
		e = s.business.InitializeMultichain(ctx, multichainInitialStates(), func(*Writer, func(uint64) error) error {
			_, e := s.managed.control.Query(ctx, "SELECT pg_terminate_backend($1)", pid)
			return e
		})
		if e == nil {
			t.Fatal("committed after sync owner session loss")
		}
		rows, e := s.businessBackend.Query(ctx, "SELECT 1 FROM "+s.managed.s.internal("_ponder_go_chains"))
		if e != nil || len(rows.Rows) != 0 {
			t.Fatalf("failed handler committed metadata: %v %v", rows, e)
		}
	})
	t.Run("expired_ready_and_handler_fail_closed", func(t *testing.T) {
		ctx := context.Background()
		_, _, open := managedFixture(t, "postgres")
		s, e := open()
		if e != nil {
			t.Fatal(e)
		}
		defer func() { managedTestClose(t, s) }()
		s.managed.cancel()
		<-s.managed.done
		if e = s.managed.control.Exec(ctx, "UPDATE "+s.managed.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{heartbeat_at}','0'::jsonb)"); e != nil {
			t.Fatal(e)
		}
		if ready, e := s.managed.Ready(ctx); e == nil || ready {
			t.Fatalf("expired Ready: %v %v", ready, e)
		}
		if e = s.business.InitializeMultichain(ctx, multichainInitialStates(), func(*Writer, func(uint64) error) error { return nil }); e == nil {
			t.Fatal("expired owner committed")
		}
	})
}
func TestManagedFiniteRestartRejectsUnsupportedState(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"mixed_checkpoint", "orphan_events", "pending_journal"} {
			t.Run(dialect+"/"+kind, func(t *testing.T) {
				ctx := context.Background()
				_, _, open := managedFixture(t, dialect)
				s, e := open()
				if e != nil {
					t.Fatal(e)
				}
				defer func() { managedTestClose(t, s) }()
				switch kind {
				case "mixed_checkpoint":
					if e = s.business.InitializeMultichain(ctx, multichainInitialStates(), func(*Writer, func(uint64) error) error { return nil }); e != nil {
						t.Fatal(e)
					}
					e = s.businessBackend.Exec(ctx, "UPDATE "+s.managed.s.internal("_ponder_go_finalized")+" SET safe_checkpoint=$1 WHERE chain_id=1", strings.Repeat("0", 75))
				case "orphan_events":
					e = s.businessBackend.Exec(ctx, "INSERT INTO "+s.managed.s.internal("_ponder_go_events")+" VALUES ('orphan',$1,1,0,0)", strings.Repeat("0", 75))
				case "pending_journal":
					e = s.businessBackend.Exec(ctx, "INSERT INTO "+s.managed.s.internal("_ponder_go_journal")+" (table_name,row_key,row_data,operation,checkpoint,chain_id) VALUES ('account','{}','{}',0,$1,1)", strings.Repeat("0", 75))
				}
				if e != nil {
					t.Fatal(e)
				}
				if e = s.Close(ctx); e != nil {
					t.Fatal(e)
				}
				replacement, e := open()
				if e == nil {
					managedTestClose(t, replacement)
					t.Fatal("unsupported restart accepted")
				}
			})
		}
	}
}

func TestManagedFiniteCanceledCommitStaysUnknown(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, _, open := managedFixture(t, dialect)
			s, e := open()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { managedTestClose(t, s) }()
			states := multichainInitialStates()
			if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
				t.Fatal(e)
			}
			caller, cancel := context.WithCancel(ctx)
			defer cancel()
			original := s.managed.s.Backend
			s.managed.s.Backend = &managedCommitFaultBackend{Backend: original, after: true, cancel: cancel}
			defer func() { s.managed.s.Backend = original }()
			e = s.managed.Activate(caller, states)
			stage, ok := e.(*ManagedStageError)
			if !ok || stage.Outcome != "unknown" {
				t.Fatalf("canceled uncertain commit: %T %v", e, e)
			}
			if ready, e := s.managed.Ready(ctx); e != nil || ready {
				t.Fatalf("partial stage ready: %v %v", ready, e)
			}
		})
	}
}
func TestManagedFinitePostgresIdentityFences(t *testing.T) {
	t.Run("backend_start_mismatch", func(t *testing.T) {
		ctx := context.Background()
		_, _, open := managedFixture(t, "postgres")
		s, e := open()
		if e != nil {
			t.Fatal(e)
		}
		defer func() { managedTestFaultClose(t, s) }()
		s.managed.cancel()
		<-s.managed.done
		s.managed.owners[0].Started = "1970-01-01 00:00:00+00"
		if e = s.managed.verifyOwnership(ctx); e == nil {
			t.Fatal("accepted reused PID with different backend start")
		}
		if e = s.managed.heartbeat(ctx); e == nil {
			t.Fatal("renewed mismatched session")
		}
	})
	t.Run("stale_close_cannot_unlock_new_token", func(t *testing.T) {
		ctx := context.Background()
		_, _, open := managedFixture(t, "postgres")
		s, e := open()
		if e != nil {
			t.Fatal(e)
		}
		defer func() { managedTestFaultClose(t, s) }()
		s.managed.cancel()
		<-s.managed.done
		if e = s.managed.control.Exec(ctx, "UPDATE "+s.managed.s.internal("_ponder_go_app")+" SET lease_token='replacement-test-token'"); e != nil {
			t.Fatal(e)
		}
		if e = s.managed.heartbeat(ctx); e == nil {
			t.Fatal("renewed replacement token")
		}
		if e = s.managed.close(); e == nil {
			t.Fatal("stale close matched replacement token")
		}
		rows, e := s.managed.control.Query(ctx, "SELECT value->>'is_locked' AS locked FROM "+s.managed.s.internal("_ponder_go_app"))
		if e != nil || rows.Rows[0]["locked"] != "1" {
			t.Fatalf("stale close altered replacement: %v %v", rows, e)
		}
	})
}

func managedTestClose(t *testing.T, s *IndependentStorage) {
	t.Helper()
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e := s.Close(ctx); e != nil {
		t.Errorf("normal managed Close: %v", e)
	}
}
func managedTestFaultClose(t *testing.T, s *IndependentStorage) {
	t.Helper()
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	e := s.Close(ctx)
	if e != nil && !managedExpectedCloseFault(e, 0) {
		t.Errorf("unexpected fault-path Close: %v", e)
	}
}
func managedExpectedCloseFault(e error, depth int) bool {
	if e == nil || depth > 32 {
		return false
	}
	if many, ok := e.(interface{ Unwrap() []error }); ok {
		seen := false
		for _, child := range many.Unwrap() {
			if child != nil {
				seen = true
				if !managedExpectedCloseFault(child, depth+1) {
					return false
				}
			}
		}
		return seen
	}
	if one, ok := e.(interface{ Unwrap() error }); ok {
		return managedExpectedCloseFault(one.Unwrap(), depth+1)
	}
	if pg, ok := e.(*pgconn.PgError); ok {
		return pg.Code == "57P01"
	}
	return e == ErrLeaseLost || e == sql.ErrConnDone || e == driver.ErrBadConn
}

func TestManagedFinitePostgresExpiryInsideHandler(t *testing.T) {
	ctx := context.Background()
	_, _, open := managedFixture(t, "postgres")
	s, e := open()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { managedTestClose(t, s) }()
	s.managed.cancel()
	<-s.managed.done
	e = s.business.InitializeMultichain(ctx, multichainInitialStates(), func(*Writer, func(uint64) error) error {
		return s.managed.control.Exec(ctx, "UPDATE "+s.managed.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{heartbeat_at}','0'::jsonb)")
	})
	if !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("expired admitted handler: %v", e)
	}
	rows, e := s.businessBackend.Query(ctx, "SELECT 1 FROM "+s.managed.s.internal("_ponder_go_chains"))
	if e != nil || len(rows.Rows) != 0 {
		t.Fatalf("expired handler persisted metadata: %v %v", rows, e)
	}
}
func TestManagedFinitePGliteCrossProcessExclusion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o, _, open := managedFixture(t, "pglite")
	s, e := open()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { managedTestClose(t, s) }()
	encoded, e := json.Marshal(o)
	if e != nil {
		t.Fatal(e)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	child := exec.CommandContext(ctx, exe, "-test.run=^TestIndependentFilesystemOwnerChild$", "-test.v")
	child.Env = append(os.Environ(), "PONDER_INDEPENDENT_OWNER_CHILD="+string(encoded))
	out, e := child.CombinedOutput()
	if e != nil {
		t.Fatalf("crossprocess flock exclusion: %v %s", e, out)
	}
}
func TestManagedFiniteNotificationCommitBoundary(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, m, open := managedFixture(t, dialect)
			s, e := open()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { managedTestClose(t, s) }()
			states := multichainInitialStates()
			if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
				t.Fatal(e)
			}
			channels := []string{s.business.LiveQueryChannel(), m.ViewsSchema + "_live_query"}
			listeners := []NotificationListener{}
			for _, channel := range channels {
				l, e := Listen(ctx, s.businessBackend, channel)
				if e != nil {
					t.Fatal(e)
				}
				listeners = append(listeners, l)
				defer func() {
					if e := l.Close(); e != nil {
						t.Error(e)
					}
				}()
			}
			if e = s.managed.Activate(ctx, states); e != nil {
				t.Fatal(e)
			}
			for _, l := range listeners {
				expectNoNotification(t, l)
			}
			for _, commit := range []bool{false, true} {
				tx, e := s.businessBackend.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if e = prepareLiveQueryChanges(ctx, tx, dialect); e != nil {
					_ = tx.Rollback(ctx)
					t.Fatal(e)
				}
				if e = tx.Exec(ctx, "UPDATE "+s.managed.s.internal("_ponder_go_progress")+" SET checkpoint=checkpoint WHERE chain_id=1"); e != nil {
					_ = tx.Rollback(ctx)
					t.Fatal(e)
				}
				if commit {
					e = tx.Commit(ctx)
				} else {
					e = tx.Rollback(ctx)
				}
				if e != nil {
					t.Fatal(e)
				}
				for i, l := range listeners {
					if commit {
						waitNotification(t, l, channels[i], "[]")
					} else {
						expectNoNotification(t, l)
					}
				}
			}
		})
	}
}

func TestManagedFiniteEveryActivationStageOutcome(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, stage := range []string{"index:account:managed_note_idx", "capture", "live-query", "aliases", "ready"} {
			for _, outcome := range []string{"ddl_rollback", "ack_before", "ack_after"} {
				t.Run(dialect+"/"+stage+"/"+outcome, func(t *testing.T) {
					ctx := context.Background()
					_, _, open := managedFixture(t, dialect, func(o *IndependentStorageOptions, _ *ManagedFiniteOptions) {
						o.Schema.Tables[0].Indexes = append(o.Schema.Tables[0].Indexes, schema.Index{Name: "managed_note_idx", Columns: []string{"note"}})
					})
					s, e := open()
					if e != nil {
						t.Fatal(e)
					}
					defer func() { managedTestClose(t, s) }()
					states := multichainInitialStates()
					if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
						t.Fatal(e)
					}
					original := s.managed.s.Backend
					fault := &managedCommitFaultBackend{Backend: original, target: stage, after: outcome == "ack_after", failMarker: outcome == "ddl_rollback"}
					s.managed.s.Backend = fault
					defer func() { s.managed.s.Backend = original }()
					e = s.managed.Activate(ctx, states)
					if !fault.fired {
						t.Fatal("target stage injection not reached")
					}
					if outcome == "ack_after" {
						if e != nil {
							t.Fatalf("committed stage not reconciled: %v", e)
						}
					} else {
						if e == nil {
							t.Fatal("uncommitted stage reported success")
						}
						if outcome == "ack_before" {
							stageErr, ok := e.(*ManagedStageError)
							if !ok || stageErr.Outcome != "not_committed" {
								t.Fatalf("unconfirmed rollback: %v", e)
							}
						}
						if ready, e := s.managed.Ready(ctx); e != nil || ready {
							t.Fatalf("partial activation ready=%v %v", ready, e)
						}
						rows, e := original.Query(ctx, "SELECT name FROM "+s.managed.s.internal("_ponder_go_managed_stages")+" WHERE name=$1", stage)
						if e != nil || len(rows.Rows) != 0 {
							t.Fatalf("failed marker survived: %v %v", rows, e)
						}
						if e = s.managed.Activate(ctx, states); e != nil {
							t.Fatalf("retry confirmed rollback: %v", e)
						}
					}
					if ready, e := s.managed.Ready(ctx); e != nil || !ready {
						t.Fatalf("completed stage matrix ready=%v %v", ready, e)
					}
				})
			}
		}
	}
}
func TestManagedFiniteEmptySchemaDistinctEnds(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, _, open := managedFixture(t, dialect, func(o *IndependentStorageOptions, m *ManagedFiniteOptions) {
				o.Schema = schema.Schema{}
				m.EndBlocks[10] = 2
			})
			s, e := open()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { managedTestClose(t, s) }()
			states := multichainInitialStates()
			cp := checkpoint.MustEncode(checkpoint.Block(101, 10, 2, true))
			states[1].LatestCheckpoint = cp
			states[1].SafeCheckpoint = cp
			states[1].FinalizedCheckpoint = cp
			if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
				t.Fatal(e)
			}
			if e = s.managed.Activate(ctx, states); e != nil {
				t.Fatal(e)
			}
			if ready, e := s.managed.Ready(ctx); e != nil || !ready {
				t.Fatalf("empty schema distinct ends: %v %v", ready, e)
			}
			managedTestClose(t, s)
			s, e = open()
			if e != nil {
				t.Fatal(e)
			}
			if e = s.managed.Activate(ctx, states); e != nil {
				t.Fatal(e)
			}
		})
	}
}

type managedBoundaryMutationBackend struct {
	Backend
	mutate func() error
	fired  bool
}
type managedBoundaryMutationTx struct {
	Transaction
	b *managedBoundaryMutationBackend
}

func (b *managedBoundaryMutationBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &managedBoundaryMutationTx{Transaction: tx, b: b}, nil
}
func (tx *managedBoundaryMutationTx) Commit(ctx context.Context) error {
	if e := tx.Transaction.Commit(ctx); e != nil {
		return e
	}
	if !tx.b.fired {
		tx.b.fired = true
		return tx.b.mutate()
	}
	return nil
}
func TestManagedFiniteFreshBoundaryBetweenStages(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, _, open := managedFixture(t, dialect)
			s, e := open()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { managedTestClose(t, s) }()
			states := multichainInitialStates()
			if e = s.business.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { return nil }); e != nil {
				t.Fatal(e)
			}
			original := s.managed.s.Backend
			mutation := &managedBoundaryMutationBackend{Backend: original, mutate: func() error {
				cp := checkpoint.MustEncode(checkpoint.Block(102, 10, 1, true))
				return original.Exec(ctx, "WITH latest AS (UPDATE "+s.managed.s.internal("_ponder_go_progress")+" SET checkpoint=$1 WHERE chain_id=10), finalized AS (UPDATE "+s.managed.s.internal("_ponder_go_finalized")+" SET checkpoint=$1,safe_checkpoint=$1 WHERE chain_id=10) UPDATE "+s.managed.s.internal("_ponder_go_runtime_cursor")+" SET checkpoint=$1 WHERE chain_id=10", cp)
			}}
			s.managed.s.Backend = mutation
			defer func() { s.managed.s.Backend = original }()
			if e = s.managed.Activate(ctx, states); e == nil {
				t.Fatal("accepted endpoint changed after first activation commit")
			}
			if !mutation.fired {
				t.Fatal("mutation did not occur between stages")
			}
			if ready, e := s.managed.Ready(ctx); e != nil || ready {
				t.Fatalf("changed endpoint ready=%v %v", ready, e)
			}
		})
	}
}

func TestManagedFiniteActivationBusyPreservesCancellation(t *testing.T) {
	m := &ManagedFinite{}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := m.Activate(ctx, nil); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled activation waited: %v", e)
	}
	if e := m.Activate(context.Background(), nil); !errors.Is(e, ErrManagedFiniteBusy) {
		t.Fatalf("concurrent activation not rejected: %v", e)
	}
}
func TestManagedFinitePGliteBusyReadsAndRetainedClose(t *testing.T) {
	ctx := context.Background()
	_, _, open := managedFixture(t, "pglite")
	s, e := open()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { managedTestClose(t, s) }()
	entered := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		result <- s.business.InitializeMultichain(ctx, multichainInitialStates(), func(*Writer, func(uint64) error) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("held handler did not enter")
	}
	for _, canceled := range []bool{false, true} {
		caller, cancel := context.WithTimeout(ctx, time.Second)
		if canceled {
			cancel()
		}
		start := time.Now()
		_, metadataErr := s.managed.Metadata(caller)
		_, readyErr := s.managed.Ready(caller)
		cancel()
		expected := ErrManagedFiniteBusy
		if canceled {
			expected = context.Canceled
		}
		if !errors.Is(metadataErr, expected) || !errors.Is(readyErr, expected) {
			t.Fatalf("held PGlite read: metadata=%v ready=%v expected=%v", metadataErr, readyErr, expected)
		}
		if time.Since(start) > time.Second {
			t.Fatal("held read exceeded caller deadline")
		}
	}
	caller, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	e = s.Close(caller)
	cancel()
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("held close: %v", e)
	}
	select {
	case <-s.closeDone:
		t.Fatal("storage closed before handler released")
	default:
	}
	if replacement, e := open(); e == nil {
		managedTestClose(t, replacement)
		t.Fatal("replacement admitted while close unfinished")
	}
	unblock()
	select {
	case e = <-result:
		if !errors.Is(e, ErrLeaseLost) {
			t.Fatalf("close did not fence held handler: %v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("held handler did not finish")
	}
	managedTestClose(t, s)
}

func TestManagedFiniteActivationHeldStoreRespectsDeadline(t *testing.T) {
	m := &ManagedFinite{s: &Store{}, opts: ManagedFiniteOptions{Chains: []MigrationChain{{ID: 1, Name: "one"}}}}
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	e := m.Activate(ctx, []CheckpointState{{ChainID: 1}})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("held store activation: %v", e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("activation ignored deadline behind store mutex")
	}
}
