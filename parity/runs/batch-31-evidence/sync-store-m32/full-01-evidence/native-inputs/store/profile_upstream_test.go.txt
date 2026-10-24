package store

import (
	"math/big"
	"reflect"
	"strings"
	"testing"

	"ponder.local/ponder-go/schema"
)

// Every source input and inline snapshot is literal here. The source callback,
// zero checkpoint and chain fixture fields are retained in ProfileEvent; only
// the fields read by record/recover are part of the native profile domain.
func TestUpstreamProfile(t *testing.T) {
	names := []string{"no pattern", "with undefined log event args", "with undefined trace event args", "with array log event args", "with array trace event args", "chainId", "log args", "block", "hint", "object args", "string concat", "string concat mixed delimiters", "string concat hint"}
	for _, name := range names {
		t.Run("recordProfilePattern() "+name, func(t *testing.T) {
			event := ProfileEvent{Type: "block", ChainID: 1, Data: Row{"id": strings.Repeat("0", 75), "block": Row{"timestamp": big.NewInt(1)}}}
			table := sourceAccountModel().Tables[0]
			key := Row{"address": sourceZeroAddress}
			var want ProfilePattern
			var recovered Row
			derived := func(path ...string) ProfileValue { return ProfileValue{Type: "derived", Value: path} }
			switch name {
			case "with undefined log event args", "with array log event args", "log args", "object args", "string concat", "string concat mixed delimiters", "string concat hint":
				event.Type = "log"
				event.Data["log"] = Row{}
				event.Data["transaction"] = Row{}
			case "with undefined trace event args", "with array trace event args":
				event.Type = "trace"
				event.Data["trace"] = Row{}
				event.Data["transaction"] = Row{}
			}
			switch name {
			case "with array log event args", "with array trace event args":
				event.Data["args"] = []any{}
				if event.Type == "trace" {
					event.Data["result"] = []any{}
				}
			case "chainId":
				table.Columns[0].Type = "integer"
				key = Row{"address": 1}
				want = ProfilePattern{"address": derived("chainId")}
				recovered = Row{"address": uint64(1)}
			case "log args":
				event.Data["args"] = Row{"address": sourceZeroAddress}
				want = ProfilePattern{"address": derived("args", "address")}
				recovered = Row{"address": sourceZeroAddress}
			case "block", "hint":
				event.Data["block"] = Row{"number": big.NewInt(3), "timestamp": big.NewInt(1)}
				table.Columns[0].Type = "bigint"
				key = Row{"address": big.NewInt(3)}
				want = ProfilePattern{"address": derived("block", "number")}
				recovered = Row{"address": big.NewInt(3)}
			case "object args":
				event.Data["args"] = Row{"address": []string{sourceZeroAddress}}
			case "string concat", "string concat mixed delimiters", "string concat hint":
				event.Data["args"] = Row{"address": sourceZeroAddress}
				event.Data["block"] = Row{"timestamp": big.NewInt(26)}
				table = schema.Table{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "address", Type: "bigint", NotNull: true}, {Name: "balance", Type: "bigint", NotNull: true}}}
				key = Row{"id": "1-" + sourceZeroAddress}
				if name == "string concat mixed delimiters" {
					key["id"] = "1-" + sourceZeroAddress + "_" + sourceZeroAddress
				} else {
					want = ProfilePattern{"id": ProfileValue{Type: "delimiter", Delimiter: "-", Values: []ProfileValue{derived("chainId"), derived("args", "address")}}}
					recovered = Row{"id": "1-" + sourceZeroAddress}
				}
			}
			pattern, e := RecordProfilePattern(event, table, key, nil)
			if e != nil {
				t.Fatal(e)
			}
			if name == "hint" || name == "string concat hint" {
				pattern, e = RecordProfilePattern(event, table, key, []ProfilePattern{pattern})
				if e != nil {
					t.Fatal(e)
				}
			}
			if !reflect.DeepEqual(pattern, want) {
				t.Fatalf("source pattern snapshot: got %#v want %#v", pattern, want)
			}
			if recovered != nil {
				actual, e := RecoverProfilePattern(pattern, event)
				if e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(actual, recovered) {
					t.Fatalf("source recover snapshot: got %#v want %#v", actual, recovered)
				}
			}
		})
	}
}
