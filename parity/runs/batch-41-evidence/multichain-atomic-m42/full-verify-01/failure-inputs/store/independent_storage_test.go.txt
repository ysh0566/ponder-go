package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"ponder.local/ponder-go/schema"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIndependentNamespaceTopology(t *testing.T) {
	for _, pair := range [][2]string{{"", "ponder_go_sync"}, {"ponder_go_a", "ponder_go_a"}, {"ponder_go_a", "ponder_go_a_sync"}, {"ponder_go_a_rpc_1", "ponder_go_a"}, {"ponder_go_a", "ponder_go_a_legacy"}} {
		o := IndependentStorageOptions{BusinessNamespace: pair[0], SyncNamespace: pair[1]}
		if _, err := OpenIndependentPostgres(context.Background(), o); err == nil {
			t.Fatalf("accepted overlapping namespaces %v", pair)
		}
	}
	if err := independentNames(IndependentStorageOptions{BusinessNamespace: "ponder_go_business", SyncNamespace: "ponder_go_sync"}); err != nil {
		t.Fatal(err)
	}
}
func TestIndependentDirectoryTopology(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(existing, alias); err != nil {
		t.Fatal(err)
	}
	a, err := canonicalIndependentDirectory(filepath.Join(alias, "new", "db"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalIndependentDirectory(filepath.Join(existing, "new", "db"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("alias identities differ: %s %s", a, b)
	}
	for _, pair := range [][2]string{{a, b}, {existing, a}, {a, existing}} {
		if release, err := reserveIndependentDirectories(pair[0], pair[1]); err == nil {
			release()
			t.Fatalf("accepted overlap %v", pair)
		}
	}
	sibling := filepath.Join(root, "existing-sibling")
	release, err := reserveIndependentDirectories(existing, sibling)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := reserveIndependentDirectories(a, filepath.Join(root, "other")); err == nil {
		again()
		t.Fatal("accepted active owner child")
	}
	release()
	release()
	again, err := reserveIndependentDirectories(a, filepath.Join(root, "other"))
	if err != nil {
		t.Fatal(err)
	}
	again()
	memory, err := reserveIndependentDirectories("memory://", "memory://")
	if err != nil {
		t.Fatal(err)
	}
	memory()
	if _, err := os.Stat(filepath.Join(existing, "new")); !os.IsNotExist(err) {
		t.Fatal("validation mutated directory")
	}
}
func TestIndependentDirectoryRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "file://unsafe", file, filepath.Join(file, "nested"), dangling, filepath.Join(dangling, "nested")} {
		if _, err := canonicalIndependentDirectory(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	body, err := os.ReadFile(file)
	if err != nil || string(body) != "untouched" {
		t.Fatal("validation changed existing data")
	}
}

type independentCloseBackend struct {
	gate    <-chan struct{}
	entered chan struct{}
	calls   int
}

func (b *independentCloseBackend) Close() error    { b.calls++; close(b.entered); <-b.gate; return nil }
func (b *independentCloseBackend) Dialect() string { return "test" }
func (b *independentCloseBackend) Query(context.Context, string, ...any) (Result, error) {
	panic("unexpected query")
}
func (b *independentCloseBackend) Exec(context.Context, string, ...any) error {
	panic("unexpected exec")
}
func (b *independentCloseBackend) Begin(context.Context) (Transaction, error) {
	panic("unexpected begin")
}
func TestIndependentCloseRetainsOwnership(t *testing.T) {
	gate := make(chan struct{})
	b := &independentCloseBackend{gate: gate, entered: make(chan struct{})}
	s := &IndependentStorage{businessBackend: b, closeDone: make(chan struct{})}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Close(canceled); err != context.Canceled {
		t.Fatalf("bounded close: %v", err)
	}
	<-b.entered
	if err := s.Close(canceled); err != context.Canceled {
		t.Fatalf("repeated bounded close: %v", err)
	}
	close(gate)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.calls != 1 {
		t.Fatalf("backend closed %d times", b.calls)
	}
}

func TestIndependentStoragePersistentTopology(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			root := t.TempDir()
			o := IndependentStorageOptions{PostgresURL: os.Getenv("PONDER_TEST_POSTGRES"), BusinessNamespace: fmt.Sprintf("ponder_go_ib_%x", time.Now().UnixNano()), SyncNamespace: fmt.Sprintf("ponder_go_is_%x", time.Now().UnixNano()), BusinessDirectory: filepath.Join(root, "business"), SyncDirectory: filepath.Join(root, "sync"), Schema: model(false), Ordering: schema.Multichain}
			open := OpenIndependentPGlite
			if dialect == "postgres" {
				if o.PostgresURL == "" {
					t.Fatal("requires explicit task-only PONDER_TEST_POSTGRES")
				}
				open = OpenIndependentPostgres
				guard, err := OpenPostgres(ctx, o.PostgresURL)
				if err != nil {
					t.Fatal(err)
				}
				defer guard.Close()
				// Only these newly generated, proven-absent fixture families are cleaned.
				names := append(independentNamespaceFamily(o.BusinessNamespace), independentNamespaceFamily(o.SyncNamespace)...)
				for _, name := range names {
					r, e := guard.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", name)
					if e != nil || len(r.Rows) != 0 {
						t.Fatalf("fixture namespace was not fresh %q: %v %v", name, r, e)
					}
				}
				defer func() {
					for _, name := range names {
						if e := guard.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(name)+" CASCADE"); e != nil {
							t.Error(e)
						}
					}
				}()
				bad := o
				bad.SyncNamespace = o.BusinessNamespace + "_sync"
				if s, e := open(ctx, bad); e == nil {
					_ = s.Close(ctx)
					t.Fatal("accepted shared derived namespace")
				}
				r, e := guard.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", o.BusinessNamespace)
				if e != nil || len(r.Rows) != 0 {
					t.Fatal("invalid topology mutated database", r, e)
				}
			}
			init, stopInit := context.WithCancel(ctx)
			s, err := open(init, o)
			if err != nil {
				stopInit()
				t.Fatal(err)
			}
			defer func() { _ = s.Close(context.Background()) }()
			stopInit() // successful owner must outlive this request's context
			if s.businessBackend == s.syncBackend {
				t.Fatal("domains share backend")
			}
			if dialect == "postgres" && s.businessBackend.(*independentPGBackend).db == s.syncBackend.(*independentPGBackend).db {
				t.Fatal("domains share SQL pool")
			}
			{
				if duplicate, e := open(ctx, o); e == nil {
					_ = duplicate.Close(ctx)
					t.Fatal("accepted concurrent storage owner")
				}
			}
			if dialect == "postgres" {
				if e := s.BusinessActions().HistoricalBatch(ctx, 1, func(w *Writer) error {
					_, e := w.SQL(ctx, "SELECT pg_catalog.pg_advisory_unlock_all()")
					if e == nil {
						return fmt.Errorf("owner unlock SQL accepted")
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
				if duplicate, e := open(ctx, o); e == nil {
					_ = duplicate.Close(ctx)
					t.Fatal("SQL released owner lock")
				}
			}
			if dialect == "pglite" {
				payload, _ := json.Marshal(o)
				exe, e := os.Executable()
				if e != nil {
					t.Fatal(e)
				}
				child := exec.CommandContext(ctx, exe, "-test.run=^TestIndependentFilesystemOwnerChild$", "-test.v")
				child.Env = append(os.Environ(), "PONDER_INDEPENDENT_OWNER_CHILD="+string(payload))
				if output, e := child.CombinedOutput(); e != nil {
					t.Fatalf("crossprocess exclusion: %v %s", e, output)
				}
			}
			if err = s.BusinessActions().HistoricalBatch(ctx, 1, func(w *Writer) error { return w.Write(ctx, "account", Row{"id": "kept", "balance": "7"}, Insert) }); err != nil {
				t.Fatal(err)
			}
			key := `[1,"eth_getBalance",["0x0000000000000000000000000000000000000001","0x1"]]`
			rollback := errors.New("business rollback")
			err = s.BusinessActions().HistoricalBatch(ctx, 1, func(w *Writer) error {
				if e := w.Write(ctx, "account", Row{"id": "lost", "balance": "9"}, Insert); e != nil {
					return e
				}
				if e := s.SyncCache().CachePut(ctx, key, json.RawMessage(`"0x2a"`)); e != nil {
					return e
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatalf("rollback: %v", err)
			}
			if err = s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			reopened, err := open(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close(context.Background())
			if got := balance(t, reopened.BusinessStore(), "kept"); got != "7" {
				t.Fatal("committed business row not durable", got)
			}
			if got := balance(t, reopened.BusinessStore(), "lost"); got != "missing" {
				t.Fatal("rolled-back row survived", got)
			}
			raw, hit, err := reopened.SyncCache().CacheGet(ctx, key)
			if err != nil || !hit || string(raw) != `"0x2a"` {
				t.Fatalf("independent cache did not survive rollback/reopen: %s %v %v", raw, hit, err)
			}
			if dialect == "postgres" {
				pid, e := reopened.businessBackend.Query(ctx, "SELECT pg_backend_pid() AS pid")
				if e != nil {
					t.Fatal(e)
				}
				if e = reopened.syncBackend.Exec(ctx, "SELECT pg_terminate_backend($1)", pid.Rows[0]["pid"]); e != nil {
					t.Fatal(e)
				}
				called := false
				e = reopened.BusinessActions().RawBatch(ctx, 1, func(*Writer) error { called = true; return nil })
				if e == nil || called {
					t.Fatal("lost lock connection resumed business work", e, called)
				}
				if unexpected, e := open(ctx, o); e == nil {
					_ = unexpected.Close(ctx)
					t.Fatal("replacement admitted while old sync domain remains owned")
				}
				// Closing the fenced owner releases the still-live sync lock too.
				_ = reopened.Close(ctx)
				replacement, e := open(ctx, o)
				if e != nil {
					t.Fatal("replacement owner not admitted after connection death", e)
				}
				syncPID, e := replacement.syncBackend.Query(ctx, "SELECT pg_backend_pid() AS pid")
				if e != nil {
					t.Fatal(e)
				}
				if e = replacement.businessBackend.Exec(ctx, "SELECT pg_terminate_backend($1)", syncPID.Rows[0]["pid"]); e != nil {
					t.Fatal(e)
				}
				if e = replacement.SyncCache().CachePut(ctx, key, json.RawMessage(`"0x99"`)); e == nil {
					t.Fatal("lost sync connection silently resumed cache mutation")
				}
				_ = replacement.Close(ctx)
				finalOwner, e := open(ctx, o)
				if e != nil {
					t.Fatal(e)
				}
				defer finalOwner.Close(context.Background())
				value, hit, e := finalOwner.SyncCache().CacheGet(ctx, key)
				if e != nil || !hit || string(value) != `"0x2a"` {
					t.Fatal("fenced sync mutation changed durable cache", string(value), hit, e)
				}

			}

		})
	}
}

func TestIndependentPGliteRejectedTopologyPreservesPaths(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(existing, "sentinel")
	if err := os.WriteFile(sentinel, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(existing, alias); err != nil {
		t.Fatal(err)
	}
	for _, dirs := range [][2]string{{existing, existing}, {existing, filepath.Join(existing, "nested")}, {existing, alias}, {filepath.Join(alias, "child"), filepath.Join(existing, "child")}} {
		o := IndependentStorageOptions{BusinessNamespace: "ponder_go_reject_b", SyncNamespace: "ponder_go_reject_s", BusinessDirectory: dirs[0], SyncDirectory: dirs[1]}
		if s, err := OpenIndependentPGlite(context.Background(), o); err == nil {
			_ = s.Close(context.Background())
			t.Fatal("unsafe topology accepted", dirs)
		}
	}
	entries, err := os.ReadDir(existing)
	if err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Fatal("rejection mutated directory", entries, err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "original" {
		t.Fatal("rejection changed existing bytes")
	}
}

func TestIndependentFilesystemOwnerChild(t *testing.T) {
	payload := os.Getenv("PONDER_INDEPENDENT_OWNER_CHILD")
	if payload == "" {
		return
	}
	var o IndependentStorageOptions
	if err := json.Unmarshal([]byte(payload), &o); err != nil {
		t.Fatal(err)
	}
	s, err := OpenIndependentPGlite(context.Background(), o)
	if err == nil {
		_ = s.Close(context.Background())
		t.Fatal("crossprocess duplicate owner accepted")
	}
	if !strings.Contains(err.Error(), "active filesystem owner") {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

func TestIndependentPGliteOwnerProcessChild(t *testing.T) {
	status := os.Getenv("PONDER_INDEPENDENT_OWNER_STATUS")
	if status == "" {
		return
	}
	var o IndependentStorageOptions
	if err := json.Unmarshal([]byte(os.Getenv("PONDER_INDEPENDENT_OWNER_OPTIONS")), &o); err != nil {
		t.Fatal(err)
	}
	s, err := OpenIndependentPGlite(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	pids := []int{s.businessBackend.(*pgliteBackend).cmd.Process.Pid, s.syncBackend.(*pgliteBackend).cmd.Process.Pid}
	encoded, _ := json.Marshal(pids)
	if err = os.WriteFile(status, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	// The parent test deliberately kills this owner without Close after stopping
	// its Node children, proving their inherited locks cover the orphan window.
	select {}
}
func TestIndependentPGliteLocksSurviveOwnerExit(t *testing.T) {
	root := t.TempDir()
	status := filepath.Join(root, "status.json")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	o := IndependentStorageOptions{BusinessNamespace: "ponder_go_crash_b", SyncNamespace: "ponder_go_crash_s", BusinessDirectory: filepath.Join(root, "business"), SyncDirectory: filepath.Join(root, "sync")}
	encoded, _ := json.Marshal(o)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestIndependentPGliteOwnerProcessChild$")
	cmd.Env = append(os.Environ(), "PONDER_INDEPENDENT_OWNER_STATUS="+status, "PONDER_INDEPENDENT_OWNER_OPTIONS="+string(encoded))
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pids []int
	defer func() {
		_ = cmd.Process.Kill()
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGCONT)
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for len(pids) != 2 {
		data, e := os.ReadFile(status)
		if e == nil {
			_ = json.Unmarshal(data, &pids)
		}
		if len(pids) == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("owner startup", ctx.Err())
		case <-ticker.C:
		}
	}
	for _, pid := range pids {
		if err = syscall.Kill(pid, syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if reopened, e := OpenIndependentPGlite(ctx, o); e == nil {
		_ = reopened.Close(ctx)
		t.Fatal("replacement entered while orphan Node children still own data")
	}
	for _, pid := range pids {
		if err = syscall.Kill(pid, syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
	}
	for {
		release, _, e := reserveIndependentFilesystem(o.BusinessDirectory, o.SyncDirectory)
		if e == nil {
			release()
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("orphan children did not release inherited ownership", e)
		case <-ticker.C:
		}
	}
	reopened, err := OpenIndependentPGlite(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// All inherited locks are released, and the original children have exited;
	// avoid signalling recycled PIDs in the deferred failure cleanup.
	pids = nil
}

func TestIndependentOwnedSQLLockGuard(t *testing.T) {
	for _, q := range []string{`SELECT pg_advisory_unlock_all()`, `SELECT pg_catalog . "pg_advisory_unlock_all" ()`, `SELECT 1; SELECT pg_advisory_unlock(1)`, `SELECT pg_advisory_unlock_shared(1)`, `SELECT U&"pg_advisory_un\006cock_all"()`} {
		if err := rejectIndependentUnlock(q); err == nil {
			t.Fatalf("owner unlock accepted: %s", q)
		}
	}
	for _, q := range []string{`SELECT 'pg_advisory_unlock_all' AS note`, `CREATE TABLE pg_advisory_unlock_events(id text)`, `SELECT pg_advisory_lock(1)`, `SELECT 1; SELECT 2`} {
		if err := rejectIndependentUnlock(q); err != nil {
			t.Fatalf("legitimate SQL rejected: %s: %v", q, err)
		}
	}
}
