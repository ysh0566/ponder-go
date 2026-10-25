package store

import (
	"context"
	"fmt"
	"math/big"
	"testing"
)

// Supplementary real SQL/ORM cache visibility; zero original test credit.
func TestSQLParserCacheInvalidation(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			sourceORMTransaction(t, dialect, sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
				if _, err := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(1)}, Conflict{}); err != nil {
					return err
				}
				warm, err := db.Find(ctx, "account", Row{"address": sourceZeroAddress})
				if err != nil {
					return err
				}
				expectSourceBalance(t, warm, 1)
				// The fixture creates a function only in its new task-owned schema.
				// Its write runs inside the same event transaction and base journal.
				function := db.writer.s.internal("bump_cached_balance")
				definition := "CREATE FUNCTION " + function + "() RETURNS bigint LANGUAGE SQL AS $body$ UPDATE " + db.writer.s.table("account") + " SET balance=balance+1 WHERE address='" + sourceZeroAddress + "' RETURNING balance $body$"
				if err := db.writer.tx.Exec(ctx, definition); err != nil {
					return err
				}
				result, err := db.SQL(ctx, "SELECT (SELECT "+function+"()) AS value")
				if err != nil {
					return err
				}
				if len(result.Rows) != 1 || fmt.Sprint(result.Rows[0]["value"]) != "2" {
					return fmt.Errorf("Actual database nested SQL write: %+v", result)
				}
				fresh, err := db.Find(ctx, "account", Row{"address": sourceZeroAddress})
				if err != nil {
					return err
				}
				if fresh == nil || fresh["balance"].(*big.Int).Cmp(big.NewInt(2)) != 0 {
					return fmt.Errorf("Nested SQL changed the actual database to2 but next ORM Find returned stale row %+v", fresh)
				}
				if _, err = db.Update(ctx, "account", Row{"address": sourceZeroAddress}, Row{"balance": big.NewInt(3)}); err != nil {
					return err
				}
				if err = db.Cache.Flush(ctx); err != nil {
					return err
				}
				physical, err := db.writer.tx.Query(ctx, "SELECT balance::text AS balance FROM "+db.writer.s.table("account"))
				if err != nil || len(physical.Rows) != 1 || physical.Rows[0]["balance"] != "3" {
					return fmt.Errorf("Subsequent ORM update: %+v %v", physical, err)
				}
				journal, err := db.writer.tx.Query(ctx, "SELECT COUNT(*)::text AS count FROM "+db.writer.s.internal("_ponder_go_journal"))
				if err != nil || len(journal.Rows) != 1 || journal.Rows[0]["count"] != "3" {
					return fmt.Errorf("Expected insert, nested SQL update and ORM update once each: %+v %v", journal, err)
				}
				return nil
			})
		})
	}
}
