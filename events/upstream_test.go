package events

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"

	"ponder.local/ponder-go/filter"
)

func fixture(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x["$bigint"].(string); ok && len(x) == 1 {
			n, ok := new(big.Int).SetString(s, 10)
			if !ok {
				panic("bad source bigint")
			}
			return n
		}
		if x["$undefined"] == true && len(x) == 1 {
			return filter.Undefined
		}
		o := Object{}
		for k, v := range x {
			o[k] = fixture(v)
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = fixture(v)
		}
		return out
	}
	return v
}
func marked(v any) any {
	switch x := v.(type) {
	case *big.Int:
		return Object{"$bigint": x.String()}
	case Object:
		o := Object{}
		for k, v := range x {
			o[k] = marked(v)
		}
		return o
	case []Object:
		out := []any{}
		for _, v := range x {
			out = append(out, marked(v))
		}
		return out
	case []RawEvent:
		out := []any{}
		for _, v := range x {
			out = append(out, marked(v.Object()))
		}
		return out
	case []any:
		out := []any{}
		for _, v := range x {
			out = append(out, marked(v))
		}
		return out
	}
	return v
}
func objects(v any) []Object {
	out := []Object{}
	if v == nil {
		return out
	}
	for _, v := range v.([]any) {
		out = append(out, object(v))
	}
	return out
}
func batch(o Object) Batch {
	b := Batch{objects(o["eventCallbacks"]), objects(o["blocks"]), objects(o["logs"]), objects(o["transactions"]), objects(o["transactionReceipts"]), objects(o["traces"]), map[string][]filter.ChildAddress{}, mustNumber(o["chainId"])}
	if m := object(o["childAddresses"]); m != nil {
		for _, value := range m["$map"].([]any) {
			pair := value.([]any)
			entries := []filter.ChildAddress{}
			for _, v := range object(pair[1])["$map"].([]any) {
				child := v.([]any)
				entries = append(entries, filter.ChildAddress{Address: text(child[0]), Block: mustNumber(child[1])})
			}
			b.ChildAddresses[text(pair[0])] = entries
		}
	}
	return b
}
func TestUpstreamEventConstruction(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/selected-eth-events.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Cases []struct {
			ID    string
			Title string
			Calls []struct {
				Operation string
				Args      []any
				Result    any
				Error     *struct{ Name, Message string }
			}
		}
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Cases) != 4 {
		t.Fatal("expected four complete source declarations")
	}
	for _, c := range data.Cases {
		t.Run(c.Title, func(t *testing.T) {
			for i, call := range c.Calls {
				args := fixture(call.Args).([]any)
				var got any
				var err error
				switch call.Operation {
				case "buildEvents":
					var built []RawEvent
					b := batch(object(args[0]))
					built, err = BuildEvents(b)
					got = built
					if err == nil {
						if len(built) != 1 || mustNumber(built[0].Transaction["transactionIndex"]) != 1 {
							t.Fatal("original receipt/index/length assertions", built)
						}
						if fmt.Sprintf("%p", built[0].TransactionReceipt) != fmt.Sprintf("%p", b.TransactionReceipts[1]) {
							t.Fatal("original receipt object identity assertion")
						}
					}
				case "decodeEvents":
					raw := []RawEvent{}
					for _, v := range objects(args[3]) {
						raw = append(raw, rawObject(v))
					}
					got, err = DecodePublicEvents(object(args[1]), objects(args[2]), raw)
				default:
					t.Fatal("missing source operation", call.Operation)
				}
				if call.Error != nil {
					if err == nil || err.Error() != call.Error.Message {
						t.Fatal("original exception assertion", err, call.Error.Message)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				actual, e := json.Marshal(marked(got))
				if e != nil {
					t.Fatal(e)
				}
				expected, e := json.Marshal(call.Result)
				if e != nil {
					t.Fatal(e)
				}
				if string(actual) != string(expected) {
					t.Fatalf("source operation%d %s\ngot %s\nwant %s", i, call.Operation, actual, expected)
				}
			}
		})
	}
}
