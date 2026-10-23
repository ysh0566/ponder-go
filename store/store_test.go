package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
	"strings"
	"testing"
	"time"
)

func model(isolated bool) schema.Schema {
	t := schema.Table{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: !isolated}, {Name: "balance", Type: "bigint", NotNull: true}, {Name: "note", Type: "text"}}}
	if isolated {
		t.Columns = append(t.Columns, schema.Column{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true})
		t.PrimaryKey = []string{"chain_id", "id"}
	}
	return schema.Schema{Tables: []schema.Table{t}}
}
func event(id string, chain, block uint64) Event {
	return Event{ID: id, Checkpoint: checkpoint.Checkpoint{BlockTimestamp: block, ChainID: chain, BlockNumber: block, EventType: 5}}
}
func apply(t *testing.T, s *Store, id string, chain, block uint64, row Row, mode WriteMode) {
	t.Helper()
	if e := s.Apply(context.Background(), event(id, chain, block), func(w *Writer) error { return w.Write(context.Background(), "account", row, mode) }); e != nil {
		t.Fatal(e)
	}
}
func balance(t *testing.T, s *Store, id string) string {
	t.Helper()
	rows, e := s.Backend.Query(context.Background(), "SELECT balance::text AS value FROM "+s.table("account")+" WHERE id=$1", id)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows.Rows) == 0 {
		return "missing"
	}
	return fmt.Sprint(rows.Rows[0]["value"])
}

func testStore(t *testing.T, b Backend) {
	ctx := context.Background()
	ns := fmt.Sprintf("ponder_go_t_%x", time.Now().UnixNano())
	s, e := New(ctx, b, model(false), ns, schema.Multichain)
	if e != nil {
		t.Fatal(e)
	}
	t.Run("atomic handler failure and ignored write error", func(t *testing.T) {
		e := s.Apply(ctx, event("failed", 1, 1), func(w *Writer) error {
			if e := w.Write(ctx, "account", Row{"id": "bad", "balance": "1"}, Insert); e != nil {
				return e
			}
			return errors.New("handler failed")
		})
		if e == nil {
			t.Fatal("expected failure")
		}
		if got := balance(t, s, "bad"); got != "missing" {
			t.Fatal(got)
		}
		e = s.Apply(ctx, event("ignored", 1, 1), func(w *Writer) error {
			_ = w.Write(ctx, "account", Row{"id": "bad", "balance": "2"}, Insert)
			_ = w.Write(ctx, "account", Row{"id": "bad", "balance": "3"}, Insert)
			return nil
		})
		if e == nil {
			t.Fatal("ignored error committed")
		}
		if got := balance(t, s, "bad"); got != "missing" {
			t.Fatal(got)
		}
		p, e := s.Progress(ctx)
		if e != nil || len(p) != 0 {
			t.Fatalf("progress %v %v", p, e)
		}
	})
	t.Run("insert update upsert delete and exact numeric journal", func(t *testing.T) {
		apply(t, s, "one", 1, 2, Row{"id": "a", "balance": "99999999999999999999999999999999999999999999999999", "note": "keep"}, Insert)
		apply(t, s, "two", 1, 3, Row{"id": "a", "balance": "2"}, Update)
		apply(t, s, "three", 1, 4, Row{"id": "b", "balance": "3"}, Upsert)
		apply(t, s, "four", 1, 5, Row{"id": "b"}, Delete)
		if got := balance(t, s, "a"); got != "2" {
			t.Fatal(got)
		}
		if got := balance(t, s, "b"); got != "missing" {
			t.Fatal(got)
		}
		if e = s.Revert(ctx, event("", 1, 2).Checkpoint); e != nil {
			t.Fatal(e)
		}
		if got := balance(t, s, "a"); got != "99999999999999999999999999999999999999999999999999" {
			t.Fatal(got)
		}
		row, e := s.Backend.Query(ctx, "SELECT note FROM "+s.table("account")+" WHERE id='a'")
		if e != nil || row.Rows[0]["note"] != "keep" {
			t.Fatalf("%v %v", row, e)
		}
	})
	t.Run("multichain reverts global operation suffix", func(t *testing.T) {
		apply(t, s, "chain2-before", 2, 10, Row{"id": "b", "balance": "10"}, Upsert)
		apply(t, s, "chain1-reorg", 1, 3, Row{"id": "a", "balance": "20"}, Update)
		apply(t, s, "chain2-after", 2, 11, Row{"id": "b", "balance": "30"}, Update)
		if e = s.Revert(ctx, event("", 1, 2).Checkpoint); e != nil {
			t.Fatal(e)
		}
		if got := balance(t, s, "b"); got != "10" {
			t.Fatalf("global suffix retained other chain write: %s", got)
		}
		p, e := s.Progress(ctx)
		if e != nil || p["2"] != checkpoint.MustEncode(event("", 2, 10).Checkpoint) {
			t.Fatalf("%v %v", p, e)
		}
	})
	t.Run("idempotent committed event replay", func(t *testing.T) {
		calls := 0
		for i := 0; i < 2; i++ {
			e := s.Apply(ctx, event("dedupe", 2, 12), func(w *Writer) error {
				calls++
				return w.Write(ctx, "account", Row{"id": "b", "balance": "12"}, Update)
			})
			if e != nil {
				t.Fatal(e)
			}
		}
		if calls != 1 {
			t.Fatal(calls)
		}
	})
	t.Run("finalization retains prefix needed by another chain", func(t *testing.T) {
		if e = s.Finalize(ctx, event("", 2, 12).Checkpoint); e != nil {
			t.Fatal(e)
		}
		apply(t, s, "chain1-unfinalized", 1, 3, Row{"id": "a", "balance": "42"}, Update)
		apply(t, s, "chain2-finalizable", 2, 13, Row{"id": "b", "balance": "13"}, Update)
		if e = s.Finalize(ctx, event("", 2, 13).Checkpoint); e != nil {
			t.Fatal(e)
		}
		if e = s.Revert(ctx, event("", 1, 2).Checkpoint); e != nil {
			t.Fatal(e)
		}
		if got := balance(t, s, "b"); got != "12" {
			t.Fatal(got)
		}
		apply(t, s, "chain2-finalizable", 2, 13, Row{"id": "b", "balance": "13"}, Update)
	})
	t.Run("read only SQL rejects mutation", func(t *testing.T) {
		if _, e := s.QueryReadOnly(ctx, "DELETE FROM account"); e == nil {
			t.Fatal("mutation accepted")
		}
		if got := balance(t, s, "b"); got != "13" {
			t.Fatal(got)
		}
	})
	t.Run("isolated reorg preserves other chain", func(t *testing.T) {
		isolated, e := New(ctx, b, model(true), ns+"i", schema.Isolated)
		if e != nil {
			t.Fatal(e)
		}
		apply(t, isolated, "i1", 1, 1, Row{"id": "same", "chain_id": 1, "balance": "1"}, Insert)
		apply(t, isolated, "i2", 2, 2, Row{"id": "same", "chain_id": 2, "balance": "2"}, Insert)
		apply(t, isolated, "i3", 1, 3, Row{"id": "same", "chain_id": 1, "balance": "3"}, Update)
		if e = isolated.Revert(ctx, event("", 1, 1).Checkpoint); e != nil {
			t.Fatal(e)
		}
		r, e := b.Query(ctx, "SELECT chain_id,balance::text AS balance FROM "+isolated.table("account")+" ORDER BY chain_id")
		if e != nil || len(r.Rows) != 2 || r.Rows[0]["balance"] != "1" || r.Rows[1]["balance"] != "2" {
			t.Fatalf("%v %v", r, e)
		}
	})
	t.Run("existing unrelated schema is refused", func(t *testing.T) {
		name := ns + "x"
		if e := b.Exec(ctx, "CREATE SCHEMA "+schema.Quote(name)); e != nil {
			t.Fatal(e)
		}
		if _, e := New(ctx, b, model(false), name, schema.Multichain); e == nil {
			t.Fatal("adopted unowned schema")
		}
	})
}

