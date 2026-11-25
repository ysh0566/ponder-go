package graphql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// Count actual data SELECTs, including queries issued by deferred fields.
type selectCounter struct {
	store.Backend
	namespace string
	counts    map[string]int
}
type countedTx struct {
	store.Transaction
	counter *selectCounter
}

func (c *selectCounter) Begin(ctx context.Context) (store.Transaction, error) {
	tx, err := c.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &countedTx{tx, c}, nil
}

func (t *countedTx) Query(ctx context.Context, stmt string, args ...any) (store.Result, error) {
	for table := range t.counter.counts {
		qualified := schema.Quote(t.counter.namespace) + "." + schema.Quote(table)
		if strings.Contains(stmt, qualified) {
			t.counter.counts[table]++
		}
	}
	return t.Transaction.Query(ctx, stmt, args...)
}

func TestRequestScopedCompositeRelations(t *testing.T) {
	parent := schema.Table{Name: "owner", PrimaryKey: []string{"chain", "id"}, Columns: []schema.Column{
		{Name: "chain", Type: "integer", NotNull: true},
		{Name: "id", Type: "text", NotNull: true},
		{Name: "label", Type: "text"},
	}}
	child := schema.Table{Name: "pet", Columns: []schema.Column{
		{Name: "id", Type: "integer", Primary: true},
		{Name: "owner_chain", Property: "ownerChain", Type: "integer"},
		{Name: "owner_id", Property: "ownerId", Type: "text"},
	}}
	fixture := sourceCase{Schema: schema.Schema{Tables: []schema.Table{parent, child}}, Relations: []Relation{
		{Table: "pet", Name: "owner", Target: "owner", Fields: []string{"owner_chain", "owner_id"}, References: []string{"chain", "id"}},
		{Table: "owner", Name: "pets", Target: "pet", Many: true},
	}, Rows: map[string]json.RawMessage{
		"owner": json.RawMessage(`[{"chain":1,"id":"same","label":"first"},{"chain":2,"id":"same","label":"second"}]`),
		"pet":   json.RawMessage(`[{"id":1,"owner_chain":2,"owner_id":"same"},{"id":2,"owner_chain":1,"owner_id":"same"},{"id":3,"owner_chain":2,"owner_id":"same"}]`),
	}}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			db := testBackend(t, dialect)
			s := sourceServer(t, db, fixture, Options{})
			counter := &selectCounter{Backend: db, namespace: s.namespace, counts: map[string]int{"pet": 0, "owner": 0}}
			s.database.Backend = counter
			read := func(second string) {
				t.Helper()
				result := s.Execute(t.Context(), Request{Query: `{pets{items{id owner{label} alias:owner{label}}}}`})
				if result.HasErrors() {
					t.Fatal(result.Errors)
				}
				var want []any
				for i, label := range []string{second, "first", second} {
					value := map[string]any{"label": label}
					want = append(want, map[string]any{"id": float64(i + 1), "owner": value, "alias": value})
				}
				compareJSON(t, result.Data, map[string]any{"pets": map[string]any{"items": want}})
			}
			read("second")
			if err := db.Exec(t.Context(), "UPDATE "+schema.Quote(s.namespace)+".owner SET label=$1 WHERE chain=2", "new"); err != nil {
				t.Fatal(err)
			}
			read("new")
			if counter.counts["pet"] != 2 || counter.counts["owner"] != 2 {
				t.Fatal(counter.counts)
			}
			result := s.Execute(t.Context(), Request{Query: `{owner(chain:2,id:"same"){pets(where:{id_gt:1}){items{id} totalCount}}}`})
			if result.HasErrors() {
				t.Fatal(result.Errors)
			}
			compareJSON(t, result.Data, map[string]any{"owner": map[string]any{"pets": map[string]any{"items": []any{map[string]any{"id": float64(3)}}, "totalCount": float64(1)}}})
		})
	}
}

func TestViewFieldNames(t *testing.T) {
	base := schema.Table{Name: "entry", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}
	view := schema.View{Name: "entry_view", ExportName: "Report", Fields: []schema.ViewField{
		{Name: "entry_key", Property: "entryKey", Column: &schema.Column{Name: "id", Type: "text", NotNull: true}},
		{Name: "row_count", Property: "rowCount", Expression: "count(*)", Aliased: true},
	}, Query: "SELECT id,count(*) FROM entry GROUP BY id"}
	fixture := sourceCase{Schema: schema.Schema{Tables: []schema.Table{base}, Views: []schema.View{view}}, Rows: map[string]json.RawMessage{"entry": json.RawMessage(`[{"id":"one"},{"id":"two"}]`)}}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			s := sourceServer(t, testBackend(t, dialect), fixture, Options{})
			result := s.Execute(t.Context(), Request{Query: `{reports(where:{entryKey:"one"}){items{entryKey rowCount} totalCount pageInfo{hasNextPage hasPreviousPage}}}`})
			if result.HasErrors() {
				t.Fatal(result.Errors)
			}
			compareJSON(t, result.Data, map[string]any{"reports": map[string]any{"items": []any{map[string]any{"entryKey": "one", "rowCount": float64(1)}}, "totalCount": float64(1), "pageInfo": map[string]any{"hasNextPage": false, "hasPreviousPage": false}}})
			for _, query := range []string{`{reports(where:{rowCount:1}){totalCount}}`, `{reports(after:"x"){totalCount}}`, `{reports{pageInfo{endCursor}}}`} {
				if r := s.Execute(t.Context(), Request{Query: query}); !r.HasErrors() {
					t.Error(query, r.Data)
				}
			}
		})
	}
}

func TestGraphQLOptionalRequestContext(t *testing.T) {
	s, err := New(&store.Store{Namespace: "ponder_go_optional"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := s.Execute(nil, Request{Query: `{__typename}`})
	if r.HasErrors() {
		t.Fatal(r.Errors)
	}
	compareJSON(t, r.Data, map[string]any{"__typename": "Query"})
}
