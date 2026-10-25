package store

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/schema"
	"strings"
	"testing"
)

func TestORMNullAndFlushRecovery(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			model := sourceAccountModel()
			model.Tables[0].Columns[1].Default = big.NewInt(0)
			sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
				// Source normalization preserves explicit NULL even with a default;
				// the physical NOT NULL constraint then rejects it at flush time.
				row, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": nil}, Conflict{})
				if e != nil {
					return e
				}
				if row["balance"] != nil {
					return fmt.Errorf("explicit NULL replaced by default")
				}
				if e = db.Cache.Flush(ctx); e == nil || !strings.Contains(e.Error(), "not-null") {
					return fmt.Errorf("expected delayed NOT NULL: %v", e)
				}
				// The failed SQL statement and journal were rolled back together.
				r, e := db.writer.tx.Query(ctx, "SELECT * FROM "+db.writer.s.table("account"))
				if e != nil {
					return e
				}
				if len(r.Rows) != 0 {
					return fmt.Errorf("failed flush left rows")
				}
				db.Cache.Clear()
				db.Cache.Invalidate()
				row, e = db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": Undefined}, Conflict{})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 0)
				if e = db.Cache.Flush(ctx); e != nil {
					return e
				}
				db.Cache.Clear()
				row, e = db.Find(ctx, "account", Row{"address": sourceZeroAddress})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 0)
				r, e = db.writer.tx.Query(ctx, "SELECT operation_id FROM "+db.writer.s.internal("_ponder_go_journal"))
				if e != nil {
					return e
				}
				if len(r.Rows) != 1 {
					return fmt.Errorf("recovery journal %v", r.Rows)
				}
				return nil
			})
		})
	}
}

func TestORMRawSQLVisibility(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			model := sourceAccountModel()
			snapshot := model.Tables[0]
			snapshot.Name = "snapshot"
			model.Tables = append(model.Tables, snapshot)
			fields := []schema.ViewField{}
			for _, column := range model.Tables[0].Columns {
				c := column
				fields = append(fields, schema.ViewField{Name: c.Name, Column: &c})
			}
			model.Views = []schema.View{{Name: "account_view", Fields: fields, Query: `SELECT * FROM account`}}
			sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
				insert := func(table string, n int, balance int64) error {
					_, e := db.InsertOne(ctx, table, Row{"address": fmt.Sprintf("0x%040x", n), "balance": big.NewInt(balance)}, Conflict{})
					return e
				}
				if e := insert("account", 0, 10); e != nil {
					return e
				}
				if e := insert("snapshot", 0, 90); e != nil {
					return e
				}
				r, e := db.SQL(ctx, `SELECT * FROM account`)
				if e != nil {
					return e
				}
				if len(r.Rows) != 1 || fmt.Sprint(r.Rows[0]["balance"]) != "10" {
					return fmt.Errorf("buffer not visible to SQL %v", r)
				}
				r, e = db.writer.tx.Query(ctx, "SELECT * FROM "+db.writer.s.table("snapshot"))
				if e != nil {
					return e
				}
				if len(r.Rows) != 0 {
					return fmt.Errorf("unrelated table was flushed by SELECT")
				}
				if _, e = db.SQL(ctx, `UPDATE account SET balance=11`); e != nil {
					return e
				}
				row, e := db.Find(ctx, "account", Row{"address": sourceZeroAddress})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 11)
				row, e = db.Find(ctx, "snapshot", Row{"address": sourceZeroAddress})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 90)
				if e = insert("account", 2, 22); e != nil {
					return e
				}
				if e = insert("snapshot", 2, 22); e != nil {
					return e
				}
				if _, e = db.SQL(ctx, `SELECT * FROM account_view`); e != nil {
					return e
				}
				r, e = db.writer.tx.Query(ctx, "SELECT * FROM "+db.writer.s.table("snapshot"))
				if e != nil {
					return e
				}
				if len(r.Rows) != 2 {
					return fmt.Errorf("view query did not flush all pending tables")
				}
				if _, e = db.SQL(ctx, `SELECT * FROM does_not_exist`); e == nil {
					return fmt.Errorf("expected raw SQL error")
				}
				if e = insert("account", 3, 33); e != nil {
					return e
				}
				if _, e = db.SQL(ctx, `INSERT INTO account (address) VALUES ('bad')`); e == nil {
					return fmt.Errorf("expected raw NULL error")
				}
				row, e = db.Find(ctx, "account", Row{"address": fmt.Sprintf("0x%040x", 3)})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 33)
				return nil
			})
		})
	}
}
