package orm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/orm/testdata/containers"
	"github.com/ysh0566/ponder-go/orm/testdata/custom"
	"github.com/ysh0566/ponder-go/orm/testdata/dbwrites"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// A custom executor implementing the original eager join API.
type plainJoin struct {
	orm.JoinExecutor
	orm.Executor
}

func TestTypedJoins(t *testing.T) {
	var model schema.Schema
	for _, fixture := range []string{"dbwrites", "containers"} {
		data, err := os.ReadFile("testdata/" + fixture + "/model.json")
		if err != nil {
			t.Fatal(err)
		}
		var part schema.Schema
		if err := json.Unmarshal(data, &part); err != nil {
			t.Fatal(err)
		}
		model.Tables = append(model.Tables, part.Tables...)
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			s, err := store.New(ctx, backend(t, dialect), model, fmt.Sprintf("ponder_go_join_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			on := orm.Equal(dbwrites.TableColumns.Id, containers.PacketColumns.Id)
			doc := json.RawMessage(`{"n":9007199254740993}`)
			write := func(w *store.Writer) error {
				db := w.Indexing()
				records, packets := dbwrites.Table.Bind(db), containers.Packet.Bind(db)
				if _, err := records.InsertMany(ctx, []dbwrites.TableInsert{dbwrites.NewTableInsert("match"), dbwrites.NewTableInsert("left-only")}); err != nil {
					return err
				}
				input := containers.NewPacketInsert("match", []byte{0, 255}, doc, []orm.Nullable[int64]{orm.Null[int64](), orm.Some(int64(9))})
				if _, err := packets.Insert(ctx, input); err != nil {
					return err
				}
				inner, err := orm.InnerJoin(ctx, db, on)
				if err != nil || len(inner) != 1 {
					return fmt.Errorf("buffered inner join: %+v %v", inner, err)
				}
				p := inner[0].Right
				if inner[0].Left.Id != "match" || p.Id != "match" || !bytes.Equal(p.Payload, []byte{0, 255}) || !bytes.Equal(p.Document, doc) || len(p.Numbers) != 2 || p.Numbers[0].Valid() || p.Numbers[1].Value() != 9 {
					return fmt.Errorf("typed result: %+v", inner)
				}
				joined, err := orm.LeftJoin(ctx, records, on)
				if err != nil || len(joined) != 2 {
					return fmt.Errorf("outer join: %+v %v", joined, err)
				}
				for _, pair := range joined {
					if (pair.Right == nil) != (pair.Left.Id == "left-only") {
						return fmt.Errorf("outer join match: %+v", pair)
					}
				}
				q := orm.QueryLeftJoin(records, on)
				leftID := orm.JoinColumn(q.Left, dbwrites.TableColumns.Id)
				rightID := orm.JoinColumn(q.Right, containers.PacketColumns.Id)
				payload := orm.JoinColumn(q.Right, containers.PacketColumns.Payload)
				missing, err := q.Where(rightID.IsNull()).All(ctx)
				if err != nil || len(missing) != 1 || missing[0].Left.Id != "left-only" || missing[0].Right != nil {
					return fmt.Errorf("left join WHERE right key IS NULL: %+v %v", missing, err)
				}
				base := q.Where(orm.Or(payload.Eq([]byte{0, 255}), leftID.Eq("left-only"))).OrderBy(rightID.Asc(), leftID.Desc())
				page, err := base.Offset(1).Limit(1).All(ctx)
				if err != nil || len(page) != 1 || page[0].Left.Id != "left-only" || page[0].Right != nil {
					return fmt.Errorf("second page: %+v %v", page, err)
				}
				first, err := base.Limit(1).All(ctx)
				if err != nil || len(first) != 1 || first[0].Right == nil || first[0].Right.Id != "match" {
					return fmt.Errorf("first page: %+v %v", first, err)
				}
				empty, err := base.Limit(0).All(ctx)
				if err != nil || len(empty) != 0 {
					return fmt.Errorf("zero limit: %+v %v", empty, err)
				}
				if _, err := base.Offset(-1).All(ctx); err == nil {
					return fmt.Errorf("accepted negative join offset")
				}
				j := orm.QueryInnerJoin(db, on)
				document := orm.JoinColumn(j.Right, containers.PacketColumns.Document)
				result, err := j.Where(document.Eq(doc)).All(ctx)
				if err != nil || len(result) != 1 || result[0].Right.Id != "match" {
					return fmt.Errorf("JSONB join filter: %+v %v", result, err)
				}
				legacy := plainJoin{db, db}
				basic := orm.QueryInnerJoin(legacy, on)
				all, err := basic.All(ctx)
				if err != nil || len(all) != 1 {
					return fmt.Errorf("original executor: %+v %v", all, err)
				}
				legacyTable := dbwrites.Table.BindExecutor(legacy)
				raw, err := legacyTable.JoinRows(ctx, store.InnerJoin, "table", "packet", []store.JoinColumns{{Left: "id", Right: "id"}}, store.SelectOptions{})
				if err != nil || len(raw) != 1 {
					return fmt.Errorf("bound original executor: %+v %v", raw, err)
				}
				if _, err := basic.Limit(1).All(ctx); err == nil {
					return fmt.Errorf("unsupported executor lost limit")
				}
				if _, err := orm.QueryInnerJoin(packets, on).Limit(1).All(ctx); err == nil {
					return fmt.Errorf("wrong bound left table accepted")
				}
				for _, invalid := range []string{"id", "x.id", "left.missing", "right.id.extra"} {
					options := store.SelectOptions{Order: []store.SelectOrder{{Column: invalid}}}
					if _, err := db.JoinRows(ctx, store.InnerJoin, "table", "packet", []store.JoinColumns{{Left: "id", Right: "id"}}, options); err == nil {
						return fmt.Errorf("accepted join order on %q", invalid)
					}
				}
				return nil
			}
			abort := errors.New("stop callback")
			if err := s.Apply(ctx, indexed("discard", 1), func(w *store.Writer) error {
				if err := write(w); err != nil {
					return err
				}
				return abort
			}); !errors.Is(err, abort) {
				t.Fatal("callback rollback:", err)
			}
			if err := s.Apply(ctx, indexed("committed", 1), write); err != nil {
				t.Fatal(err)
			}
			if err := s.Revert(ctx, indexed("", 0).Checkpoint); err != nil {
				t.Fatal(err)
			}
			if err := s.Apply(ctx, indexed("replacement", 1), func(w *store.Writer) error {
				pairs, err := orm.LeftJoin(ctx, w.Indexing(), on)
				if err != nil || len(pairs) != 0 {
					return fmt.Errorf("rows after reorg: %+v %v", pairs, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTypedCallbackSelfJoin(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			s := modelStore(t, backend(t, dialect), "custom")
			on := orm.Equal(custom.HoldingColumns.Limit, custom.HoldingColumns.Id)
			samePayload := orm.Equal(custom.HoldingColumns.Payload, custom.HoldingColumns.Payload)
			db := indexing.NewSessionDatabase(store.NewIndexingSession(s), new(indexing.ErrorState))
			bound := custom.BindCallbackDatabase(db)
			q := orm.QueryLeftJoin(bound.Holding, on)
			child := orm.JoinColumn(q.Left, custom.HoldingColumns.Id)
			parent := orm.JoinColumn(q.Right, custom.HoldingColumns.Id)
			selected := q.Where(orm.Or(child.Eq(3), parent.IsNull())).OrderBy(parent.Asc(), child.Desc())
			check := func(handle context.Context) error {
				rows, err := selected.All(handle)
				if err != nil || len(rows) != 2 || rows[0].Left.Id != 3 || rows[0].Right == nil || rows[0].Right.Id != 2 || rows[1].Left.Id != 2 || rows[1].Right != nil {
					return fmt.Errorf("aliased self join: %+v %v", rows, err)
				}
				return nil
			}
			var binding *indexing.DatabaseBinding
			var retained context.Context
			txnErr := s.Apply(ctx, indexed("join", 1), func(w *store.Writer) error {
				handle, lease, err := db.Bind(ctx, w)
				if err != nil {
					return err
				}
				binding, retained = lease, handle
				defer lease.Revoke()
				rows := []custom.HoldingInsert{
					custom.NewHoldingInsert(1, custom.Payload{1}).WithLimit(orm.Some(custom.Units(2))),
					custom.NewHoldingInsert(2, custom.Payload{1}),
					custom.NewHoldingInsert(3, custom.Payload{3}).WithLimit(orm.Some(custom.Units(2))),
				}
				if _, err := bound.Holding.InsertMany(handle, rows); err != nil {
					return err
				}
				all, err := orm.InnerJoin(handle, db, on)
				if err != nil || len(all) != 2 {
					return fmt.Errorf("self join: %+v %v", all, err)
				}
				inverse := orm.Equal(custom.HoldingColumns.Id, custom.HoldingColumns.Limit)
				children, err := orm.InnerJoin(handle, bound.Holding, inverse)
				if err != nil || len(children) != 2 || children[0].Left.Id != 2 || children[1].Left.Id != 2 || children[0].Right.Id == children[1].Right.Id {
					return fmt.Errorf("one-to-many join: %+v %v", children, err)
				}
				filtered, err := orm.LeftJoin(handle, bound.Holding, on, samePayload)
				if err != nil || len(filtered) != 3 {
					return fmt.Errorf("composite self join: %+v %v", filtered, err)
				}
				for _, pair := range filtered {
					if pair.Left.Id == 1 {
						if pair.Right == nil || pair.Right.Id != 2 || pair.Right.Limit.Valid() {
							return fmt.Errorf("matched pair: %+v", pair)
						}
					} else if pair.Right != nil {
						return fmt.Errorf("unexpected match: %+v", pair)
					}
				}
				if _, err := q.Where(parent.Eq(-1)).All(handle); !errors.Is(err, custom.ErrUnits) {
					return fmt.Errorf("right codec error: %v", err)
				}
				return check(handle)
			})
			if binding != nil {
				if err := binding.Finish(txnErr); err != nil {
					t.Fatal(err)
				}
			}
			if txnErr != nil {
				t.Fatal(txnErr)
			}
			if _, err := orm.InnerJoin(retained, bound.Holding, on); err == nil {
				t.Fatal("join reused a closed callback")
			}
			if _, err := selected.All(retained); err == nil {
				t.Fatal("filtered join reused a closed callback")
			}
			err := db.WithReadOnly(ctx, 1, func(read context.Context) error {
				got, err := orm.InnerJoin(read, bound.Holding, on, samePayload)
				if err != nil || len(got) != 1 || got[0].Right.Id != 2 {
					return fmt.Errorf("read-only join: %+v %v", got, err)
				}
				return check(read)
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTypedJoinCaller(t *testing.T) {
	code, err := os.ReadFile("testdata/dbwrites/models.go")
	if err != nil {
		t.Fatal(err)
	}
	compileDBCaller(t, code, `var rows []orm.Joined[TableRow,*TableRow] = result(orm.LeftJoin(ctx, db, orm.Equal(TableColumns.Id,TableColumns.Id))); _ = rows`)
	compileDBCaller(t, code, `q:=orm.QueryLeftJoin(db,orm.Equal(TableColumns.Id,TableColumns.Id)); right:=orm.JoinColumn(q.Right,TableColumns.Id); var rows []orm.Joined[TableRow,*TableRow]=result(q.Where(right.IsNull()).OrderBy(right.Desc()).Limit(2).All(ctx)); _=rows`)
	compileDBCaller(t, code, `type Peer struct{}; q:=orm.QueryInnerJoin(db,orm.Equal(TableColumns.Id,TableColumns.Id)); _=orm.JoinColumn(q.Right,orm.Column[Peer,string]{})`, "does not match inferred type")
	compileDBCaller(t, code, `_ = orm.Equal(TableColumns.Other,TableColumns.Id)`, "does not match inferred type")
	compileDBCaller(t, code, `_ = TableColumns.Unknown`, "Unknown undefined")
	compileDBCaller(t, code, `var rows []orm.Joined[TableRow,TableRow] = result(orm.LeftJoin(ctx, db, orm.Equal(TableColumns.Id,TableColumns.Id))); _ = rows`, "cannot use")
}

func TestIsolatedTypedJoin(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			account := schema.Table{Name: "table", PrimaryKey: []string{"chain_id", "id"}, Columns: []schema.Column{
				{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "id", Type: "text", NotNull: true}, {Name: "other", Type: "integer"},
			}}
			s, err := store.New(ctx, backend(t, dialect), schema.Schema{Tables: []schema.Table{account}}, fmt.Sprintf("ponder_go_isojoin_%x", time.Now().UnixNano()), schema.Isolated)
			if err != nil {
				t.Fatal(err)
			}
			on := orm.Equal(dbwrites.TableColumns.Id, dbwrites.TableColumns.Id)
			for _, chain := range []uint64{1, 10} {
				ev := indexed(fmt.Sprint(chain), 1)
				ev.Checkpoint.ChainID = chain
				if err := s.Apply(ctx, ev, func(w *store.Writer) error {
					db := w.Indexing()
					if _, err := db.InsertOne(ctx, "table", store.Row{"chainId": int64(chain), "id": "shared", "other": int64(chain)}, store.Conflict{}); err != nil {
						return err
					}
					col := dbwrites.TableColumns
					match := col.Id.Eq("shared")
					for _, filter := range []orm.Predicate[dbwrites.TableRow]{match, orm.Or(match, col.Other.Eq(1))} {
						page, err := dbwrites.Table.Bind(db).Query().Where(filter).OrderBy(col.Other.Asc()).Limit(1).All(ctx)
						if err != nil || len(page) != 1 || page[0].Other.Value() != int64(chain) {
							return fmt.Errorf("chain %d selected rows %+v: %v", chain, page, err)
						}
					}
					joined, err := orm.LeftJoin(ctx, db, on)
					if err != nil || len(joined) != 1 || joined[0].Right == nil || joined[0].Left.Other.Value() != int64(chain) || joined[0].Right.Other.Value() != int64(chain) {
						return fmt.Errorf("chain %d joined rows %+v: %v", chain, joined, err)
					}
					q := orm.QueryLeftJoin(db, on)
					l := orm.JoinColumn(q.Left, col.Other)
					r := orm.JoinColumn(q.Right, col.Other)
					page, err := q.Where(orm.Or(l.Eq(int64(chain)), r.Eq(1))).OrderBy(r.Asc()).Limit(1).All(ctx)
					if err != nil || len(page) != 1 || page[0].Right == nil || page[0].Left.Other.Value() != int64(chain) || page[0].Right.Other.Value() != int64(chain) {
						return fmt.Errorf("isolated join page on chain %d: %+v %v", chain, page, err)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGeneratedJoinColumns(t *testing.T) {
	model := schema.Schema{
		Enums: []schema.Enum{{Name: "table_columns", Values: []string{"normal"}}},
		Tables: []schema.Table{{Name: "table", Columns: []schema.Column{
			{Name: "record_id", Property: "id", Type: "text", Primary: true},
			{Name: "kind", Type: "table_columns"},
		}}},
	}
	code, err := orm.GenerateModels("dbwrites", model)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), `orm.ColumnOf[string](Table, "record_id")`) {
		t.Fatalf("missing physical join column: %s", code)
	}
	compileDBCaller(t, code, `var original TableColumns = TableColumnsNormal(); _ = original; _ = orm.Equal(TableColumns2.Id,TableColumns2.Id)`)
}
