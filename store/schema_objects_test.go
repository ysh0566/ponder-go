package store

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"ponder.local/ponder-go/schema"
	"reflect"
	"testing"
	"time"
)

func TestSchemaObjects(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			var b Backend
			var e error
			if dialect == "postgres" {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if url == "" {
					t.Skip("explicit task PostgreSQL required; no parity credit")
				}
				b, e = OpenPostgres(ctx, url)
			} else {
				b, e = OpenPGlite(ctx, "memory://")
			}
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			model := schema.Schema{Enums: []schema.Enum{{Name: "mood", Values: []string{"good", "bad"}}}, Tables: []schema.Table{
				{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", Default: big.NewInt(10)}, {Name: "m", Type: "mood", NotNull: true}}},
				{Name: "trade", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "market_address", Type: "hex", NotNull: true}, {Name: "cost", Type: "bigint", NotNull: true}}},
				{Name: "transfer", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "timestamp", Type: "bigint", NotNull: true}, {Name: "amount", Type: "bigint", NotNull: true}}},
			}}
			col := func(name, typ string) schema.ViewField {
				return schema.ViewField{Name: name, Column: &schema.Column{Name: name, Type: typ}}
			}
			expr := func(name, sql string) schema.ViewField {
				return schema.ViewField{Name: name, Expression: sql, Aliased: true}
			}
			model.Views = []schema.View{
				{Name: "account_nested", Fields: []schema.ViewField{col("address", "hex"), col("balance", "bigint")}, Query: `SELECT address,balance FROM account_view`},
				{Name: "account_view", Fields: []schema.ViewField{col("address", "hex"), col("balance", "bigint")}, Query: `SELECT address,balance FROM account`},
				{Name: "account_raw", Fields: []schema.ViewField{col("address", "hex"), col("balance", "bigint"), col("m", "mood")}, Query: `SELECT * FROM account`},
				{Name: "trade_volume", Fields: []schema.ViewField{col("market_address", "hex"), expr("volume", "SUM(cost)"), expr("count", "COUNT(*)")}, Query: `SELECT market_address,SUM(cost) AS volume,COUNT(*) AS count FROM trade GROUP BY market_address`},
				{Name: "hourly_bucket", Fields: []schema.ViewField{expr("hour", "FLOOR(timestamp / 3600) * 3600"), expr("total_volume", "SUM(amount)"), expr("transfer_count", "COUNT(*)")}, Query: `SELECT FLOOR(timestamp / 3600) * 3600 AS hour,SUM(amount) AS total_volume,COUNT(*) AS transfer_count FROM transfer GROUP BY FLOOR(timestamp / 3600)`},
			}
			s, e := New(ctx, b, model, fmt.Sprintf("ponder_go_objects_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			e = s.Apply(ctx, event("objects", 1, 1), func(w *Writer) error {
				for _, entry := range []struct {
					table string
					row   Row
				}{{"account", Row{"address": "0x0", "m": "good"}}, {"trade", Row{"id": "1", "market_address": "0x0", "cost": big.NewInt(8)}}, {"trade", Row{"id": "2", "market_address": "0x0", "cost": big.NewInt(9)}}, {"transfer", Row{"id": "1", "timestamp": big.NewInt(3601), "amount": big.NewInt(7)}}, {"transfer", Row{"id": "2", "timestamp": big.NewInt(3666), "amount": big.NewInt(8)}}} {
					if e := w.Write(ctx, entry.table, entry.row, Insert); e != nil {
						return e
					}
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			for _, test := range []struct {
				sql  string
				want string
			}{{`SELECT balance::text AS value FROM account_nested`, "10"}, {`SELECT m::text AS value FROM account_raw`, "good"}, {`SELECT volume::text AS value FROM trade_volume`, "17"}, {`SELECT total_volume::text AS value FROM hourly_bucket`, "15"}, {`SELECT hour::text AS value FROM hourly_bucket`, "3600"}} {
				r, e := s.QueryReadOnly(ctx, test.sql)
				if e != nil || len(r.Rows) != 1 || fmt.Sprint(r.Rows[0]["value"]) != test.want {
					t.Fatalf("%s: %v %v", test.sql, r, e)
				}
			}
			deps, e := s.QueryDependencies([]string{"account_nested", "account_raw"})
			if e != nil || !reflect.DeepEqual(deps, []string{"account"}) {
				t.Fatalf("view dependencies %v %v", deps, e)
			}
			if e = s.Revert(ctx, event("", 1, 0).Checkpoint); e != nil {
				t.Fatal(e)
			}
			r, e := s.QueryReadOnly(ctx, `SELECT * FROM hourly_bucket`)
			if e != nil || len(r.Rows) != 0 {
				t.Fatalf("view rollback %v %v", r, e)
			}
			if _, e = New(ctx, b, model, s.Namespace, schema.Multichain); e != nil {
				t.Fatal("schema resume", e)
			}
		})
	}
}
