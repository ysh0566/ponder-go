package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/schema"
)

// Interrupt the database engine between a journaled business write and commit.
// Both opens use the same physical data. The server fixture owns its container.
func TestDatabaseEngineRecovery(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			dir := filepath.Join(t.TempDir(), "db")
			open := func() (Backend, error) { return OpenPGlite(ctx, dir) }
			stop := func(b Backend) {
				pg := b.(*pgliteBackend)
				if err := pg.cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-pg.scanDone:
				case <-time.After(10 * time.Second):
					t.Fatal("PGlite engine did not exit")
				}
			}
			restart := func() {}
			if dialect == "postgres" {
				open, stop, restart = disposablePostgresEngine(t)
			}
			first, err := open()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = first.Close() })
			namespace := "ponder_go_engine_recovery"
			s, err := New(ctx, first, model(false), namespace, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			apply(t, s, "initial", 1, 1, Row{"id": "owner", "balance": "7"}, Insert)
			interrupted := event("replay", 1, 2)
			err = s.Apply(ctx, interrupted, func(w *Writer) error {
				if err := w.Write(ctx, "account", Row{"id": "owner", "balance": "13"}, Update); err != nil {
					return err
				}
				rows, err := w.tx.Query(ctx, "SELECT operation_id FROM "+s.internal("_ponder_go_journal")+" WHERE checkpoint=$1", checkpoint.MustEncode(interrupted.Checkpoint))
				if err != nil || len(rows.Rows) != 1 {
					t.Fatalf("in-flight journal: %+v %v", rows, err)
				}
				stop(first)
				return nil
			})
			if err == nil {
				t.Fatal("interrupted transaction returned success")
			}
			_ = first.Close()
			restart()
			next, err := open()
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			s, err = New(ctx, next, model(false), namespace, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			check := func(wantBalance string, height int) {
				t.Helper()
				if got := balance(t, s, "owner"); got != wantBalance {
					t.Fatalf("balance %s, want %s", got, wantBalance)
				}
				progress, err := s.Progress(ctx)
				if err != nil || progress["1"] != checkpoint.MustEncode(event("", 1, uint64(height)).Checkpoint) {
					t.Fatalf("progress %+v %v", progress, err)
				}
				for _, relation := range []string{"_ponder_go_journal", "_ponder_go_events"} {
					result, err := next.Query(ctx, "SELECT checkpoint FROM "+s.internal(relation)+" ORDER BY checkpoint")
					if err != nil || len(result.Rows) != height {
						t.Fatalf("%s: %+v %v", relation, result, err)
					}
					for i, row := range result.Rows {
						if row["checkpoint"] != checkpoint.MustEncode(event("", 1, uint64(i+1)).Checkpoint) {
							t.Fatalf("%s: %+v", relation, row)
						}
					}
				}
			}
			check("7", 1)
			calls := 0
			for i := 0; i < 2; i++ {
				if err := s.Apply(ctx, interrupted, func(w *Writer) error {
					calls++
					return w.Write(ctx, "account", Row{"id": "owner", "balance": "13"}, Update)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 {
				t.Fatalf("replay ran %d times", calls)
			}
			check("13", 2)
			if err := s.Revert(ctx, event("initial", 1, 1).Checkpoint); err != nil {
				t.Fatal(err)
			}
			check("7", 1)
		})
	}
}

func disposablePostgresEngine(t *testing.T) (func() (Backend, error), func(Backend), func()) {
	t.Helper()
	image := os.Getenv("PONDER_TEST_POSTGRES_ENGINE_IMAGE")
	if image == "" {
		t.Skip("set PONDER_TEST_POSTGRES_ENGINE_IMAGE to a local disposable postgres image")
	}
	password := make([]byte, 24)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	docker := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = append(os.Environ(), "POSTGRES_PASSWORD="+hex.EncodeToString(password))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	id := docker("run", "--detach", "--pull=never", "--publish", "127.0.0.1::5432", "--env", "POSTGRES_PASSWORD", "--label", "ponder-go-test=engine-recovery", image)
	if _, err := hex.DecodeString(id); len(id) != 64 || err != nil {
		t.Fatalf("invalid new container ID %q", id)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", "rm", "--force", "--volumes", id).CombinedOutput(); err != nil {
			t.Errorf("remove test container: %v %s", err, out)
		}
	})
	endpoint := &url.URL{Scheme: "postgres", User: url.UserPassword("postgres", hex.EncodeToString(password)), Path: "/postgres", RawQuery: "sslmode=disable"}
	open := func() (Backend, error) {
		endpoint.Host = docker("port", id, "5432/tcp")
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		for {
			db, err := OpenPostgres(ctx, endpoint.String())
			if err == nil {
				return db, nil
			}
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("disposable PostgreSQL startup: %w", ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	var previous string
	stop := func(db Backend) {
		result, err := db.Query(t.Context(), "SELECT pg_postmaster_start_time()::text AS started")
		if err != nil || len(result.Rows) != 1 {
			t.Fatalf("first postmaster: %v %v", result, err)
		}
		previous = fmt.Sprint(result.Rows[0]["started"])
		docker("kill", "--signal", "KILL", id)
		if code := docker("inspect", "--format", "{{.State.ExitCode}}", id); code != "137" {
			t.Fatalf("engine exit: %s", code)
		}
	}
	restart := func() {
		docker("start", id)
		db, err := open()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		result, err := db.Query(t.Context(), "SELECT pg_postmaster_start_time()::text AS started")
		if err != nil || len(result.Rows) != 1 || result.Rows[0]["started"] == previous {
			t.Fatalf("new postmaster: %v %v", result, err)
		}
	}
	return open, stop, restart
}
