package store

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/schema"
	"testing"
	"time"
)

func TestSQLWriteBoundaries(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			m := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint"}}}}}
			fields := []schema.ViewField{}
			for _, column := range m.Tables[0].Columns {
				c := column
				fields = append(fields, schema.ViewField{Name: c.Name, Column: &c})
			}
			m.Views = []schema.View{{Name: "account_view", Fields: fields, Query: `SELECT * FROM account`}}
			s, e := New(ctx, b, m, fmt.Sprintf("ponder_go_sql_bounds_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(ctx, event("cte", 1, 1), func(w *Writer) error {
				r, e := w.SQL(ctx, `WITH inserted AS (INSERT INTO account (id,balance) VALUES ('cte',10) RETURNING id) SELECT id FROM inserted`)
				if e != nil {
					return e
				}
				if len(r.Rows) != 1 || r.Rows[0]["id"] != "cte" {
					return fmt.Errorf("CTE result %v", r)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(ctx, event("view-delete", 1, 2), func(w *Writer) error { _, e := w.SQL(ctx, `DELETE FROM account_view WHERE id='cte'`); return e }); e != nil {
				t.Fatal(e)
			}
			if got := balance(t, s, "cte"); got != "missing" {
				t.Fatal(got)
			}
			r, e := b.Query(ctx, "SELECT table_name FROM "+s.internal("_ponder_go_journal")+" ORDER BY operation_id")
			if e != nil || len(r.Rows) != 2 || r.Rows[0]["table_name"] != "account" || r.Rows[1]["table_name"] != "account" {
				t.Fatalf("CTE/view journal %v %v", r, e)
			}
			if e = s.Revert(ctx, event("", 1, 1).Checkpoint); e != nil {
				t.Fatal(e)
			}
			if got := balance(t, s, "cte"); got != "10" {
				t.Fatal("deleted row not restored", got)
			}
			if e = s.Finalize(ctx, event("", 1, 1).Checkpoint); e != nil {
				t.Fatal(e)
			}
			r, e = b.Query(ctx, "SELECT operation_id FROM "+s.internal("_ponder_go_journal"))
			if e != nil || len(r.Rows) != 0 {
				t.Fatalf("finalize did not prune undo prefix %v %v", r, e)
			}
			if e = s.Revert(ctx, event("", 1, 0).Checkpoint); e == nil {
				t.Fatal("rollback crossed finalized safe checkpoint")
			}
			if got := balance(t, s, "cte"); got != "10" {
				t.Fatal("finalized state changed", got)
			}
		})
	}
}
