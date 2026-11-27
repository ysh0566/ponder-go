package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/schema"
	"math/big"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRawSQLJournal(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			var b Backend
			var e error
			if dialect == "postgres" {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if url == "" {
					t.Skip("explicit task database required; no parity credit")
				}
				b, e = OpenPostgres(ctx, url)
			} else {
				b, e = OpenPGlite(ctx, "memory://")
			}
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			m := model(false)
			m.Tables[0].Columns[1].Default = big.NewInt(10)
			fields := []schema.ViewField{}
			for _, column := range m.Tables[0].Columns {
				c := column
				fields = append(fields, schema.ViewField{Name: c.Name, Column: &c})
			}
			m.Views = []schema.View{{Name: "account_view", Fields: fields, Query: `SELECT * FROM account`}}
			s, e := New(ctx, b, m, fmt.Sprintf("ponder_go_sql_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			t.Run("all business DML is captured once", func(t *testing.T) {
				e := s.Apply(ctx, event("sql1", 1, 1), func(w *Writer) error {
					if e := w.Write(ctx, "account", Row{"id": "api", "balance": big.NewInt(1)}, Insert); e != nil {
						return e
					}
					if _, e := w.SQL(ctx, `INSERT INTO account (id) VALUES ('raw1'),('raw2')`); e != nil {
						return e
					}
					if _, e := w.SQL(ctx, `UPDATE account SET balance=balance+1 WHERE id LIKE 'raw%'`); e != nil {
						return e
					}
					_, e := w.SQL(ctx, `DELETE FROM account WHERE id='raw2'`)
					return e
				})
				if e != nil {
					t.Fatal(e)
				}
				r, e := b.Query(ctx, "SELECT COUNT(*)::text AS n FROM "+s.internal("_ponder_go_journal"))
				if e != nil || r.Rows[0]["n"] != "6" {
					t.Fatalf("journal %v %v", r, e)
				}
				if got := balance(t, s, "raw1"); got != "11" {
					t.Fatal(got)
				}
				if e = s.Revert(ctx, event("", 1, 0).Checkpoint); e != nil {
					t.Fatal(e)
				}
				if got := balance(t, s, "api"); got != "missing" {
					t.Fatal(got)
				}
				if got := balance(t, s, "raw1"); got != "missing" {
					t.Fatal(got)
				}
			})
			t.Run("direct writes outside event are rejected", func(t *testing.T) {
				if e := b.Exec(ctx, "INSERT INTO "+s.table("account")+" (id,balance) VALUES ('outside',4)"); e == nil {
					t.Fatal("untracked business write allowed")
				}
				if got := balance(t, s, "outside"); got != "missing" {
					t.Fatal(got)
				}
			})
			t.Run("updatable view writes use the base table journal", func(t *testing.T) {
				if e := s.Apply(ctx, event("view-write", 1, 1), func(w *Writer) error {
					_, e := w.SQL(ctx, `INSERT INTO account_view (id,balance) VALUES ('via-view',4)`)
					return e
				}); e != nil {
					t.Fatal(e)
				}
				r, e := b.Query(ctx, "SELECT table_name FROM "+s.internal("_ponder_go_journal"))
				if e != nil || len(r.Rows) != 1 || r.Rows[0]["table_name"] != "account" {
					t.Fatalf("view journal %v %v", r, e)
				}
				if e = s.Revert(ctx, event("", 1, 0).Checkpoint); e != nil {
					t.Fatal(e)
				}
				if got := balance(t, s, "via-view"); got != "missing" {
					t.Fatal(got)
				}
			})
			t.Run("raw error rolls back statement and later queries work", func(t *testing.T) {
				e := s.Apply(ctx, event("sql-error", 1, 2), func(w *Writer) error {
					if _, e := w.SQL(ctx, `INSERT INTO account (id,balance) VALUES ('kept',5)`); e != nil {
						return e
					}
					_, e := w.SQL(ctx, `INSERT INTO account (id,balance) VALUES ('rolled-back',6),('kept',7)`)
					var raw *RawSQLError
					if !errors.As(e, &raw) {
						return fmt.Errorf("wrong error %T %v", e, e)
					}
					r, e := w.SQL(ctx, `SELECT id FROM account ORDER BY id`)
					if e != nil {
						return e
					}
					if len(r.Rows) != 1 || r.Rows[0]["id"] != "kept" {
						return fmt.Errorf("statement leaked rows: %v", r)
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
				if got := balance(t, s, "kept"); got != "5" {
					t.Fatal(got)
				}
			})
			t.Run("raw updates participate in global suffix rollback", func(t *testing.T) {
				apply(t, s, "other-before", 2, 5, Row{"id": "chain2", "balance": big.NewInt(7)}, Insert)
				if e := s.Apply(ctx, event("raw-branch", 1, 3), func(w *Writer) error { _, e := w.SQL(ctx, `UPDATE account SET balance=8 WHERE id='kept'`); return e }); e != nil {
					t.Fatal(e)
				}
				apply(t, s, "other-after", 2, 6, Row{"id": "chain2", "balance": big.NewInt(9)}, Update)
				if e := s.Revert(ctx, event("", 1, 2).Checkpoint); e != nil {
					t.Fatal(e)
				}
				if got := balance(t, s, "kept"); got != "5" {
					t.Fatal(got)
				}
				if got := balance(t, s, "chain2"); got != "7" {
					t.Fatal(got)
				}
			})
			t.Run("DDL internal context and primary key changes are rejected", func(t *testing.T) {
				e := s.Apply(ctx, event("guarded", 1, 4), func(w *Writer) error {
					for _, q := range []string{`TRUNCATE account`, `ALTER TABLE account DISABLE TRIGGER ALL`, `SELECT * INTO account_copy FROM account`, `SELECT * INTO TEMP account_temp FROM account`, `INSERT INTO account (id) VALUES ('first'); DELETE FROM account`, `SELECT set_config('ponder_go.reverting','1',true)`, `DELETE FROM _ponder_go_journal`, `UPDATE account SET id='changed' WHERE id='kept'`} {
						if _, e := w.SQL(ctx, q); e == nil {
							return fmt.Errorf("accepted %s", q)
						}
					}
					_, e := w.SQL(ctx, `SELECT * FROM account`)
					return e
				})
				if e != nil {
					t.Fatal(e)
				}
			})
			t.Run("journal failure rolls back the business statement", func(t *testing.T) {
				if e := b.Exec(ctx, "ALTER TABLE "+s.internal("_ponder_go_journal")+" ADD CONSTRAINT injected_journal_failure CHECK (false) NOT VALID"); e != nil {
					t.Fatal(e)
				}
				e := s.Apply(ctx, event("journal-failure", 1, 5), func(w *Writer) error {
					return w.Write(ctx, "account", Row{"id": "not-committed", "balance": 4}, Insert)
				})
				if e == nil {
					t.Fatal("journal failure ignored")
				}
				if got := balance(t, s, "not-committed"); got != "missing" {
					t.Fatal(got)
				}
				if e = b.Exec(ctx, "ALTER TABLE "+s.internal("_ponder_go_journal")+" DROP CONSTRAINT injected_journal_failure"); e != nil {
					t.Fatal(e)
				}
			})
		})
	}
}

