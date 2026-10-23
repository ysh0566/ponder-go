package store

import (
	"context"
	"math/big"
	"ponder.local/ponder-go/schema"
	"testing"
	"time"
)

func TestUpstreamORMColumns(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"basic columns", "array", "text array", "enum", "bytes", "text with null bytes", "timestamp", "point", "line"} {
				t.Run(name, func(t *testing.T) {
					model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}}}}}
					input := Row{"address": sourceZeroAddress}
					expected := Row{"address": sourceZeroAddress}
					add := func(column schema.Column, value any) {
						model.Tables[0].Columns = append(model.Tables[0].Columns, column)
						key := property(column)
						input[key] = value
					}
					switch name {
					case "basic columns":
						expected = Row{"address": sourceZeroAddress, "balance": big.NewInt(20), "boolean": true, "char": "2", "doublePrecision": int64(20), "int2": int64(20), "int8b": big.NewInt(20), "int8n": int64(20), "numeric": "20", "real": int64(20), "text": "20", "varchar": "20"}
						for _, column := range []schema.Column{{Name: "balance", Type: "bigint"}, {Name: "int2", Type: "smallint"}, {Name: "int8n", Type: "int8", Mode: "number"}, {Name: "int8b", Type: "int8", Mode: "bigint"}, {Name: "boolean", Type: "boolean"}, {Name: "text", Type: "text"}, {Name: "varchar", Type: "varchar"}, {Name: "char", Type: "char"}, {Name: "numeric", Type: "numeric"}, {Name: "real", Type: "real"}, {Name: "double_precision", Property: "doublePrecision", Type: "doublePrecision"}} {
							column.NotNull = true
							var value any = int64(20)
							switch column.Name {
							case "balance", "int8b":
								value = big.NewInt(20)
							case "boolean":
								value = true
							case "text", "varchar", "numeric":
								value = "20"
							case "char":
								value = "2"
							}
							add(column, value)
						}
					case "array":
						expected = Row{"address": sourceZeroAddress, "balances": []*big.Int{big.NewInt(20)}}
						add(schema.Column{Name: "balances", Type: "bigint[]", NotNull: true}, []*big.Int{big.NewInt(20)})
					case "text array":
						expected = Row{"address": sourceZeroAddress, "textArray": []string{"//U_W_U\\\\"}}
						model.Tables[0].Name = "test"
						add(schema.Column{Name: "text_array", Property: "textArray", Type: "text[]", NotNull: true}, []string{"//U_W_U\\\\"})
					case "enum":
						expected = Row{"address": sourceZeroAddress, "mood": "ok"}
						model.Enums = []schema.Enum{{Name: "mood", Values: []string{"sad", "ok", "happy"}}}
						add(schema.Column{Name: "mood", Type: "mood"}, "ok")
					case "bytes":
						expected = Row{"address": sourceZeroAddress, "calldata": make([]byte, 20)}
						add(schema.Column{Name: "calldata", Type: "bytes", NotNull: true}, make([]byte, 20))
					case "text with null bytes":
						add(schema.Column{Name: "name", Type: "text", NotNull: true}, "tencentclub\x00\x00\x00\x00\x00\x00")
						expected["name"] = "tencentclub"
					case "timestamp":
						date := time.UnixMilli(1742925862000).UTC()
						add(schema.Column{Name: "timestamp", Type: "timestamp", NotNull: true}, &date)
						want := time.UnixMilli(1742925862000).UTC()
						expected = Row{"address": sourceZeroAddress, "timestamp": &want}
					case "point":
						add(schema.Column{Name: "point", Type: "point", NotNull: true}, []int64{1, 2})
						expected = Row{"address": sourceZeroAddress, "point": []int64{1, 2}}
					case "line":
						add(schema.Column{Name: "line", Type: "line", NotNull: true}, []int64{1, 2, 3})
						expected = Row{"address": sourceZeroAddress, "line": []int64{1, 2, 3}}
					}
					sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
						if _, e := db.InsertOne(ctx, model.Tables[0].Name, input, Conflict{}); e != nil {
							return e
						}
						row, e := db.Find(ctx, model.Tables[0].Name, Row{"address": sourceZeroAddress})
						if e != nil {
							return e
						}
						expectSourceMatch(t, row, expected)
						return db.Cache.Flush(ctx)
					})
				})
			}
		})
	}
}
