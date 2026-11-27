package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/metrics"
)

const sourceSelectSchemata = `select "schema_name" from "information_schema"."schemata"`

func checkSchemata(ctx context.Context, db Querier, statement string) error {
	rows, err := db.Query(ctx, statement)
	if err != nil {
		return err
	}
	for _, row := range rows.Rows {
		if row["schema_name"] == "public" {
			return nil
		}
	}
	return fmt.Errorf("missing public schema in %v", rows.Rows)
}

type observedQuery struct {
	Querier
	before func(string) error
}

func (q observedQuery) Query(ctx context.Context, s string, args ...any) (Result, error) {
	if err := q.before(s); err != nil {
		return Result{}, err
	}
	return q.Querier.Query(ctx, s, args...)
}
func (q observedQuery) Exec(ctx context.Context, s string, args ...any) error {
	if err := q.before(s); err != nil {
		return err
	}
	return q.Querier.Exec(ctx, s, args...)
}

type observedTx struct {
	observedQuery
	tx Transaction
}

func (q observedTx) Commit(ctx context.Context) error {
	if err := q.before("COMMIT"); err != nil {
		return err
	}
	return q.tx.Commit(ctx)
}
func (q observedTx) Rollback(ctx context.Context) error {
	if err := q.before("ROLLBACK"); err != nil {
		return err
	}
	return q.tx.Rollback(ctx)
}