func TestPGliteStore(t *testing.T) {
	b, e := OpenPGlite(context.Background(), "memory://")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	testStore(t, b)
}
func TestPostgresStore(t *testing.T) {
	url := os.Getenv("PONDER_TEST_POSTGRES")
	if url == "" {
		t.Skip("PostgreSQL suite requires explicit task-only PONDER_TEST_POSTGRES; no parity credit")
	}
	b, e := OpenPostgres(context.Background(), url)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	testStore(t, b)
}

// A separate OS process exits between business write and journal/progress commit.
func crashWorker(t *testing.T) {
	if os.Getenv("PONDER_CRASH_WORKER") != "1" {
		return
	}
	ctx := context.Background()
	var b Backend
	var e error
	if url := os.Getenv("PONDER_TEST_POSTGRES"); url != "" {
		b, e = OpenPostgres(ctx, url)
	} else {
		b, e = OpenPGlite(ctx, os.Getenv("PONDER_CRASH_DIR"))
	}
	if e != nil {
		t.Fatal(e)
	}
	ns := os.Getenv("PONDER_CRASH_SCHEMA")
	s, e := New(ctx, b, model(false), ns, schema.Multichain)
	if e != nil {
		t.Fatal(e)
	}
	s.Backend = &crashBackend{Backend: b, stage: os.Getenv("PONDER_CRASH_STAGE")}
	_ = s.Apply(ctx, event("crash", 1, 2), func(w *Writer) error {
		if e := w.Write(ctx, "account", Row{"id": "uncommitted", "balance": "9"}, Insert); e != nil {
			return e
		}
		if os.Getenv("PONDER_CRASH_STAGE") == "after_journal" {
			r, e := w.tx.Query(ctx, "SELECT operation_id FROM "+s.internal("_ponder_go_journal")+" WHERE checkpoint=$1", checkpoint.MustEncode(event("", 1, 2).Checkpoint))
			if e != nil || len(r.Rows) != 1 {
				t.Fatalf("journal was not captured: %v %v", r, e)
			}
			os.Exit(73)
		}
		return nil
	})
	t.Fatal("crash worker returned")
}
func TestCrashRecovery(t *testing.T) {
	if os.Getenv("PONDER_CRASH_WORKER") == "1" {
		crashWorker(t)
		return
	}
	for _, backend := range []string{"pglite", "postgres"} {
		for _, stage := range []string{"after_business", "after_journal", "after_progress", "after_commit"} {
			t.Run(backend+"/"+stage, func(t *testing.T) {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if backend == "postgres" && url == "" {
					t.Skip("explicit task-only PostgreSQL URL missing; no parity credit")
				}
				dir := filepath.Join(t.TempDir(), "db")
				ns := fmt.Sprintf("ponder_go_crash_%x", time.Now().UnixNano())
				open := func() Backend {
					var b Backend
					var e error
					if backend == "postgres" {
						b, e = OpenPostgres(context.Background(), url)
					} else {
						b, e = OpenPGlite(context.Background(), dir)
					}
					if e != nil {
						t.Fatal(e)
					}
					return b
				}
				b := open()
				s, e := New(context.Background(), b, model(false), ns, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				apply(t, s, "committed", 1, 1, Row{"id": "committed", "balance": "7"}, Insert)
				if e = b.Close(); e != nil {
					t.Fatal(e)
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecovery$")
				env := []string{}
				for _, v := range os.Environ() {
					if !strings.HasPrefix(v, "PONDER_TEST_POSTGRES=") {
						env = append(env, v)
					}
				}
				if backend == "postgres" {
					env = append(env, "PONDER_TEST_POSTGRES="+url)
				}
				cmd.Env = append(env, "PONDER_CRASH_WORKER=1", "PONDER_CRASH_DIR="+dir, "PONDER_CRASH_SCHEMA="+ns, "PONDER_CRASH_STAGE="+stage)
				out, e := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 73 {
					t.Fatalf("worker %v: %s", e, out)
				}
				b = open()
				defer b.Close()
				s, e = New(context.Background(), b, model(false), ns, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				if got := balance(t, s, "committed"); got != "7" {
					t.Fatal(got)
				}
				wantRow, wantBlock, wantEvents := "missing", uint64(1), 0
				if stage == "after_commit" {
					wantRow = "9"
					wantBlock = 2
					wantEvents = 1
				}
				if got := balance(t, s, "uncommitted"); got != wantRow {
					t.Fatal(got)
				}
				progress, e := s.Progress(context.Background())
				if e != nil || progress["1"] != checkpoint.MustEncode(event("", 1, wantBlock).Checkpoint) {
					t.Fatalf("%v %v", progress, e)
				}
				rows, e := b.Query(context.Background(), "SELECT event_id FROM "+s.internal("_ponder_go_events")+" WHERE event_id='crash'")
				if e != nil || len(rows.Rows) != wantEvents {
					t.Fatalf("%v %v", rows, e)
				}
			})
		}
	}
}

// Test-only transaction decoration injects abrupt process exits at actual SQL
// boundaries without adding failure hooks to the production implementation.
type crashBackend struct {
	Backend
	stage string
}

func (b *crashBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &crashTransaction{Transaction: tx, stage: b.stage}, nil
}

type crashTransaction struct {
	Transaction
	stage string
}

func (tx *crashTransaction) Exec(ctx context.Context, sql string, args ...any) error {
	if e := tx.Transaction.Exec(ctx, sql, args...); e != nil {
		return e
	}
	business := strings.HasPrefix(sql, "INSERT INTO ") && strings.Contains(sql, `."account" `)
	journal := strings.HasPrefix(sql, "INSERT INTO ") && strings.Contains(sql, `"_ponder_go_journal"`)
	progress := strings.HasPrefix(sql, "INSERT INTO ") && strings.Contains(sql, `"_ponder_go_progress"`)
	if tx.stage == "after_business" && business || tx.stage == "after_journal" && journal || tx.stage == "after_progress" && progress {
		os.Exit(73)
	}
	return nil
}
func (tx *crashTransaction) Commit(ctx context.Context) error {
	e := tx.Transaction.Commit(ctx)
	if e == nil && tx.stage == "after_commit" {
		os.Exit(73)
	}
	return e
}

func TestPGliteCancelledCommit(t *testing.T) {
	ctx := context.Background()
	b, e := OpenPGlite(ctx, "memory://")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if e = b.Exec(ctx, "CREATE TABLE test_cancel(id integer)"); e != nil {
		t.Fatal(e)
	}
	tx, e := b.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Exec(ctx, "INSERT INTO test_cancel VALUES (1)"); e != nil {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if e = tx.Commit(cancelled); e == nil {
		t.Fatal("cancelled commit succeeded")
	}
	r, e := b.Query(ctx, "SELECT * FROM test_cancel")
	if e != nil || len(r.Rows) != 0 {
		t.Fatalf("cancelled transaction leaked: %v %v", r, e)
	}
}
