package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
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
	// The fixture must connect to its own server even when another port is configured.
	t.Setenv("PGPORT", "15432")
	server := testpostgres.Start(t)
	open := func() (Backend, error) { return OpenPostgres(t.Context(), server.URL()) }
	var previous string
	stop := func(db Backend) {
		result, err := db.Query(t.Context(), "SELECT pg_postmaster_start_time()::text AS started")
		if err != nil || len(result.Rows) != 1 {
			t.Fatalf("first postmaster: %v %v", result, err)
		}
		previous = fmt.Sprint(result.Rows[0]["started"])
		server.Kill(t)
	}
	restart := func() {
		server.Restart(t)
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
