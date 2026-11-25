package schema

import (
	"reflect"
	"testing"
)

func TestSourcePrimaryKeyColumns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		columns []Column
		keys    []string
		want    []PrimaryKeyColumn
	}{
		{"getPrimaryKeyColumns()", []Column{SourceColumn("account", Column{Type: "hex", Primary: true}), SourceColumn("balance", Column{Type: "bigint", NotNull: true})}, nil, []PrimaryKeyColumn{{"account", "account"}}},
		{"getPrimaryKeyColumns() sql", []Column{SourceColumn("name", Column{Name: "unique_name", Type: "integer", Primary: true})}, nil, []PrimaryKeyColumn{{"name", "unique_name"}}},
		{"getPrimaryKeyColumns() snake case", []Column{SourceColumn("chainId", Column{Type: "integer", Primary: true})}, nil, []PrimaryKeyColumn{{"chainId", "chain_id"}}},
		{"getPrimaryKeyColumns() composite", []Column{SourceColumn("name", Column{Type: "text"}), SourceColumn("age", Column{Type: "integer"}), SourceColumn("address", Column{Type: "hex"})}, []string{"name", "address"}, []PrimaryKeyColumn{{"name", "name"}, {"address", "address"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := Table{Name: "table", Columns: tc.columns, PrimaryKey: tc.keys}
			if got := table.PrimaryKeyColumns(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("primary keys: %v", got)
			}
		})
	}
}

func TestSourceColumnCasing(t *testing.T) {
	for name, want := range map[string]string{"HTTPServerID": "http_server_id", "owner2ID": "owner2_id", "some'Value": "some_value", "A1b": "a_1b"} {
		table := Table{Columns: []Column{SourceColumn(name, Column{Type: "integer", Primary: true})}}
		got := table.PrimaryKeyColumns()
		if len(got) != 1 || got[0].JS != name || got[0].SQL != want {
			t.Fatal(name, got)
		}
	}
}
