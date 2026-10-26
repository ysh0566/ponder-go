package events_test

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type decodedOrderCase struct {
	name, kind, component string
	inputs, outputs       []abi.Parameter
	values                []uint64
	target                uint64
	keys, path            []string
	final                 map[string]uint64
}

func orderParameter(name string, indexed bool) abi.Parameter {
	return abi.Parameter{Name: name, HasName: name != "", Type: "uint256", Indexed: indexed}
}
func orderParameters(names ...string) []abi.Parameter {
	out := []abi.Parameter{}
	for _, name := range names {
		out = append(out, orderParameter(name, false))
	}
	return out
}
func decodeOrderEvent(t *testing.T, c decodedOrderCase) events.Object {
	t.Helper()
	cb := events.Object{"type": "contract", "name": "Token:Order", "fn": indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil }), "filter": events.Object{"type": c.kind}}
	raw := events.RawEvent{EventCallbackIndex: 0, Checkpoint: "actual-decoder", Block: events.Object{"number": big.NewInt(50), "timestamp": big.NewInt(5000)}, Transaction: events.Object{"transactionIndex": 0, "hash": "0x" + strings.Repeat("1", 64)}}
	if c.kind == "log" {
		item := abi.Event{Type: "event", Name: "Order", Inputs: c.inputs}
		cb["abiItem"] = item
		topics := []string{abi.EventSelector(item)}
		data := "0x"
		for i, p := range c.inputs {
			word := fmt.Sprintf("%064x", c.values[i])
			if p.Indexed {
				topics = append(topics, "0x"+word)
			} else {
				data += word
			}
		}
		raw.Log = events.Object{"topics": topics, "data": data, "logIndex": 0, "transactionIndex": 0, "address": "0x" + strings.Repeat("2", 40)}
	} else {
		item := abi.Function{Type: "function", Name: "order", Inputs: c.inputs, Outputs: c.outputs}
		cb["abiItem"] = item
		input := abi.FunctionSelector(item)
		for range c.inputs {
			input += fmt.Sprintf("%064x", 7)
		}
		output := "0x"
		for _, v := range c.values {
			output += fmt.Sprintf("%064x", v)
		}
		raw.Trace = events.Object{"input": input, "output": output, "traceAddress": []int{}, "from": "0x" + strings.Repeat("3", 40), "to": "0x" + strings.Repeat("4", 40)}
	}
	chain := events.Object{"id": 1, "name": "one"}
	decoded, err := (&events.Decoder{}).DecodeEvents(chain, []events.Object{cb}, []events.RawEvent{raw})
	if err != nil || len(decoded) != 1 {
		t.Fatal("actual decode failed", decoded, err)
	}
	if reflect.ValueOf(decoded[0]["eventCallback"]).Pointer() != reflect.ValueOf(cb).Pointer() || reflect.ValueOf(decoded[0]["chain"]).Pointer() != reflect.ValueOf(chain).Pointer() {
		t.Fatal("canonical decoded identity lost")
	}
	return decoded[0]
}
func assertDualProfileOrder(t *testing.T, event events.Object, target uint64, order map[string][]string, path []string) {
	t.Helper()
	body := event["event"].(events.Object)
	table := schema.Table{Name: "order_control", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}
	row, err := store.RecordProfilePattern(store.ProfileEvent{Type: event["type"].(string), ChainID: 1, Data: store.Row(body), FieldOrder: order}, table, store.Row{"id": fmt.Sprint(target)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rpc, err := indexing.RecordProfilePattern(event, indexing.ContractCall{Address: "0x" + strings.Repeat("9", 40), FunctionName: "read", Args: []any{new(big.Int).SetUint64(target)}}, "", nil, order)
	if err != nil || rpc == nil || len(rpc.Pattern.Args) != 1 {
		t.Fatal("actual RPC profiler failed", rpc, err)
	}
	if path == nil {
		if row != nil || rpc.Pattern.Args[0].Type != "constant" {
			t.Fatal("array/scalar generated derived profile", row, rpc.Pattern.Args)
		}
	} else if row == nil || row["id"].Type != "derived" || !reflect.DeepEqual(row["id"].Value, path) || rpc.Pattern.Args[0].Type != "derived" || !reflect.DeepEqual(rpc.Pattern.Args[0].Value, path) {
		t.Fatal("dual profiler wrong equal-value key", row, rpc.Pattern.Args, path)
	}
}
func TestDecodedProfileFieldOrder(t *testing.T) {
	numeric := []string{"10", "2", "z", "4294967295", "01", "4294967294", "a"}
	numericKeys := []string{"2", "10", "4294967294", "z", "4294967295", "01", "a"}
	tuple := func(p []abi.Parameter) []abi.Parameter { return []abi.Parameter{{Type: "tuple", Components: p}} }
	cases := []decodedOrderCase{
		{name: "log-indexed-before-nonindexed", kind: "log", inputs: []abi.Parameter{orderParameter("a", false), orderParameter("z", true)}, values: []uint64{7, 7}, target: 7, component: "args", keys: []string{"z", "a"}, path: []string{"args", "z"}},
		{name: "log-nonindexed-declaration-order", kind: "log", inputs: orderParameters("z", "a"), values: []uint64{7, 7}, target: 7, component: "args", keys: []string{"z", "a"}, path: []string{"args", "z"}},
		{name: "log-integer-key-boundaries", kind: "log", inputs: orderParameters(numeric...), values: []uint64{7, 7, 7, 7, 7, 7, 7}, target: 7, component: "args", keys: numericKeys, path: []string{"args", "2"}},
		{name: "log-duplicate-final-data-value", kind: "log", inputs: []abi.Parameter{orderParameter("dup", false), orderParameter("z", false), orderParameter("dup", true)}, values: []uint64{9, 7, 7}, target: 9, component: "args", keys: []string{"dup", "z"}, path: []string{"args", "dup"}, final: map[string]uint64{"dup": 9, "z": 7}},
		{name: "log-unnamed-array-skipped", kind: "log", inputs: orderParameters("a", ""), values: []uint64{7, 7}, target: 7},
		{name: "log-no-arguments-skipped", kind: "log", target: 7},
		{name: "trace-args-array-skipped", kind: "trace", inputs: orderParameters("x"), outputs: orderParameters("value"), values: []uint64{0}, target: 7},
		{name: "trace-single-named-tuple", kind: "trace", inputs: orderParameters("x"), outputs: tuple(orderParameters("z", "a")), values: []uint64{7, 7}, target: 7, component: "result", keys: []string{"z", "a"}, path: []string{"result", "z"}},
		{name: "trace-tuple-integer-key-boundaries", kind: "trace", inputs: orderParameters("x"), outputs: tuple(orderParameters(numeric...)), values: []uint64{7, 7, 7, 7, 7, 7, 7}, target: 7, component: "result", keys: numericKeys, path: []string{"result", "2"}},
		{name: "trace-tuple-duplicate-final-value", kind: "trace", inputs: orderParameters("x"), outputs: tuple(orderParameters("z", "a", "z")), values: []uint64{7, 7, 9}, target: 9, component: "result", keys: []string{"z", "a"}, path: []string{"result", "z"}, final: map[string]uint64{"z": 9, "a": 7}},
		{name: "trace-unnamed-tuple-array-skipped", kind: "trace", inputs: orderParameters("x"), outputs: tuple(orderParameters("z", "")), values: []uint64{7, 7}, target: 7},
		{name: "trace-multiple-outputs-array-skipped", kind: "trace", inputs: orderParameters("x"), outputs: orderParameters("z", "a"), values: []uint64{7, 7}, target: 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			event := decodeOrderEvent(t, c)
			body := event["event"].(events.Object)
			order := events.ProfileFieldOrder(event)
			if c.component == "" {
				if order != nil {
					t.Fatal("array/scalar manufactured order", order)
				}
			} else if !reflect.DeepEqual(order, map[string][]string{c.component: c.keys}) {
				t.Fatal("Object.keys mismatch", order, c.keys)
			}
			for key, want := range c.final {
				got := body[c.component].(events.Object)[key].(*big.Int)
				if got.Uint64() != want {
					t.Fatal("actual decoded final duplicate value differs", key, got, want)
				}
			}
			if c.kind == "trace" {
				if _, ok := body["args"].([]any); !ok {
					t.Fatal("trace args not actual array")
				}
				if _, ok := order["args"]; ok {
					t.Fatal("array arguments profiled")
				}
			}
			assertDualProfileOrder(t, event, c.target, order, c.path)
		})
	}
}
func TestDecodedProfileFieldOrderRequiresActualShape(t *testing.T) {
	event := decodeOrderEvent(t, decodedOrderCase{kind: "log", inputs: orderParameters("z", "a"), values: []uint64{7, 7}})
	body := event["event"].(events.Object)
	original := body["args"]
	body["args"] = []any{big.NewInt(7)}
	if events.ProfileFieldOrder(event) != nil {
		t.Fatal("static ABI manufactured array shape")
	}
	body["args"] = events.Object{"z": big.NewInt(7), "a": big.NewInt(7), "unexpected": 1}
	if events.ProfileFieldOrder(event) != nil {
		t.Fatal("unknown insertion order guessed")
	}
	body["args"] = original
	if !reflect.DeepEqual(events.ProfileFieldOrder(event)["args"], []string{"z", "a"}) {
		t.Fatal("immutable ABI changed")
	}
	for _, event := range []events.Object{nil, {}, {"event": events.Object{}}} {
		if events.ProfileFieldOrder(event) != nil {
			t.Fatal("invalid envelope accepted")
		}
	}
}
func TestDecodedProfileFieldOrderOverrideAndConcurrentLearning(t *testing.T) {
	event := decodeOrderEvent(t, decodedOrderCase{kind: "log", inputs: orderParameters("z", "a"), values: []uint64{7, 7}})
	assertDualProfileOrder(t, event, 7, map[string][]string{"args": {"a", "z"}}, []string{"args", "a"})
	session := &filter.Session{}
	f := event["eventCallback"].(events.Object)["filter"].(events.Object)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 1000; n++ {
			session.Update(func() { f["include"] = []string{fmt.Sprint(n)}; f["extra"] = n; delete(f, "extra") })
		}
	}()
	for n := 0; n < 1000; n++ {
		if !reflect.DeepEqual(events.ProfileFieldOrder(event)["args"], []string{"z", "a"}) {
			t.Fatal("learning changed ABI order")
		}
	}
	wg.Wait()
}
