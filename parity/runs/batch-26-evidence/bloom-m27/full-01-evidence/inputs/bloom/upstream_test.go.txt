package bloom

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"ponder.local/ponder-go/filter"
)

type capturedCall struct {
	Operation string
	Args      []any
	ArgsAfter []any `json:"args_after"`
	Result    any
	Error     *struct{ Name, Message string }
}

func restore(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = restore(item)
		}
		return out
	case map[string]any:
		if v["$undefined"] == true {
			return filter.Undefined
		}
		if data, ok := v["$uint8Array"].([]any); ok {
			out := make([]byte, len(data))
			for i, n := range data {
				out[i] = byte(n.(float64))
			}
			return out
		}
		out := filter.Object{}
		for key, item := range v {
			out[key] = restore(item)
		}
		return out
	default:
		return value
	}
}
func mark(value any) any {
	if filter.IsUndefined(value) {
		return map[string]any{"$undefined": true}
	}
	switch v := value.(type) {
	case []byte:
		data := make([]int, len(v))
		for i, n := range v {
			data[i] = int(n)
		}
		return map[string]any{"$uint8Array": data}
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = mark(item)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			out[key] = mark(item)
		}
		return out
	case Bits:
		return map[string]any{"byte0": v.Byte0, "mask0": v.Mask0, "byte1": v.Byte1, "mask1": v.Mask1, "byte2": v.Byte2, "mask2": v.Mask2}
	case []Bits:
		if v == nil {
			return map[string]any{"$undefined": true}
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = mark(item)
		}
		return out
	case LogFilterBloom:
		topics := make([]any, len(v.Topics))
		for i, topic := range v.Topics {
			topics[i] = mark(topic)
		}
		return map[string]any{"address": mark(v.Address), "topics": topics}
	case FactoryBloom:
		return map[string]any{"address": mark(v.Address), "eventSelector": mark(v.EventSelector)}
	default:
		return value
	}
}
func bitsFrom(value any) Bits {
	data, _ := json.Marshal(value)
	var bits Bits
	if err := json.Unmarshal(data, &bits); err != nil {
		panic(err)
	}
	return bits
}
func alternatives(value any) []Bits {
	if filter.IsUndefined(value) {
		return nil
	}
	xs := value.([]any)
	out := make([]Bits, len(xs))
	for i, item := range xs {
		out[i] = bitsFrom(item)
	}
	return out
}
func logBloomFrom(value any) LogFilterBloom {
	o := value.(map[string]any)
	out := LogFilterBloom{Address: alternatives(o["address"])}
	for i, item := range o["topics"].([]any) {
		out.Topics[i] = alternatives(item)
	}
	return out
}
func factoryBloomFrom(value any) FactoryBloom {
	o := value.(map[string]any)
	return FactoryBloom{Address: alternatives(o["address"]), EventSelector: bitsFrom(o["eventSelector"])}
}
func replay(t *testing.T, call capturedCall) any {
	t.Helper()
	args := restore(call.Args).([]any)
	var actual any
	var err error
	switch call.Operation {
	case "getBloomBits":
		actual, err = GetBloomBits(args[0].(string))
	case "isBloomBitsInBloom":
		actual = IsBloomBitsInBloom(args[0].([]byte), bitsFrom(args[1]))
	case "isInBloom":
		actual, err = IsInBloom(args[0].([]byte), args[1].(string))
	case "getLogFilterBloom":
		actual, err = GetLogFilterBloomObject(args[0].(map[string]any))
	case "isFilterInBloom":
		o := args[0].(map[string]any)
		actual = IsFilterInBloom(o["bloom"].([]byte), logBloomFrom(o["filter"]))
	case "getFactoryBloom":
		actual, err = GetFactoryBloom(args[0].(map[string]any))
	case "isFactoryInBloom":
		o := args[0].(map[string]any)
		actual = IsFactoryInBloom(o["bloom"].([]byte), factoryBloomFrom(o["factory"]))
	default:
		t.Fatal("unknown captured source operation", call.Operation)
	}
	if call.Error != nil {
		if err == nil {
			t.Fatal("source error missing", call.Operation, call.Error)
		}
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(mark(actual))
	want, _ := json.Marshal(call.Result)
	if string(got) != string(want) {
		t.Fatalf("%s complete source result differs\ngot%s\nwant%s", call.Operation, got, want)
	}
	after, _ := json.Marshal(mark(args))
	expectedAfter, _ := json.Marshal(call.ArgsAfter)
	if string(after) != string(expectedAfter) {
		t.Fatalf("%s mutation differs\ngot%s\nwant%s", call.Operation, after, expectedAfter)
	}
	return actual
}

// All eight declarations, original complete inputs/results and twelve explicit
// original boolean assertions. The two invalid topic0 runtime inputs retain the
// separate required public API compile-failure obligations.
func TestUpstreamBloom(t *testing.T) {
	data, err := os.ReadFile("../testdata/bloom/captured-original-blooms.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			ID, Title string
			Calls     []capturedCall
		}
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 8 {
		t.Fatal("expected all eight original declarations")
	}
	expected := map[string][]bool{
		"isInBloom": {true, true, false, false, true},
		"isFilterInBloom returns false for missing topics":                      {false},
		"isFilterInBloom returns true for undefined address":                    {true},
		"isFilterInBloom returns false for factory with only new child address": {false},
		"isFilterInBloom returns true for factory without new child address":    {true},
		"isFilterInBloom returns true for array of addresses":                   {true},
		"isFactoryInBloom returns true for factory in bloom":                    {true},
		"isFactoryInBloom returns false for missing factory address":            {false},
	}
	for _, c := range fixture.Cases {
		t.Run(c.Title, func(t *testing.T) {
			actual := []bool{}
			primary := strings.Split(c.Title, " ")[0]
			for _, call := range c.Calls {
				value := replay(t, call)
				if b, ok := value.(bool); ok && call.Operation == primary {
					actual = append(actual, b)
				}
			}
			want, ok := expected[c.Title]
			if !ok || !reflect.DeepEqual(actual, want) {
				t.Fatal("original boolean assertions", actual, want)
			}
		})
	}
}

func TestBloomDifferential(t *testing.T) {
	data, err := os.ReadFile("../testdata/bloom/differential.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name string
			capturedCall
		}
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 100 {
		t.Fatal("source vectors incomplete")
	}
	grouped := map[string][]struct {
		Name string
		capturedCall
	}{}
	for _, c := range fixture.Cases {
		grouped[c.Operation] = append(grouped[c.Operation], c)
	}
	for operation, cases := range grouped {
		t.Run(operation, func(t *testing.T) {
			for _, c := range cases {
				func() {
					defer func() {
						if p := recover(); p != nil {
							t.Fatalf("vector %s: %v", c.Name, p)
						}
					}()
					replay(t, c.capturedCall)
				}()
			}
		})
	}
}
