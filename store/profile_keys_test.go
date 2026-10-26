package store

import (
	"math"
	"math/big"
	"reflect"
	"testing"

	"ponder.local/ponder-go/schema"
)

func TestProfileRecoveredTextKey(t *testing.T) {
	table := schema.Table{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}
	event := ProfileEvent{Type: "block", ChainID: 99, Data: Row{"block": Row{"number": big.NewInt(4)}}}
	key := Row{"id": "4"}
	pattern, err := RecordProfilePattern(event, table, key, nil)
	if err != nil || !reflect.DeepEqual(pattern, ProfilePattern{"id": {Type: "derived", Value: []string{"block", "number"}}}) {
		t.Fatal(pattern, err)
	}
	hinted, err := RecordProfilePattern(event, table, key, []ProfilePattern{pattern})
	if err != nil || !reflect.DeepEqual(hinted, pattern) {
		t.Fatal("valid numeric-text hint rejected", hinted, err)
	}
	unmatched, err := RecordProfilePattern(event, table, Row{"id": "3"}, []ProfilePattern{pattern})
	if err != nil || unmatched != nil {
		t.Fatal("different key should not match existing hint", unmatched, err)
	}
	raw, err := RecoverProfilePattern(pattern, event)
	if err != nil || raw["id"].(*big.Int).Cmp(big.NewInt(4)) != 0 {
		t.Fatal("raw profile recovery changed", raw, err)
	}
	recovered, err := recoverProfileKey(pattern, event, table)
	if err != nil || recovered["id"] != "4" {
		t.Fatal(recovered, err)
	}
	if key["id"] != "4" || event.Data["block"].(Row)["number"].(*big.Int).Cmp(big.NewInt(4)) != 0 {
		t.Fatal("prediction mutated input")
	}
	if _, err := NormalizeColumn(table.Columns[0], big.NewInt(4), false); err == nil {
		t.Fatal("public text input validation was widened")
	}
	if _, err := cacheKey(table, Row{"id": big.NewInt(4)}); err == nil {
		t.Fatal("public cache key validation was widened")
	}
}

func TestProfileRecoveredTextPrimitiveFormatting(t *testing.T) {
	table := schema.Table{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}
	pattern := ProfilePattern{"id": {Type: "derived", Value: []string{"args", "value"}}}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{big.NewInt(-123), "-123"}, {uint64(123), "123"}, {true, "true"}, {false, "false"},
		{math.Copysign(0, -1), "0"}, {1e6, "1000000"}, {1e-6, "0.000001"}, {1e-7, "1e-7"}, {1e21, "1e+21"},
		{math.NaN(), "NaN"}, {math.Inf(1), "Infinity"}, {math.Inf(-1), "-Infinity"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			event := ProfileEvent{Data: Row{"args": Row{"value": tc.value}}}
			row, err := recoverProfileKey(pattern, event, table)
			if err != nil || row["id"] != tc.want {
				t.Fatal(row, err)
			}
		})
	}
	table.Columns[0].Type = "bigint"
	row, err := recoverProfileKey(pattern, ProfileEvent{Data: Row{"args": Row{"value": big.NewInt(4)}}}, table)
	if err != nil || row["id"].(*big.Int).Cmp(big.NewInt(4)) != 0 {
		t.Fatal("nontext column changed", row, err)
	}
}
