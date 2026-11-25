package graphql

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestNativePostgresColumnTypes(t *testing.T) {
	table := schema.Table{Name: "machine", Columns: []schema.Column{
		{Name: "id", Type: "text", Primary: true},
		{Name: "subnet", Type: "cidr", NotNull: true}, {Name: "host", Type: "inet", NotNull: true},
		{Name: "duration", Type: "interval", NotNull: true}, {Name: "mac", Type: "macaddr", NotNull: true},
		{Name: "eui", Type: "macaddr8", NotNull: true}, {Name: "clock", Type: "time", NotNull: true},
		{Name: "hosts", Type: "inet[]", NotNull: true},
	}}
	model := schema.Schema{Tables: []schema.Table{table}}
	generated, err := orm.GenerateModels("models", model)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Subnet", "Host", "Duration", "Mac", "Eui", "Clock"} {
		if !strings.Contains(strings.Join(strings.Fields(string(generated)), " "), field+" string") {
			t.Fatalf("missing generated string field %s", field)
		}
	}
	want := map[string]string{"subnet": "192.0.2.0/24", "host": "2001:db8::1", "duration": "2 days 03:04:05", "mac": "08:00:2b:01:02:03", "eui": "08:00:2b:ff:fe:01:02:03", "clock": "09:08:07.25"}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			b := testBackend(t, dialect)
			db, err := store.New(ctx, b, model, fmt.Sprintf("ponder_go_columns_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			key := store.Row{"id": "one"}
			event := func(id string, n uint64) store.Event {
				return store.Event{ID: id, Checkpoint: checkpoint.Block(1700000000+n, 1, n, true)}
			}
			first := event("insert", 1)
			if err := db.Apply(ctx, first, func(w *store.Writer) error {
				return w.Write(ctx, "machine", store.Row{"id": "one", "subnet": "192.0.2.0/24", "host": "2001:0db8:0:0::1", "duration": "2 days 3 hours 4 minutes 5 seconds", "mac": "0800.2b01.0203", "eui": "08:00:2b:ff:fe:01:02:03", "clock": "9:08:07.2500", "hosts": []string{"192.0.2.1", "2001:db8::1"}}, store.Insert)
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Apply(ctx, event("update", 2), func(w *store.Writer) error {
				row, err := w.Find(ctx, "machine", key)
				if err != nil {
					return err
				}
				for name, value := range want {
					if row[name] != value {
						return fmt.Errorf("%s: %v, want %s", name, row[name], value)
					}
				}
				return w.Write(ctx, "machine", store.Row{"id": "one", "duration": "5 minutes"}, store.Update)
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Revert(ctx, first.Checkpoint); err != nil {
				t.Fatal(err)
			}
			server, err := New(db, Options{})
			if err != nil {
				t.Fatal(err)
			}
			query := `{machines(where:{subnet:"192.0.2.0/24",host_contains:"db8",duration:"2 days 03:04:05",mac_starts_with:"08:00",eui_ends_with:"02:03",clock:"09:08:07.25",hosts_has:"192.0.2.1"},orderBy:"clock"){items{id subnet host duration mac eui clock hosts} totalCount}}`
			result := server.Execute(ctx, Request{Query: query})
			if result.HasErrors() {
				t.Fatal(result.Errors)
			}
			page := result.Data.(map[string]any)["machines"].(map[string]any)
			items := page["items"].([]any)
			if len(items) != 1 {
				t.Fatal(page)
			}
			row := items[0].(map[string]any)
			for name, value := range want {
				if row[name] != value {
					t.Fatalf("%s: %v, want %s", name, row[name], value)
				}
			}
			if fmt.Sprint(row["hosts"]) != "[192.0.2.1 2001:db8::1]" {
				t.Fatal(row)
			}
		})
	}
}

func TestGeometricColumns(t *testing.T) {
	table := schema.Table{Name: "drawing", Columns: []schema.Column{
		{Name: "id", Type: "text", Primary: true}, {Name: "origin", Type: "point"},
		{Name: "axis", Type: "line"}, {Name: "route", Type: "point[]"},
	}}
	model := schema.Schema{Tables: []schema.Table{table}}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			db, err := store.New(ctx, testBackend(t, dialect), model, fmt.Sprintf("ponder_go_geometry_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			err = db.Apply(ctx, store.Event{ID: "draw", Checkpoint: checkpoint.Block(100, 1, 1, true)}, func(w *store.Writer) error {
				if err := w.Write(ctx, "drawing", store.Row{"id": "a", "origin": []float64{1, 2}, "axis": []float64{1, 0, -1}, "route": [][]float64{{1, 2}, {3, 4}}}, store.Insert); err != nil {
					return err
				}
				return w.Write(ctx, "drawing", store.Row{"id": "b", "origin": []float64{3, 4}}, store.Insert)
			})
			if err != nil {
				t.Fatal(err)
			}
			err = db.Apply(ctx, store.Event{ID: "update", Checkpoint: checkpoint.Block(110, 1, 2, true)}, func(w *store.Writer) error {
				return w.Write(ctx, "drawing", store.Row{"id": "a", "origin": [2]float64{5, 6}, "route": []string{"(5,6)"}, "axis": "{0,1,-2}"}, store.Update)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Revert(ctx, checkpoint.Block(100, 1, 1, true)); err != nil {
				t.Fatal(err)
			}
			s, err := New(db, Options{})
			if err != nil {
				t.Fatal(err)
			}
			read := func(query string, vars map[string]any) map[string]any {
				t.Helper()
				result := s.Execute(ctx, Request{Query: query, Variables: vars})
				if result.HasErrors() {
					t.Fatal(result.Errors)
				}
				return result.Data.(map[string]any)["drawings"].(map[string]any)
			}
			page := read(`{drawings(where:{origin:"(1, 2)",axis_in:["{1,0,-1}"],route_has:"(3, 4)"}){items{id origin axis route} totalCount}}`, nil)
			items := page["items"].([]any)
			if len(items) != 1 {
				t.Fatal(page)
			}
			row := items[0].(map[string]any)
			if row["id"] != "a" || row["origin"] != "(1,2)" || row["axis"] != "{1,0,-1}" || fmt.Sprint(row["route"]) != "[(1,2) (3,4)]" {
				t.Fatal(row)
			}
			query := `query Sorted($after:String){drawings(orderBy:"origin",limit:1,after:$after){items{id axis} pageInfo{endCursor}}}`
			page = read(query, nil)
			if fmt.Sprint(page["items"]) != "[map[axis:{1,0,-1} id:a]]" {
				t.Fatal(page)
			}
			cursor := page["pageInfo"].(map[string]any)["endCursor"]
			page = read(query, map[string]any{"after": cursor})
			items = page["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["id"] != "b" || items[0].(map[string]any)["axis"] != nil {
				t.Fatal(page)
			}
		})
	}
}
