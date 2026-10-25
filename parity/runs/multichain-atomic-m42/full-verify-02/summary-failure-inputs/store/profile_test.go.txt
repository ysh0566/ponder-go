package store

import (
	"math/big"
	"reflect"
	"testing"

	"ponder.local/ponder-go/schema"
)

func TestProfileNativeBranches(t *testing.T) {
	table := schema.Table{Name: "x", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}
	for _, tc := range []struct {
		name, kind string
		data       Row
		key        any
		path       []string
		order      map[string][]string
	}{
		{"arg order", "log", Row{"args": Row{"a": "value", "b": "value"}}, "value", []string{"args", "b"}, map[string][]string{"args": {"b", "a"}}},
		{"trace result", "trace", Row{"result": Row{"amount": big.NewInt(7)}}, big.NewInt(7), []string{"result", "amount"}, nil},
		{"transfer priority", "transfer", Row{"transfer": Row{"from": "address"}, "trace": Row{"from": "address"}}, "address", []string{"transfer", "from"}, nil},
		{"receipt contract", "transaction", Row{"transactionReceipt": Row{"contractAddress": "created"}}, "created", []string{"transactionReceipt", "contractAddress"}, nil},
		{"zero number widths", "log", Row{"log": Row{"logIndex": uint64(0)}}, int(0), []string{"log", "logIndex"}, nil},
		{"false arg", "log", Row{"args": Row{"flag": false}}, false, []string{"args", "flag"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := ProfileEvent{Type: tc.kind, ChainID: 99, Data: tc.data, FieldOrder: tc.order}
			pattern, e := RecordProfilePattern(event, table, Row{"id": tc.key}, nil)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(pattern, ProfilePattern{"id": ProfileValue{Type: "derived", Value: tc.path}}) {
				t.Fatal(pattern)
			}
		})
	}
	t.Run("timestamp quotient and canonical key", func(t *testing.T) {
		event := ProfileEvent{Type: "block", ChainID: 99, Data: Row{"block": Row{"timestamp": big.NewInt(7200)}}}
		pattern, e := RecordProfilePattern(event, table, Row{"id": big.NewInt(2)}, nil)
		if e != nil {
			t.Fatal(e)
		}
		if pattern["id"].DivideBy != 3600 {
			t.Fatal(pattern)
		}
		event.Data["block"] = Row{"timestamp": big.NewInt(10800)}
		row, e := RecoverProfilePattern(pattern, event)
		if e != nil || row["id"].(*big.Int).Cmp(big.NewInt(3)) != 0 {
			t.Fatal(row, e)
		}
		key, e := ProfilePatternKey(pattern)
		if e != nil || key != `{"id":{"type":"derived","value":["block","timestamp"]}}` {
			t.Fatal(key, e)
		}
	})
}
