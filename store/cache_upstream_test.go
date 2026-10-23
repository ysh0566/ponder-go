package store

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/schema"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestUpstreamCache(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"flush() insert", "flush() update", "flush() encoding", "flush() encoding escape", "flush() recovers error"} {
				if name == "flush() recovers error" && dialect != "postgres" {
					continue
				} // Original conditional returns before its assertion on PGlite.
				t.Run(name, func(t *testing.T) {
					model := sourceAccountModel()
					if name == "flush() encoding" {
						model = schema.Schema{Enums: []schema.Enum{{Name: "e", Values: []string{"a", "b", "c"}}}, Tables: []schema.Table{{Name: "test", Columns: []schema.Column{{Name: "hex", Type: "hex", Primary: true}, {Name: "bigint", Type: "bigint", NotNull: true}, {Name: "e", Type: "e", NotNull: true}, {Name: "array", Type: "integer[]", NotNull: true}, {Name: "bytes", Type: "bytes", NotNull: true}, {Name: "json", Type: "json", NotNull: true}, {Name: "null", Type: "text"}}}}}
					}
					if name == "flush() encoding escape" {
						model = schema.Schema{Tables: []schema.Table{{Name: "test", Columns: []schema.Column{{Name: "backslash", Type: "text", Primary: true}}}}}
					}
					sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
						if name == "flush() encoding escape" {
							values := []Row{}
							for _, value := range []string{"\\\\", "\\b", "\\f", "\\n", "\\r", "\\t", "\\v", "\\00", "\\x00", "\\", "\b", "\f", "\n", "\r", "\t", "\v"} {
								values = append(values, Row{"backslash": value})
							}
							if _, e := db.InsertMany(ctx, "test", values, Conflict{}); e != nil {
								return e
							}
							if e := db.Cache.Flush(ctx); e != nil {
								return e
							}
							db.Cache.Clear()
							result, e := db.Select(ctx, "test", nil)
							if e != nil {
								return e
							}
							if !reflect.DeepEqual(sourceValue(result), sourceValue(values)) {
								t.Fatalf("source toStrictEqual escape values: %v", result)
							}
							return nil
						}
						if name == "flush() encoding" {
							values := []Row{{"hex": sourceZeroAddress, "bigint": big.NewInt(10), "e": "a", "array": []int64{1, 2, 4}, "bytes": []byte{0, 128, 255, 1}, "json": map[string]any{"a": int64(1), "b": int64(2)}, "null": nil}, {"hex": fmt.Sprintf("0x%040x", 1), "bigint": big.NewInt(11), "e": "b", "array": []int64{}, "bytes": []byte{}, "json": map[string]any{}, "null": nil}, {"hex": fmt.Sprintf("0x%040x", 2), "bigint": big.NewInt(12), "e": "c", "array": []int64{0}, "bytes": []byte{0x5c, 0x4e, 9, 10, 13}, "json": map[string]any{"c": int64(3)}, "null": nil}}
							if _, e := db.InsertMany(ctx, "test", values, Conflict{}); e != nil {
								return e
							}
							if e := db.Cache.Flush(ctx); e != nil {
								return e
							}
							db.Cache.Clear()
							result, e := db.Select(ctx, "test", nil)
							if e != nil {
								return e
							}
							sort.Slice(result, func(i, j int) bool { return result[i]["hex"].(string) < result[j]["hex"].(string) })
							// This literal is the complete original inline snapshot, including
							// empty bytes/arrays/objects and every NULL field.
							expected := []Row{{"hex": sourceZeroAddress, "bigint": big.NewInt(10), "e": "a", "array": []int64{1, 2, 4}, "bytes": []byte{0, 128, 255, 1}, "json": map[string]any{"a": int64(1), "b": int64(2)}, "null": nil}, {"hex": "0x0000000000000000000000000000000000000001", "bigint": big.NewInt(11), "e": "b", "array": []int64{}, "bytes": []byte{}, "json": map[string]any{}, "null": nil}, {"hex": "0x0000000000000000000000000000000000000002", "bigint": big.NewInt(12), "e": "c", "array": []int64{0}, "bytes": []byte{92, 78, 9, 10, 13}, "json": map[string]any{"c": int64(3)}, "null": nil}}
							if !reflect.DeepEqual(sourceValue(result), sourceValue(expected)) {
								t.Fatalf("source inline snapshot:\ngot %#v\nwant %#v", sourceValue(result), sourceValue(expected))
							}
							return nil
						}
						if name == "flush() update" {
							db.Cache.Invalidate()
							if _, e := db.Find(ctx, "account", Row{"address": sourceZeroAddress}); e != nil {
								return e
							}
						}
						if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
							return e
						}
						if e := db.Cache.Flush(ctx); e != nil {
							return e
						}
						if name == "flush() recovers error" {
							if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
								return e
							}
							e := db.Cache.Flush(ctx)
							if e == nil || !strings.Contains(e.Error(), `duplicate key value violates unique constraint "account_pkey"`) {
								t.Fatalf("source toThrowError: %v", e)
							}
							return nil
						}
						if name == "flush() update" {
							if _, e := db.Update(ctx, "account", Row{"address": sourceZeroAddress}, Row{"balance": big.NewInt(12)}); e != nil {
								return e
							}
							if e := db.Cache.Flush(ctx); e != nil {
								return e
							}
						}
						row, e := db.Find(ctx, "account", Row{"address": sourceZeroAddress})
						if e != nil {
							return e
						}
						want := int64(10)
						if name == "flush() update" {
							want = 12
						}
						expectSourceMatch(t, row, Row{"address": sourceZeroAddress, "balance": big.NewInt(want)})
						if name == "flush() update" {
							if _, e = db.Update(ctx, "account", Row{"address": sourceZeroAddress}, Row{"balance": big.NewInt(12)}); e != nil {
								return e
							}
							if e = db.Cache.Flush(ctx); e != nil {
								return e
							}
							row, e = db.Find(ctx, "account", Row{"address": sourceZeroAddress})
							if e != nil {
								return e
							}
							expectSourceMatch(t, row, Row{"address": sourceZeroAddress, "balance": big.NewInt(12)})
						}
						return nil
					})
				})
			}
		})
	}
}
