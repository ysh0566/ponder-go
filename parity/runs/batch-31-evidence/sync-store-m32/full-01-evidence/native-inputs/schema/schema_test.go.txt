package schema

import (
	"fmt"
	"math/big"
	"testing"
)

func account() Table {
	return Table{Name: "account", Columns: []Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}
}
func expectBuild(t *testing.T, s Schema, order Ordering, wantError bool) {
	t.Helper()
	e := s.Validate(order)
	if wantError && e == nil {
		t.Fatal("expected buildSchema error")
	}
	if !wantError && e != nil {
		t.Fatal(e)
	}
}
func fields(table Table) []ViewField {
	out := []ViewField{}
	for i := range table.Columns {
		c := table.Columns[i]
		out = append(out, ViewField{Name: c.Name, Column: &c})
	}
	return out
}

// One subtest for every original build/schema.test.ts declaration, preserving
// the three successive builds and both rejection assertions in "buildSchema exp".
func TestUpstreamSchema(t *testing.T) {
	t.Run("buildSchema() success", func(t *testing.T) { expectBuild(t, Schema{Tables: []Table{account()}}, Multichain, false) })
	t.Run("buildSchema() success with more than 100 tables", func(t *testing.T) {
		s := Schema{}
		for i := 0; i < 101; i++ {
			s.Tables = append(s.Tables, Table{Name: fmt.Sprintf("table_%d", i), ExportName: fmt.Sprintf("table%d", i), Columns: []Column{{Name: "id", Type: "integer", Primary: true}}})
		}
		expectBuild(t, s, Multichain, false)
	})
	t.Run("buildSchema() error with multiple primary key", func(t *testing.T) {
		a := account()
		a.Columns[1].Primary = true
		a.Columns[1].NotNull = false
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() error with no primary key", func(t *testing.T) {
		a := account()
		a.Columns[0].Primary = false
		a.Columns[1].NotNull = false
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() success with composite primary key", func(t *testing.T) {
		a := account()
		a.Columns[0].Primary = false
		a.Columns[0].NotNull = true
		a.PrimaryKey = []string{"address", "balance"}
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, false)
	})
	t.Run("buildSchema() error with sequences", func(t *testing.T) {
		expectBuild(t, Schema{Tables: []Table{account()}, Sequences: []Sequence{{Name: "seq"}}}, Multichain, true)
	})
	t.Run("buildSchema() error with generated", func(t *testing.T) {
		a := account()
		a.Columns[0].Type = "integer"
		a.Columns[1].Generated = true
		a.Columns[1].GeneratedExpression = big.NewInt(10)
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() error with generated identity", func(t *testing.T) {
		a := account()
		a.Columns[0] = Column{Name: "id", Type: "integer", Primary: true, Identity: true, IdentityOptions: &IdentityOptions{StartWith: 1000}}
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() error with serial", func(t *testing.T) {
		a := account()
		a.Columns[0].Type = "serial"
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() success with default", func(t *testing.T) {
		a := account()
		a.Columns[0].Type = "integer"
		a.Columns[1].NotNull = false
		a.Columns[1].Default = big.NewInt(10)
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, false)
	})
	t.Run("buildSchema() error with default sql", func(t *testing.T) {
		a := account()
		a.Columns[0].Type = "integer"
		a.Columns[1].NotNull = false
		a.Columns[1].Default = RawSQL{Text: "10"}
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() error with $defaultFn sql", func(t *testing.T) {
		a := account()
		a.Columns[0].Type = "integer"
		a.Columns[1].NotNull = false
		a.Columns[1].DefaultFn = func() any { return RawSQL{Text: "10"} }
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() error with $onUpdateFn sql", func(t *testing.T) {
		a := account()
		a.Columns[0].Type = "integer"
		a.Columns[1].NotNull = false
		a.Columns[1].OnUpdateFn = func() any { return RawSQL{Text: "10"} }
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() error with foreign key", func(t *testing.T) {
		a := account()
		a.Columns[0].Type = "integer"
		a.Columns[1].References = "account.address"
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() error with unique", func(t *testing.T) {
		a := account()
		a.Columns[0].Type = "integer"
		a.Columns[1].Unique = true
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() error with check", func(t *testing.T) {
		a := account()
		a.CheckConstraints = []CheckConstraint{{Name: "test", Expression: ""}}
		expectBuild(t, Schema{Tables: []Table{a}}, Multichain, true)
	})
	t.Run("buildSchema() success with enum", func(t *testing.T) {
		a := Table{Name: "account", Columns: []Column{{Name: "address", Type: "hex", Primary: true}, {Name: "m", Type: "mood", NotNull: true}}}
		expectBuild(t, Schema{Tables: []Table{a}, Enums: []Enum{{Name: "mood", Values: []string{"good", "bad"}}}}, Multichain, false)
	})
	t.Run("buildSchema() duplicate table name", func(t *testing.T) {
		a := account()
		a.Columns = a.Columns[:1]
		b := a
		b.ExportName = "account2"
		expectBuild(t, Schema{Tables: []Table{a, b}}, Multichain, true)
	})
	t.Run("buildSchema() duplicate index name", func(t *testing.T) {
		a := account()
		a.Indexes = []Index{{Name: "balance_idx", Columns: []string{"balance"}}}
		b := account()
		b.Name = "account2"
		b.Indexes = []Index{{Name: "balance_idx", Columns: []string{"balance"}}}
		expectBuild(t, Schema{Tables: []Table{a, b}}, Multichain, true)
	})
	t.Run("buildSchema exp", func(t *testing.T) {
		a := account()
		expectBuild(t, Schema{Tables: []Table{a}}, Isolated, true)
		a.Columns = append(a.Columns, Column{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true})
		expectBuild(t, Schema{Tables: []Table{a}}, Isolated, true)
		a.Columns[0].Primary = false
		a.Columns[0].NotNull = true
		a.PrimaryKey = []string{"address", "chain_id"}
		expectBuild(t, Schema{Tables: []Table{a}}, Isolated, false)
	})
	t.Run("buildSchema view", func(t *testing.T) {
		a := account()
		a.Indexes = []Index{{Name: "balance_idx", Columns: []string{"balance"}}}
		expectBuild(t, Schema{Tables: []Table{a}, Views: []View{{Name: "account_view", Fields: fields(a), Query: `SELECT address,balance FROM account`}}}, Multichain, false)
	})
	t.Run("buildSchema view raw sql", func(t *testing.T) {
		a := account()
		a.Indexes = []Index{{Name: "balance_idx", Columns: []string{"balance"}}}
		expectBuild(t, Schema{Tables: []Table{a}, Views: []View{{Name: "account_view", Fields: fields(a), Query: `SELECT * FROM account`}}}, Multichain, false)
	})
	t.Run("buildSchema view with aggregate functions", func(t *testing.T) {
		a := Table{Name: "trade", Columns: []Column{{Name: "id", Type: "text", Primary: true}, {Name: "market_address", Property: "marketAddress", Type: "hex", NotNull: true}, {Name: "cost", Type: "bigint", NotNull: true}}}
		market := a.Columns[1]
		v := View{Name: "trade_volume", Fields: []ViewField{{Name: "market_address", Column: &market}, {Name: "volume", Expression: "SUM(cost)", Aliased: true}, {Name: "count", Expression: "COUNT(*)", Aliased: true}}, Query: `SELECT market_address,SUM(cost) AS volume,COUNT(*) AS count FROM trade GROUP BY market_address`}
		expectBuild(t, Schema{Tables: []Table{a}, Views: []View{v}}, Multichain, false)
	})
	t.Run("buildSchema view with alias", func(t *testing.T) {
		a := Table{Name: "transfer", Columns: []Column{{Name: "id", Type: "text", Primary: true}, {Name: "timestamp", Type: "bigint", NotNull: true}, {Name: "amount", Type: "bigint", NotNull: true}}}
		v := View{Name: "hourly_bucket", Fields: []ViewField{{Name: "hour", Expression: "FLOOR(timestamp / 3600) * 3600", Aliased: true}, {Name: "total_volume", Expression: "SUM(amount)", Aliased: true}, {Name: "transfer_count", Expression: "COUNT(*)", Aliased: true}}, Query: `SELECT FLOOR(timestamp / 3600) * 3600 AS hour,SUM(amount) AS total_volume,COUNT(*) AS transfer_count FROM transfer GROUP BY FLOOR(timestamp / 3600)`}
		expectBuild(t, Schema{Tables: []Table{a}, Views: []View{v}}, Multichain, false)
	})
}