// These two source cases have no omitted inline TS type assertion. The broader
// "sql" case still needs its compile-failure fixture and remains unported.
func TestUpstreamSQLCases(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"sql followed by find", "sql with error"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					var b Backend
					var e error
					if dialect == "postgres" {
						url := os.Getenv("PONDER_TEST_POSTGRES")
						if url == "" {
							t.Skip("explicit task database required; no parity credit")
						}
						b, e = OpenPostgres(ctx, url)
					} else {
						b, e = OpenPGlite(ctx, "memory://")
					}
					if e != nil {
						t.Fatal(e)
					}
					defer b.Close()
					s, e := New(ctx, b, schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}, fmt.Sprintf("ponder_go_sql_case_%x", time.Now().UnixNano()), schema.Multichain)
					if e != nil {
						t.Fatal(e)
					}
					zero := "0x0000000000000000000000000000000000000000"
					e = s.Apply(ctx, event("source-sql", 1, 1), func(w *Writer) error {
						if name == "sql followed by find" {
							if _, e := w.SQL(ctx, `INSERT INTO account (address,balance) VALUES ($1,$2)`, zero, big.NewInt(10)); e != nil {
								return e
							}
							row, e := w.Find(ctx, "account", Row{"address": zero})
							if e != nil {
								return e
							}
							balance, ok := row["balance"].(*big.Int)
							if row["address"] != zero || !ok || balance.Cmp(big.NewInt(10)) != 0 {
								return fmt.Errorf("row does not match original assertion: %v", row)
							}
							return nil
						}
						_, e := w.SQL(ctx, `SELECT * FROM does_not_exist`)
						var raw *RawSQLError
						if !errors.As(e, &raw) {
							return fmt.Errorf("expected RawSqlError, got %T %v", e, e)
						}
						_, e = w.SQL(ctx, `SELECT * FROM account WHERE address=$1`, zero)
						return e
					})
					if e != nil {
						t.Fatal(e)
					}
				})
			}
		})
	}
}

