package abi

import (
	"reflect"
	"strings"
	"testing"
)

func TestReusableOutput(t *testing.T) {
	out := []any{"prefix"}
	params := []Parameter{{Type: "uint8"}, {Type: "uint8"}}
	before := append([]Parameter(nil), params...)
	word := "0x" + strings.Repeat("0", 63) + "1"
	if _, e := DecodeParameters(params, word, DecodeOptions{Out: &out}); e == nil {
		t.Fatal("second parameter should fail")
	}
	if !reflect.DeepEqual(out, []any{"prefix", int64(1)}) {
		t.Fatal("successful decoded prefix was lost", out)
	}
	if !reflect.DeepEqual(params, before) {
		t.Fatal("caller ABI mutated")
	}
	result, e := DecodeParameters(nil, "0x", DecodeOptions{Out: &out})
	if e != nil || !reflect.DeepEqual(result, out) {
		t.Fatalf("blank preserves output: %v %v", result, e)
	}
}

func TestTypedGenerationGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params []Parameter
	}{
		{"UnknownType", []Parameter{{Type: "invalid"}}},
		{"DuplicateName", []Parameter{{Type: "tuple", Components: []Parameter{{Name: "a_b", Type: "bool"}, {Name: "a_b", Type: "bool"}}}}},
		{"HugeArray", []Parameter{{Type: "uint8[4294967295]"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := GenerateParameters("fixture", tc.name, tc.params); e == nil {
				t.Fatal("invalid generated decoder accepted")
			}
		})
	}
	var target = struct{ Value0 bool }{true}
	if e := DecodeParametersInto([]Parameter{{Type: "uint8"}}, "0x"+strings.Repeat("0", 64), &target); e == nil {
		t.Fatal("wrong destination type accepted")
	}
	if !target.Value0 {
		t.Fatal("shape error mutated destination")
	}
}

func TestCallbackTupleCollision(t *testing.T) {
	components := []Parameter{{Name: "a_b", Type: "bool"}, {Name: "aB", Type: "bool"}}
	result, err := CallbackResultType([]Parameter{{Type: "tuple[]", Components: components}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AB_615f62", "AB_6142"} {
		if !strings.Contains(result, name) {
			t.Fatalf("missing independent component %q in %s", name, result)
		}
	}
	type resultRow struct {
		First  bool `abi:"a_b"`
		Second bool `abi:"aB"`
	}
	rows, err := ProjectDecoded[[]resultRow]([]any{map[string]any{"a_b": false, "aB": true}})
	if err != nil || len(rows) != 1 || rows[0].First || !rows[0].Second {
		t.Fatal(rows, err)
	}
}
