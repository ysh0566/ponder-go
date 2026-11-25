package graphql

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

func TestWriterGraphQL(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			db := testBackend(t, dialect)
			ctx := t.Context()
			name := fmt.Sprintf("ponder_go_graph_%x", time.Now().UnixNano())
			table := schema.Table{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "amount", Type: "bigint"}}}
			st, err := store.New(ctx, db, schema.Schema{Tables: []schema.Table{table}}, name, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, ns := range []string{st.SyncNamespace, name} {
					if e := db.Exec(context.Background(), "DROP SCHEMA "+schema.Quote(ns)+" CASCADE"); e != nil {
						t.Error(e)
					}
				}
			})
			if err := db.Exec(ctx, "INSERT INTO "+schema.Quote(name)+"._ponder_go_chains VALUES (1,'mainnet')"); err != nil {
				t.Fatal(err)
			}
			id := "alice's wallet"
			amount := "900719925474099312345678901234567890"
			cp := checkpoint.Checkpoint{ChainID: 1, BlockNumber: 10, BlockTimestamp: 20}
			err = st.Apply(ctx, store.Event{ID: "deposit", Name: "Deposit", Checkpoint: cp}, func(w *store.Writer) error {
				return w.Write(ctx, "account", store.Row{"id": id, "amount": amount}, store.Insert)
			})
			if err != nil {
				t.Fatal(err)
			}
			s, err := New(st, Options{})
			if err != nil {
				t.Fatal(err)
			}
			req := Request{Query: `query Ignored { __typename } query Balance($key:String!) { owner:account(id:$key) { ...Fields } _meta {status} } fragment Fields on account { amount }`, OperationName: "Balance", Variables: map[string]any{"key": id}}
			r := s.Execute(ctx, req)
			if r.HasErrors() {
				t.Fatal(r.Errors)
			}
			compareJSON(t, r.Data, map[string]any{"owner": map[string]any{"amount": amount}, "_meta": map[string]any{"status": map[string]any{"mainnet": map[string]any{"id": float64(1), "block": map[string]any{"number": float64(10), "timestamp": float64(20)}}}}})
			req.Variables["key"] = "missing"
			r = s.Execute(ctx, req)
			if r.HasErrors() || r.Data.(map[string]any)["owner"] != nil {
				t.Fatal(r)
			}
			s, err = New(st, Options{MaxOperationAliases: 1})
			if err != nil {
				t.Fatal(err)
			}
			r = s.Execute(ctx, Request{Query: `{account(id:"a"){...F ...F}} fragment F on account { a:amount }`})
			if len(r.Errors) != 1 || !strings.Contains(r.Errors[0].Message, "found 2") {
				t.Fatal(r)
			}
		})
	}
}

func TestGraphQLEnumKeys(t *testing.T) {
	model := schema.Schema{
		Enums:  []schema.Enum{{Name: "mood", Values: []string{"A", "B"}}},
		Tables: []schema.Table{{Name: "a", Columns: []schema.Column{{Name: "m", Type: "mood", Primary: true}}}, {Name: "b", PrimaryKey: []string{"m", "n"}, Columns: []schema.Column{{Name: "m", Type: "mood", NotNull: true}, {Name: "n", Type: "text", NotNull: true}}}},
	}
	s, err := New(&store.Store{Namespace: "ponder_go_enum", Schema: model}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	fields := s.schema.QueryType().Fields()
	for field, expected := range map[string]string{"a": "mood!", "b": "String!"} {
		found := false
		for _, arg := range fields[field].Args {
			if arg.Name() == "m" {
				found = true
				if arg.Type.String() != expected {
					t.Fatal(field, arg.Type, expected)
				}
			}
		}
		if !found {
			t.Fatal("missing key", field)
		}
	}
}