func TestIndexingStoreDurations(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			db, err := New(ctx, sourceBackend(t, ctx, dialect), sourceAccountModel(), fmt.Sprintf("ponder_go_durations_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			registry := metrics.New()
			run := metrics.WithHandlers(ctx, registry)
			key := Row{"address": sourceZeroAddress}
			row := Row{"address": sourceZeroAddress, "balance": big.NewInt(1)}
			err = db.Apply(run, event("metrics", 1, 1), func(w *Writer) error {
				if _, err := w.SQL(ctx, "SELECT 1"); err != nil {
					return err
				}
				if _, err := w.SQL(ctx, "SELECT 1/0"); err == nil {
					return errors.New("division by zero succeeded")
				}
				index := w.Indexing()
				if _, err := index.SQL(ctx, "CREATE TABLE forbidden(id bigint)"); err == nil {
					return errors.New("indexing guard accepted DDL")
				}
				if _, err := index.Find(ctx, "account", key); err != nil {
					return err
				}
				if _, err := index.Find(ctx, "account", key); err != nil {
					return err
				}
				if _, err := index.InsertOne(ctx, "account", row, Conflict{}); err != nil {
					return err
				}
				if _, err := index.SQL(ctx, "SELECT balance FROM account"); err != nil {
					return err
				}
				_, err := index.Update(ctx, "account", key, Row{"balance": big.NewInt(2)})
				return err // the event transaction flushes this update
			})
			if err != nil {
				t.Fatal(err)
			}
			// A failed buffer flush prevents the raw query from starting. An
			// explicitly supplied cache registry owns the measurements in this event.
			other := metrics.New()
			err = db.Apply(run, event("duplicate", 1, 2), func(w *Writer) error {
				index := w.Indexing(CacheOptions{Complete: true, Metrics: other})
				if _, err := index.SQL(ctx, "SELECT 1"); err != nil {
					return err
				}
				if _, err := index.InsertOne(ctx, "account", row, Conflict{}); err != nil {
					return err
				}
				_, err := index.SQL(ctx, "SELECT balance FROM account")
				return err
			})
			if err == nil {
				t.Fatal("duplicate insert should have failed at flush")
			}
			registry.ResetIndexing()
			assertStoreDurationCounts(t, registry, 3, map[string]int{"find": 1, "flush": 2})
			assertStoreDurationCounts(t, other, 1, nil)
		})
	}
}

func assertStoreDurationCounts(t *testing.T, r *metrics.Registry, sql int, cache map[string]int) {
	t.Helper()
	body, err := r.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fmt.Sprintf("ponder_indexing_store_raw_sql_duration_count %d", sql)}
	for op, count := range cache {
		want = append(want, fmt.Sprintf("ponder_indexing_cache_query_duration_count{method=%q,table=\"account\"} %d", op, count))
	}
	var got []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "ponder_indexing_cache_query_duration_count") || strings.HasPrefix(line, "ponder_indexing_store_raw_sql_duration_count") {
			got = append(got, line)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("duration counts %v, want %v", got, want)
	}
}