// Observe the same BEGIN/statement/rollback/commit boundaries as the upstream
// query spy. Successful operations always reach the actual PostgreSQL session.
func observeQB(q *SourceQueryBuilder, before func(string) error) {
	q.raw = observedQuery{q.raw, before}
	begin := q.begin
	q.begin = func(ctx context.Context) (Transaction, any, func(), error) {
		if err := before("BEGIN"); err != nil {
			return nil, nil, nil, err
		}
		tx, client, release, err := begin(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		return observedTx{observedQuery{tx, before}, tx}, client, release, nil
	}
}
func sourcePostgresQB(t *testing.T) *SourceQueryBuilder {
	t.Helper()
	q, err := NewSourceQueryBuilder(sourceBackend(t, t.Context(), "postgres"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestSourceQueryBuilder(t *testing.T) {
	t.Run("QB query", func(t *testing.T) {
		q := sourcePostgresQB(t)
		for _, tc := range []struct{ label, statement string }{{"", "SELECT * FROM information_schema.schemata"}, {"test", sourceSelectSchemata}} {
			if err := q.Wrap(t.Context(), tc.label, func(db Querier) error { return checkSchemata(t.Context(), db, tc.statement) }); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("QB transaction", func(t *testing.T) {
		q := sourcePostgresQB(t)
		selectSchemas := func(db Querier) error { return checkSchemata(t.Context(), db, sourceSelectSchemata) }
		if err := q.Transaction(t.Context(), "test1", func(tx *SourceQueryBuilder) error { return tx.Wrap(t.Context(), "test2", selectSchemas) }); err != nil {
			t.Fatal(err)
		}
		if err := q.Transaction(t.Context(), "", func(tx *SourceQueryBuilder) error {
			return tx.Transaction(t.Context(), "test3", func(nested *SourceQueryBuilder) error { return nested.Wrap(t.Context(), "test4", selectSchemas) })
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("QB wrap retries error", func(t *testing.T) {
		q := sourcePostgresQB(t)
		calls := 0
		observeQB(q, func(string) error {
			calls++
			if calls == 1 {
				return errors.New("Connection terminated unexpectedly")
			}
			return nil
		})
		err := q.Wrap(t.Context(), "test1", func(db Querier) error { return checkSchemata(t.Context(), db, sourceSelectSchemata) })
		if err != nil || calls != 2 {
			t.Fatal(calls, err)
		}
	})
	t.Run("QB transaction retries error", func(t *testing.T) {
		pool := sourcePostgresQB(t).Client().(*sql.DB)
		conn, err := pool.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q, err := NewSourceConnectionQueryBuilder(conn, nil)
		if err != nil {
			t.Fatal(err)
		}
		var calls []string
		observeQB(q, func(s string) error {
			calls = append(calls, s)
			if len(calls) == 1 || len(calls) == 3 {
				return errors.New("Connection terminated unexpectedly")
			}
			return nil
		})
		err = q.Transaction(t.Context(), "test1", func(tx *SourceQueryBuilder) error {
			if tx.Client() != conn {
				return errors.New("transaction changed its dedicated connection")
			}
			return tx.Wrap(t.Context(), "test2", func(db Querier) error { return checkSchemata(t.Context(), db, sourceSelectSchemata) })
		})
		want := []string{"BEGIN", "BEGIN", sourceSelectSchemata, "ROLLBACK", "BEGIN", sourceSelectSchemata, "COMMIT"}
		if err != nil || !reflect.DeepEqual(calls, want) {
			t.Fatal(calls, err)
		}
	})
	t.Run("QB parses error", func(t *testing.T) {
		q := sourcePostgresQB(t)
		calls := 0
		observeQB(q, func(string) error {
			calls++
			if calls == 1 {
				return errors.New("violates not-null constraint")
			}
			return nil
		})
		err := q.Wrap(t.Context(), "test1", func(db Querier) error { return checkSchemata(t.Context(), db, sourceSelectSchemata) })
		var constraint *NotNullConstraintError
		if calls != 1 || !errors.As(err, &constraint) {
			t.Fatal(calls, err)
		}
	})
	t.Run("QB client", func(t *testing.T) {
		q := sourcePostgresQB(t)
		if _, ok := q.Client().(*sql.DB); !ok || q.Dialect() != "postgres" {
			t.Fatal(q.Client(), q.Dialect())
		}
		if err := q.Transaction(t.Context(), "", func(tx *SourceQueryBuilder) error {
			if _, ok := tx.Client().(*sql.Conn); !ok || tx.Dialect() != "postgres" {
				return fmt.Errorf("transaction client %T on %s", tx.Client(), tx.Dialect())
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSourceQueryBuilderBoundaries(t *testing.T) {
	t.Run("PGlite callback retains the same client", func(t *testing.T) {
		ctx := t.Context()
		b := sourceBackend(t, ctx, "pglite")
		q, err := NewSourceQueryBuilder(b, nil)
		if err != nil {
			t.Fatal(err)
		}
		failure := errors.New("stop callback")
		err = q.Transaction(ctx, "pglite", func(tx *SourceQueryBuilder) error {
			if tx != q || tx.Client() != b || tx.Dialect() != "pglite" {
				t.Fatal("PGlite client identity")
			}
			err := tx.Wrap(ctx, "", func(raw Querier) error { return raw.Exec(ctx, "CREATE TEMP TABLE qb_source_probe (id integer)") })
			if err != nil {
				return err
			}
			return failure
		})
		if err != failure {
			t.Fatal(err)
		}
		rows, err := q.Query(ctx, "SELECT to_regclass('pg_temp.qb_source_probe')::text AS name")
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["name"] != "qb_source_probe" {
			t.Fatal(rows, err)
		}
	})
	t.Run("savepoint rollback leaves the outer transaction usable", func(t *testing.T) {
		ctx := t.Context()
		q := sourcePostgresQB(t)
		var escaped *SourceQueryBuilder
		err := q.Transaction(ctx, "", func(tx *SourceQueryBuilder) error {
			if err := tx.Wrap(ctx, "", func(db Querier) error {
				return db.Exec(ctx, "CREATE TEMP TABLE qb_rows(id integer PRIMARY KEY) ON COMMIT DROP")
			}); err != nil {
				return err
			}
			err := tx.Transaction(ctx, "", func(sp *SourceQueryBuilder) error {
				escaped = sp
				return sp.Wrap(ctx, "", func(db Querier) error {
					if err := db.Exec(ctx, "INSERT INTO qb_rows VALUES(1)"); err != nil {
						return err
					}
					return db.Exec(ctx, "INSERT INTO qb_rows VALUES(1)")
				})
			})
			var unique *UniqueConstraintError
			if !errors.As(err, &unique) {
				return fmt.Errorf("expected unique violation: %w", err)
			}
			rows, err := tx.Query(ctx, "SELECT id FROM qb_rows")
			if err == nil && len(rows.Rows) != 0 {
				return fmt.Errorf("savepoint left %v", rows.Rows)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = escaped.Query(ctx, "SELECT 1")
		if !errors.Is(err, sql.ErrTxDone) {
			t.Fatal(err)
		}
	})
	t.Run("parallel savepoints cannot share the same scope", func(t *testing.T) {
		ctx := t.Context()
		q := sourcePostgresQB(t)
		err := q.Transaction(ctx, "", func(tx *SourceQueryBuilder) error {
			start, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- tx.Transaction(ctx, "", func(*SourceQueryBuilder) error { close(start); <-release; return nil })
			}()
			<-start
			overlap := tx.Transaction(ctx, "", func(*SourceQueryBuilder) error { return errors.New("unexpected nested transaction") })
			close(release)
			if err := <-done; err != nil {
				return err
			}
			if !isNonRetryableDBError(overlap) {
				return fmt.Errorf("overlap classification: %v", overlap)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("failed savepoint cleanup prevents an outer commit", func(t *testing.T) {
		ctx := t.Context()
		conn, err := sourcePostgresQB(t).Client().(*sql.DB).Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err = conn.ExecContext(ctx, "CREATE TEMP TABLE qb_rollback(id integer)"); err != nil {
			t.Fatal(err)
		}
		q, err := NewSourceConnectionQueryBuilder(conn, nil)
		if err != nil {
			t.Fatal(err)
		}
		failed := &NonRetryableUserError{Message: "savepoint rollback unavailable"}
		observeQB(q, func(s string) error {
			if s == "ROLLBACK TO SAVEPOINT ponder_qb_1" {
				return failed
			}
			return nil
		})
		err = q.Transaction(ctx, "", func(tx *SourceQueryBuilder) error {
			_ = tx.Transaction(ctx, "", func(nested *SourceQueryBuilder) error {
				if err := nested.Wrap(ctx, "", func(db Querier) error { return db.Exec(ctx, "INSERT INTO qb_rollback VALUES(1)") }); err != nil {
					return err
				}
				return &NonRetryableUserError{Message: "abort nested work"}
			})
			// The caller deliberately handles the inner error.
			return nil
		})
		if !errors.Is(err, failed) {
			t.Fatalf("outer transaction: %v", err)
		}
		rows, err := q.Query(ctx, "SELECT id FROM qb_rollback")
		if err != nil || len(rows.Rows) != 0 {
			t.Fatal(rows, err)
		}
	})
	t.Run("retry only the rolled back savepoint", func(t *testing.T) {
		ctx := t.Context()
		q := sourcePostgresQB(t)
		outer, inner := 0, 0
		err := q.Transaction(ctx, "", func(tx *SourceQueryBuilder) error {
			outer++
			return tx.Transaction(ctx, "", func(nested *SourceQueryBuilder) error {
				inner++
				if inner == 1 {
					return errors.New("Connection terminated unexpectedly")
				}
				return nested.Wrap(ctx, "", func(db Querier) error { return checkSchemata(ctx, db, sourceSelectSchemata) })
			})
		})
		if err != nil || outer != 1 || inner != 2 {
			t.Fatal(outer, inner, err)
		}
	})
	t.Run("cancel interrupts backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		q := sourcePostgresQB(t)
		n := 0
		q.observe = func(a SourceQueryAttempt) {
			if a.Label == "cancel" && a.Number == 1 && a.Err != nil {
				cancel()
			}
		}
		err := q.Wrap(ctx, "cancel", func(Querier) error { n++; return errors.New("ETIMEDOUT") })
		if !errors.Is(err, context.Canceled) || n != 1 {
			t.Fatal(n, err)
		}
	})
	t.Run("commit acknowledgement loss does not replay writes", func(t *testing.T) {
		ctx := t.Context()
		pool := sourcePostgresQB(t).Client().(*sql.DB)
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q, err := NewSourceConnectionQueryBuilder(conn, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE qb_commit (id integer)"); err != nil {
			t.Fatal(err)
		}
		begin := q.begin
		var callbacks int
		q.begin = func(ctx context.Context) (Transaction, any, func(), error) {
			tx, c, release, e := begin(ctx)
			if e != nil {
				return nil, nil, nil, e
			}
			return &lostCommit{tx}, c, release, nil
		}
		err = q.Transaction(ctx, "", func(tx *SourceQueryBuilder) error {
			callbacks++
			return tx.Wrap(ctx, "", func(db Querier) error { return db.Exec(ctx, "INSERT INTO qb_commit VALUES (1)") })
		})
		if !errors.Is(err, ErrIndeterminateCommit) || callbacks != 1 {
			t.Fatal(callbacks, err)
		}
		rows, err := q.Query(ctx, "SELECT id FROM qb_commit")
		if err != nil || len(rows.Rows) != 1 {
			t.Fatal(rows, err)
		}
	})
}

type lostCommit struct{ Transaction }

func (tx *lostCommit) Commit(ctx context.Context) error {
	if err := tx.Transaction.Commit(ctx); err != nil {
		return err
	}
	return errors.New("Connection terminated unexpectedly")
}

func TestDatabaseQueryAttempts(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			r := metrics.New()
			ctx := metrics.WithDatabase(t.Context(), r)
			b := sourceBackend(t, t.Context(), dialect)
			var attempts []SourceQueryAttempt
			q, err := NewSourceQueryBuilder(b, func(a SourceQueryAttempt) { attempts = append(attempts, a) })
			if err != nil {
				t.Fatal(err)
			}
			err = q.Wrap(ctx, "probe", func(db Querier) error {
				if len(attempts) == 0 {
					_, err := db.Query(ctx, "SELECT 1 / 0")
					return err
				}
				return db.Exec(ctx, "SELECT 42")
			})
			if err != nil || len(attempts) != 2 || attempts[0].Err == nil || attempts[1].Err != nil {
				t.Fatalf("attempts=%+v err=%v", attempts, err)
			}
			queryInTx := func(tx *SourceQueryBuilder) error {
				_, err := tx.Query(ctx, "SELECT 1")
				return err
			}
			if err := q.Transaction(ctx, "", queryInTx); err != nil {
				t.Fatal(err)
			}
			if dialect == "postgres" {
				conn, err := q.Client().(*sql.DB).Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				dedicated, err := NewSourceConnectionQueryBuilder(conn, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := dedicated.Query(ctx, "SELECT 1"); err != nil {
					t.Fatal(err)
				}
				if err := dedicated.Transaction(ctx, "", queryInTx); err != nil {
					t.Fatal(err)
				}
			}
			body, err := r.Collect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, sample := range []string{
				`ponder_database_method_duration_count{method="probe"} 2`,
				`ponder_database_method_error_total{method="probe"} 1`,
			} {
				if !strings.Contains(body, sample+"\n") {
					t.Fatalf("missing %s in %s", sample, body)
				}
			}
			if dialect == "postgres" {
				for _, sample := range []string{`ponder_postgres_query_total{pool="default"} 3`, `ponder_postgres_query_total{pool="dedicated"} 2`} {
					if !strings.Contains(body, sample+"\n") {
						t.Fatalf("missing %s in %s", sample, body)
					}
				}
			} else if strings.Contains(body, "ponder_postgres_query_total") {
				t.Fatal(body)
			}
		})
	}
}
