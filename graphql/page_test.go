package graphql

import (
	"encoding/json"
	"strings"
	"testing"

	"ponder.local/ponder-go/schema"
)

func TestTablePageVariables(t *testing.T) {
	table := schema.Table{Name: "account", Columns: []schema.Column{
		{Name: "id", Type: "text", Primary: true},
		{Name: "amount", Type: "bigint"},
		{Name: "memo", Type: "text"},
	}}
	fixture := sourceCase{Schema: schema.Schema{Tables: []schema.Table{table}}, Rows: map[string]json.RawMessage{
		"account": json.RawMessage(`[{"id":"alice's wallet","amount":"900719925474099312345678901234567890","memo":null},{"id":"bob","amount":"9","memo":"Hello Bob"},{"id":"carl","amount":"9","memo":"Hello Carl"}]`),
	}}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			s := sourceServer(t, testBackend(t, dialect), fixture, Options{})
			query := `query List($w:accountFilter,$offset:Int){accounts(where:$w,offset:$offset,orderBy:"amount",orderDirection:"desc"){items{id amount} ...Count pageInfo{startCursor endCursor hasPreviousPage hasNextPage}}} fragment Count on accountPage { n:totalCount }`
			read := func(where map[string]any, offset int) map[string]any {
				t.Helper()
				result := s.Execute(t.Context(), Request{Query: query, Variables: map[string]any{"w": where, "offset": offset}})
				if result.HasErrors() {
					t.Fatal(result.Errors)
				}
				return result.Data.(map[string]any)["accounts"].(map[string]any)
			}
			page := read(map[string]any{"AND": []any{map[string]any{"memo": nil}, map[string]any{"id_in": []any{"alice's wallet", "bob"}}}}, 0)
			compareJSON(t, page["items"], []any{map[string]any{"id": "alice's wallet", "amount": "900719925474099312345678901234567890"}})
			compareJSON(t, page["n"], float64(1))
			page = read(map[string]any{"memo_contains_nocase": "HELLO"}, 1)
			compareJSON(t, page["pageInfo"], map[string]any{"startCursor": nil, "endCursor": nil, "hasPreviousPage": true, "hasNextPage": false})
			compareJSON(t, page["items"], []any{map[string]any{"id": "bob", "amount": "9"}})
			compareJSON(t, page["n"], float64(2))
			for _, tc := range []struct{ args, err string }{
				{`after:"bad",before:"bad"`, "Cannot specify both before and after cursors."},
				{`offset:1,before:"bad"`, "Cannot specify both before cursor and offset."},
				{`offset:1,after:"bad"`, "Cannot specify both after cursor and offset."},
				{`orderBy:"missing"`, "Unknown column"},
				{`after:"bad!"`, "invalid page cursor"},
			} {
				result := s.Execute(t.Context(), Request{Query: "{accounts(" + tc.args + "){items{id}}}"})
				if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Message, tc.err) {
					t.Errorf("%s: %v", tc.args, result.Errors)
				}
			}
		})
	}
}
