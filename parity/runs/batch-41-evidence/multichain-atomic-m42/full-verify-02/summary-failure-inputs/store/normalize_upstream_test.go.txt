package store

import (
	"ponder.local/ponder-go/schema"
	"reflect"
	"testing"
	"time"
)

func TestUpstreamNormalize(t *testing.T) {
	t.Run("normalize smallint", func(t *testing.T) {
		column := schema.Column{Name: "balance", Type: "smallint"}
		value := int64(123)
		result, e := NormalizeColumn(column, value, false)
		if e != nil {
			t.Fatal(e)
		}
		if result != value {
			t.Fatalf("source toBe(value): %v", result)
		}
	})
	t.Run("normalizeUpdateSet", func(t *testing.T) {
		calls := 0
		table := schema.Table{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true, MapToDriverValue: func(value any) (any, error) { calls++; return value, nil }}, {Name: "balance", Type: "bigint"}, {Name: "metadata", Type: "json"}, {Name: "values", Type: "integer[]"}, {Name: "bytes", Type: "bytes"}, {Name: "timestamp", Type: "timestamp"}}}
		metadata := map[string]any{"nested": []int64{1, 2}}
		values := []int64{1, 2}
		data := []byte{1, 2}
		timestamp := time.UnixMilli(1742925862000).UTC()
		result, e := NormalizeUpdateSet(table, Row{"metadata": metadata, "values": values, "bytes": data, "timestamp": &timestamp, "balance": Undefined})
		if e != nil {
			t.Fatal(e)
		}
		if calls != 0 {
			t.Fatal("source addressMapper not called", calls)
		}
		expectSourceMatch(t, result, Row{"metadata": map[string]any{"nested": []int64{1, 2}}, "values": []int64{1, 2}, "bytes": []byte{1, 2}, "timestamp": &timestamp, "balance": Undefined})
		if reflect.ValueOf(result["metadata"]).Pointer() == reflect.ValueOf(metadata).Pointer() {
			t.Fatal("source metadata.not.toBe(input)")
		}
		if reflect.ValueOf(result["values"]).Pointer() == reflect.ValueOf(values).Pointer() {
			t.Fatal("source values.not.toBe(input)")
		}
		if _, ok := result["bytes"].([]byte); !ok {
			t.Fatal("source Uint8Array type", result["bytes"])
		}
		if _, ok := result["timestamp"].(*time.Time); !ok {
			t.Fatal("source Date type", result["timestamp"])
		}
	})
}
