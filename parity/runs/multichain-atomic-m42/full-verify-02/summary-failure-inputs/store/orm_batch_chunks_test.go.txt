package store

import (
	"context"
	"fmt"
	"testing"

	"ponder.local/ponder-go/schema"
)

func TestORMBatchChunks(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			model := schema.Schema{Tables: []schema.Table{{Name: "batch", Columns: []schema.Column{{Name: "id", Type: "integer", Primary: true}, {Name: "value", Type: "integer"}}}}}
			sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
				rows := make([]Row, 1001)
				for i := range rows {
					rows[i] = Row{"id": i, "value": i}
				}
				rows[1000]["value"] = int64(1) << 40 // SQL int4 range failure in chunk two.
				if _, e := db.InsertMany(ctx, "batch", rows, Conflict{}); e != nil {
					return e
				}
				if e := db.Cache.Flush(ctx); e == nil {
					return fmt.Errorf("out-of-range second batch committed")
				}
				for _, table := range []string{db.writer.s.table("batch"), db.writer.s.internal("_ponder_go_journal")} {
					r, e := db.writer.tx.Query(ctx, "SELECT COUNT(*)::text AS n FROM "+table)
					if e != nil {
						return e
					}
					if r.Rows[0]["n"] != "0" {
						return fmt.Errorf("first batch escaped flush savepoint: %s %v", table, r)
					}
				}
				row, e := db.Find(ctx, "batch", Row{"id": 0})
				if e != nil || row["value"] != int64(0) {
					return fmt.Errorf("failed flush lost pending rows: %v %v", row, e)
				}
				deleted, e := db.Delete(ctx, "batch", Row{"id": 1000})
				if e != nil || !deleted {
					return fmt.Errorf("failed pending row not removable: %v %v", deleted, e)
				}
				if _, e = db.InsertOne(ctx, "batch", Row{"id": 1000, "value": 1000}, Conflict{}); e != nil {
					return e
				}
				if e = db.Cache.Flush(ctx); e != nil {
					return e
				}
				db.Cache.Clear()
				db.Cache.Invalidate()
				row, e = db.Find(ctx, "batch", Row{"id": 1000})
				if e != nil || row["value"] != int64(1000) {
					return fmt.Errorf("retry value: %v %v", row, e)
				}
				for _, table := range []string{db.writer.s.table("batch"), db.writer.s.internal("_ponder_go_journal")} {
					r, e := db.writer.tx.Query(ctx, "SELECT COUNT(*)::text AS n FROM "+table)
					if e != nil {
						return e
					}
					if r.Rows[0]["n"] != "1001" {
						return fmt.Errorf("retry wrote an extra operation: %s %v", table, r)
					}
				}
				return nil
			})
		})
	}
}
