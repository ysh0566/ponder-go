package orm_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/orm/testdata/containers"
	"github.com/ysh0566/ponder-go/orm/testdata/custom"
	"github.com/ysh0566/ponder-go/orm/testdata/dbwrites"
	"github.com/ysh0566/ponder-go/orm/testdata/enums"
	"github.com/ysh0566/ponder-go/store"
)

func TestTypedSelect(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			s := modelStore(t, backend(t, dialect), "dbwrites")
			err := s.Apply(ctx, indexed("page", 1), func(w *store.Writer) error {
				table := dbwrites.Table.Bind(w.Indexing())
				col := dbwrites.TableColumns
				const quoted = "it's a parameter"
				for i, id := range []string{"a", "b", "c", "d", quoted} {
					row := dbwrites.NewTableInsert(id)
					if i < 4 {
						row = row.WithOther(orm.Some(int64(i / 2)))
					}
					if _, err := table.Insert(ctx, row); err != nil {
						return err
					}
				}
				base := table.Query().Where(col.Other.Gte(0), col.Other.Lt(2)).OrderBy(col.Other.Desc())
				first := base.OrderBy(col.Id.Asc()).Limit(2)
				reversed := base.OrderBy(col.Id.Desc()).Limit(2)
				// Reuse a caller-owned slice after building the disjunction.
				alternatives := []orm.Predicate[dbwrites.TableRow]{col.Id.Eq("b"), col.Id.Eq(quoted)}
				selectedIDs := orm.Or(col.Id.Eq("a"), alternatives...)
				alternatives[1] = col.Id.Eq("d")
				sorted := table.Query().OrderBy(col.Id.Asc())
				shared := orm.Or(orm.And(col.Id.Eq("a"), col.Other.Eq(0)), col.Other.IsNull())
				for _, tc := range []struct {
					query orm.Query[dbwrites.TableRow]
					want  []string
				}{
					{sorted.Where(selectedIDs), []string{"a", "b", quoted}},
					{sorted.Where(orm.Or(shared, shared)), []string{"a", quoted}},
					{sorted.Where(orm.Or(col.Other.IsNull(), orm.And(col.Other.Gte(1), col.Id.Ne("c")))), []string{"d", quoted}},
					{sorted.Where(orm.And(selectedIDs, orm.Or(col.Other.Eq(0), col.Other.IsNull()))).Where(col.Id.Ne("b")).Limit(1).Offset(1), []string{quoted}},
					{sorted.Where(orm.Or(col.Other.Gt(0), col.Id.Eq("a"))), []string{"a", "c", "d"}},
					{sorted.Where(orm.Or(orm.And(col.Id.Eq(quoted)))), []string{quoted}},
					{first, []string{"c", "d"}},
					{reversed, []string{"d", "c"}},
					{first.Offset(2), []string{"a", "b"}},
					{first.Offset(6), nil},
					{first.Limit(0), nil},
					{table.Query().Where(col.Other.IsNull(), col.Id.Eq(quoted)), []string{quoted}},
					{table.Query().Where(col.Other.Gt(0), col.Other.Lte(1)).Where(col.Id.Ne("c")), []string{"d"}},
					{table.Query().Where(col.Other.IsNotNull()).OrderBy(col.Id.Asc()).Offset(3), []string{"d"}},
				} {
					got, err := tc.query.All(ctx)
					if err != nil {
						return err
					}
					var ids []string
					for _, row := range got {
						ids = append(ids, row.Id)
					}
					if !slices.Equal(ids, tc.want) {
						return fmt.Errorf("page IDs %v, want %v", ids, tc.want)
					}
				}
				legacy := orm.ColumnOf[string](dbwrites.Table, "id")
				if _, err := table.Query().Where(legacy.Eq(quoted)).All(ctx); err == nil || !strings.Contains(err.Error(), "regenerate the typed model") {
					return fmt.Errorf("missing generated encoder: %v", err)
				}
				for _, invalid := range []orm.Query[dbwrites.TableRow]{first.Limit(-1), first.Offset(-1), first.Where(orm.Predicate[dbwrites.TableRow]{}), first.OrderBy(orm.Order[dbwrites.TableRow]{})} {
					if _, err := invalid.All(ctx); err == nil {
						return fmt.Errorf("invalid query executed")
					}
				}
				alias := orm.Define[dbwrites.TableKey, dbwrites.TableInsert, dbwrites.TableUpdate, dbwrites.TableRow]("other_table", nil, nil, nil, nil)
				foreign := orm.ColumnOf[string](alias, "id")
				if _, err := sorted.Where(orm.Or(col.Id.Eq("a"), foreign.IsNull())).All(ctx); err == nil {
					return fmt.Errorf("group admitted a physical column from another table")
				}
				cycle := make([]store.SelectFilter, 1)
				cycle[0] = store.SelectFilter{Operator: "OR", Value: cycle}
				for _, bad := range []store.SelectOptions{
					{Filters: cycle},
					{Filters: []store.SelectFilter{{Operator: "OR", Value: []store.SelectFilter{}}}},
					{Filters: []store.SelectFilter{{Operator: "AND", Value: []store.SelectFilter{{Column: "missing", Operator: "IS NULL"}}}}},
					{Filters: []store.SelectFilter{{Column: "missing", Operator: "="}}},
					{Filters: []store.SelectFilter{{Column: "id", Operator: "OR"}}},
					{Order: []store.SelectOrder{{Column: "missing"}}},
				} {
					if _, err := w.Indexing().SelectRows(ctx, "table", bad); err == nil {
						return fmt.Errorf("invalid SQL fragment accepted")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Revert(ctx, indexed("", 0).Checkpoint); err != nil {
				t.Fatal(err)
			}
			if err := s.Apply(ctx, indexed("replacement", 1), func(w *store.Writer) error {
				rows, err := dbwrites.Table.Bind(w.Indexing()).Query().All(ctx)
				if err != nil || len(rows) != 0 {
					return fmt.Errorf("rows after fork: %v %v", rows, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTypedSelectCodecs(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			s := modelStore(t, backend(t, dialect), "custom")
			db := indexing.NewSessionDatabase(store.NewIndexingSession(s), new(indexing.ErrorState))
			table := custom.BindCallbackDatabase(db).Holding
			col := custom.HoldingColumns
			var handle context.Context
			var lease *indexing.DatabaseBinding
			payload := custom.Payload{255}
			history := []orm.Nullable[custom.Units]{orm.Some(custom.Units(7)), orm.Null[custom.Units]()}
			query := table.Query().Where(col.Id.Eq(3), col.Payload.Eq(payload), col.History.Eq(history))
			grouped := table.Query().Where(orm.Or(col.Limit.IsNotNull(), orm.And(col.Id.Eq(3), col.Payload.Eq(payload), col.History.Eq(history))))
			payload[0] = 0
			history[0] = orm.Some(custom.Units(0))
			check := func(ctx context.Context) error {
				for _, q := range []orm.Query[custom.HoldingRow]{query, grouped} {
					got, err := q.All(ctx)
					if err != nil || len(got) != 1 || got[0].Id != 3 {
						return fmt.Errorf("encoded query: %+v %v", got, err)
					}
				}
				return nil
			}
			txnErr := s.Apply(ctx, indexed("custom", 1), func(w *store.Writer) error {
				var err error
				handle, lease, err = db.Bind(ctx, w)
				if err != nil {
					return err
				}
				defer lease.Revoke()
				if _, err = table.Query().Where(col.Id.Eq(-2)).All(handle); !errors.Is(err, custom.ErrUnits) {
					return fmt.Errorf("lost codec error: %v", err)
				}
				if _, err = table.Query().Where(orm.Or(col.Limit.IsNull(), orm.And(col.Id.Eq(3), col.Id.Eq(-2)))).All(handle); !errors.Is(err, custom.ErrUnits) {
					return fmt.Errorf("lost codec error: %v", err)
				}
				input := custom.NewHoldingInsert(3, custom.Payload{255}).WithHistory(orm.Some([]orm.Nullable[custom.Units]{orm.Some(custom.Units(7)), orm.Null[custom.Units]()}))
				if _, err = table.Insert(handle, input); err != nil {
					return err
				}
				return check(handle)
			})
			if lease != nil {
				if err := lease.Finish(txnErr); err != nil {
					t.Fatal(err)
				}
			}
			if txnErr != nil {
				t.Fatal(txnErr)
			}
			if _, err := query.All(handle); err == nil {
				t.Fatal("released query binding")
			}
			if _, err := grouped.All(handle); err == nil {
				t.Fatal("grouped query reused a released binding")
			}
			if err := db.WithReadOnly(ctx, 1, check); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTypedSelectJSON(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			s := modelStore(t, backend(t, dialect), "containers")
			if err := s.Apply(ctx, indexed("json", 1), func(w *store.Writer) error {
				table := containers.Packet.Bind(w.Indexing())
				doc := json.RawMessage(`{"number":9007199254740993}`)
				input := containers.NewPacketInsert("p", []byte{0, 42}, doc, []orm.Nullable[int64]{orm.Null[int64](), orm.Some(int64(4))})
				if _, err := table.Insert(ctx, input); err != nil {
					return err
				}
				col := containers.PacketColumns
				rows, err := table.Query().Where(col.Document.Eq(doc), col.Numbers.Eq([]orm.Nullable[int64]{orm.Null[int64](), orm.Some(int64(4))})).All(ctx)
				if err != nil || len(rows) != 1 || rows[0].Id != "p" {
					return fmt.Errorf("JSON and array: %v %v", rows, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTypedSelectEnum(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			s := modelStore(t, backend(t, dialect), "enums")
			err := s.Apply(ctx, indexed("enums", 1), func(w *store.Writer) error {
				table := enums.Trade.Bind(w.Indexing())
				previous := []orm.Nullable[enums.TradeType]{orm.Some(enums.TradeTypeSell()), orm.Null[enums.TradeType]()}
				for _, kind := range []enums.TradeType{enums.TradeTypeBuy(), enums.TradeTypePreSale(), enums.TradeTypeSell()} {
					row := enums.NewTradeInsert(kind, enums.StatusBuy()).WithHistory(orm.Some(previous))
					if _, err := table.Insert(ctx, row); err != nil {
						return err
					}
				}
				col := enums.TradeColumns
				rows, err := table.Query().Where(col.Kind.Gt(enums.TradeTypeBuy()), col.History.Eq(previous)).OrderBy(col.Kind.Desc()).All(ctx)
				if err != nil || len(rows) != 2 || rows[0].Kind != enums.TradeTypePreSale() || rows[1].Kind != enums.TradeTypeSell() {
					return fmt.Errorf("enum storage order: %+v %v", rows, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTypedSelectCaller(t *testing.T) {
	code, err := os.ReadFile("testdata/dbwrites/models.go")
	if err != nil {
		t.Fatal(err)
	}
	compileDBCaller(t, code, `var _ []TableRow = result(table.Query().Where(TableColumns.Other.Gt(3)).OrderBy(TableColumns.Id.Desc()).Limit(2).All(ctx))`)
	compileDBCaller(t, code, `var _ []TableRow = result(table.Query().Where(orm.Or(TableColumns.Id.Eq("x"), orm.And(TableColumns.Other.Gte(3), TableColumns.Other.Lt(7)))).All(ctx))`)
	compileDBCaller(t, code, `type PeerRow struct{}; _ = orm.Or(TableColumns.Id.IsNull(), orm.Predicate[PeerRow]{})`, "does not match inferred type")
	compileDBCaller(t, code, `_ = TableColumns.Other.Eq("bad")`, "cannot use")
	compileDBCaller(t, code, `type PeerRow struct{}; _ = table.Query().Where(orm.Predicate[PeerRow]{})`, "cannot use")
	compileDBCaller(t, code, `type PeerRow struct{}; _ = table.Query().OrderBy(orm.Order[PeerRow]{})`, "cannot use")
}
