package orm_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/orm/testdata/dbwrites"
	"github.com/ysh0566/ponder-go/store"
)

func TestTypedInsertConflicts(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			db := modelStore(t, backend(t, dialect), "dbwrites")
			kevin := dbwrites.NewTableInsert("kevin")
			updateCalls := 0
			update := func(row dbwrites.TableRow) (dbwrites.TableUpdate, error) {
				updateCalls++
				other := row.Other
				if !other.Valid() {
					other = orm.Some(int64(8))
				}
				return dbwrites.TableUpdate{Other: orm.Set(other)}, nil
			}
			err := db.Apply(ctx, indexed("seed", 1), func(w *store.Writer) error {
				table := dbwrites.Table.Bind(w.Indexing())
				row, err := table.UpsertFunc(ctx, kevin, update)
				if err != nil || row.Other.Valid() || updateCalls != 0 {
					return fmt.Errorf("new row: %+v calls %d: %v", row, updateCalls, err)
				}
				row, err = table.UpsertFunc(ctx, kevin, update)
				if err != nil || !row.Other.Valid() || row.Other.Value() != 8 || updateCalls != 1 {
					return fmt.Errorf("existing row: %+v calls %d: %v", row, updateCalls, err)
				}
				rows, err := table.InsertManyOrIgnore(ctx, []dbwrites.TableInsert{
					kevin, dbwrites.NewTableInsert("two"), dbwrites.NewTableInsert("two"), dbwrites.NewTableInsert("three"),
				})
				if err != nil || len(rows) != 4 || rows[0] != nil || rows[1] == nil || rows[1].Id != "two" || rows[2] != nil || rows[3] == nil || rows[3].Id != "three" {
					return fmt.Errorf("positional batch: %+v: %v", rows, err)
				}
				rows[1].Id = "local"
				if rows[3].Id != "three" {
					return errors.New("distinct batch outputs share a row")
				}
				empty, err := table.InsertManyOrIgnore(ctx, nil)
				if err != nil || empty == nil || len(empty) != 0 {
					return fmt.Errorf("empty batch: %v: %v", empty, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("update stopped")
			err = db.Apply(ctx, indexed("rollback", 2), func(w *store.Writer) error {
				table := dbwrites.Table.Bind(w.Indexing())
				if _, err := table.InsertManyOrIgnore(ctx, []dbwrites.TableInsert{dbwrites.NewTableInsert("rollback")}); err != nil {
					return err
				}
				_, err := table.UpsertFunc(ctx, kevin, func(dbwrites.TableRow) (dbwrites.TableUpdate, error) { return dbwrites.TableUpdate{}, failure })
				return err
			})
			if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			err = db.Apply(ctx, indexed("read", 3), func(w *store.Writer) error {
				table := dbwrites.Table.Bind(w.Indexing())
				for _, key := range []string{"kevin", "two", "three", "rollback"} {
					row, err := table.Find(ctx, dbwrites.NewTableKey(key))
					if err != nil {
						return err
					}
					if key == "rollback" {
						if row != nil {
							return errors.New("failed transaction leaked its insert")
						}
						continue
					}
					if row == nil || row.Id != key || key == "kevin" && (!row.Other.Valid() || row.Other.Value() != 8) {
						return fmt.Errorf("persisted %s: %+v", key, row)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
