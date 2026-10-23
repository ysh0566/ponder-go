package store

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
	"testing"
	"time"
)

func TestORMEventAtomicity(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			s, e := New(ctx, b, sourceAccountModel(), fmt.Sprintf("ponder_go_orm_atomic_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			rows := make([]Row, 50)
			for i := range rows {
				rows[i] = Row{"address": fmt.Sprintf("0x%040x", i), "balance": big.NewInt(int64(i))}
			}
			var escaped *IndexingStore
			if e = s.Apply(ctx, event("batch", 1, 1), func(w *Writer) error {
				db := w.Indexing()
				escaped = db
				_, e := db.InsertMany(ctx, "account", rows, Conflict{})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if _, e = escaped.Find(ctx, "account", Row{"address": sourceZeroAddress}); e == nil {
				t.Fatal("event scope remained open")
			}
			check := func(wantRows, wantJournal int, wantLast string) {
				t.Helper()
				r, e := s.Read(ctx, "account")
				if e != nil || len(r.Rows) != wantRows {
					t.Fatalf("rows %v %v", r, e)
				}
				r, e = b.Query(ctx, "SELECT operation_id FROM "+s.internal("_ponder_go_journal"))
				if e != nil || len(r.Rows) != wantJournal {
					t.Fatalf("journal %v %v", r, e)
				}
				p, e := s.Progress(ctx)
				if e != nil || p["1"] != wantLast {
					t.Fatalf("progress %v %v", p, e)
				}
			}
			cp1 := checkpoint.MustEncode(event("", 1, 1).Checkpoint)
			check(50, 50, cp1)
			for _, flush := range []bool{false, true} {
				e = s.Apply(ctx, event("aborted", 1, 2), func(w *Writer) error {
					db := w.Indexing()
					for i := range rows {
						if _, e := db.Update(ctx, "account", Row{"address": fmt.Sprintf("0x%040x", i)}, Row{"balance": big.NewInt(100)}); e != nil {
							return e
						}
					}
					if flush {
						if e := db.Cache.Flush(ctx); e != nil {
							return e
						}
						if _, e := db.SQL(ctx, `DELETE FROM account WHERE address=$1`, sourceZeroAddress); e != nil {
							return e
						}
					}
					return fmt.Errorf("injected failure after buffered/physical writes")
				})
				if e == nil {
					t.Fatal("handler failure ignored")
				}
				check(50, 50, cp1)
			}
			applyUpdate := func() error {
				return s.Apply(ctx, event("replay", 1, 2), func(w *Writer) error {
					db := w.Indexing()
					for i := range rows {
						if _, e := db.UpdateFunc(ctx, "account", Row{"address": fmt.Sprintf("0x%040x", i)}, func(row Row) (Row, error) {
							return Row{"balance": new(big.Int).Add(row["balance"].(*big.Int), big.NewInt(10))}, nil
						}); e != nil {
							return e
						}
					}
					return nil
				})
			}
			if e = applyUpdate(); e != nil {
				t.Fatal(e)
			}
			check(50, 100, checkpoint.MustEncode(event("", 1, 2).Checkpoint))
			if e = s.Revert(ctx, event("", 1, 1).Checkpoint); e != nil {
				t.Fatal(e)
			}
			check(50, 50, cp1)
			if e = applyUpdate(); e != nil {
				t.Fatal(e)
			}
			check(50, 100, checkpoint.MustEncode(event("", 1, 2).Checkpoint))
			if e = s.Apply(ctx, event("inspect", 1, 3), func(w *Writer) error {
				row, e := w.Indexing().Find(ctx, "account", Row{"address": sourceZeroAddress})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 10)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(ctx, event("mixed", 1, 4), func(w *Writer) error {
				db := w.Indexing()
				key := Row{"address": sourceZeroAddress}
				if _, e := db.Update(ctx, "account", key, Row{"balance": big.NewInt(50)}); e != nil {
					return e
				}
				row, e := w.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 50)
				if e = w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(60)}, Update); e != nil {
					return e
				}
				row, e = db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 60)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
