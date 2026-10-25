package store

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
)

func TestUpstreamInsertThen(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			// The source creates no physical account table and never flushes.
			sourceORMTransaction(t, dialect, sourceAccountModel(), false, func(ctx context.Context, db *IndexingStore) error {
				results := []any{}
				if e := db.InsertThen(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, func(row Row) error { results = append(results, row); return nil }); e != nil {
					return e
				}
				if len(results) != 1 {
					t.Fatalf("source results length: %d", len(results))
				}
				row := results[0].(Row)
				if row["address"] != sourceZeroAddress {
					t.Fatal("source result address", row)
				}
				expectSourceBalance(t, row, 10)
				results = results[:0]
				if e := db.InsertManyThen(ctx, "account", []Row{{"address": "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", "balance": big.NewInt(10)}}, func(rows []Row) error { results = append(results, rows); return nil }); e != nil {
					return e
				}
				if len(results) != 1 {
					t.Fatalf("source array result length: %d", len(results))
				}
				return nil
			})
		})
	}
}
func TestUpstreamFullSQL(t *testing.T) {
	// The original returns before its fixture on pglite_test. Do not count a
	// companion execution for an unexecuted original branch.
	t.Run("postgres", func(t *testing.T) {
		sourceORMTransaction(t, "postgres", sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
			if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
				return e
			}
			rows, e := db.Select(ctx, "account", Row{"address": sourceZeroAddress})
			if e != nil {
				return e
			}
			if len(rows) != 1 || rows[0]["address"] != sourceZeroAddress {
				t.Fatalf("source SQL select result: %#v", rows)
			}
			expectSourceBalance(t, rows[0], 10)
			if e = db.writer.tx.Exec(ctx, "SAVEPOINT test"); e != nil {
				return e
			}
			_, e = db.SQLInsert(ctx, "account", Row{"address": "0x0000000000000000000000000000000000000001", "balance": Undefined})
			var raw *RawSQLError
			if !errors.As(e, &raw) {
				return fmt.Errorf("source non-null constraint expected RawSqlError: %w", e)
			}
			if e = db.writer.tx.Exec(ctx, "ROLLBACK TO test"); e != nil {
				return e
			}
			_, e = db.SQLInsert(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)})
			if !errors.As(e, &raw) {
				return fmt.Errorf("source unique constraint expected RawSqlError: %w", e)
			}
			return nil
		})
	})
}
