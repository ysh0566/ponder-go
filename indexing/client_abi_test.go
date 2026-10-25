package indexing

import (
	"encoding/hex"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ponderabi "ponder.local/ponder-go/abi"
)

func actionTestABI(t *testing.T, text string) ponderabi.ABI {
	t.Helper()
	a, err := ponderabi.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestContractABIEncodingAndSelection(t *testing.T) {
	a := actionTestABI(t, `[
 {"type":"function","name":"balanceOf","inputs":[{"name":"owner","type":"address"}],"outputs":[{"type":"uint256"}]},
 {"type":"function","name":"pick","inputs":[{"type":"bool"}],"outputs":[{"type":"bool"}]},
 {"type":"function","name":"pick","inputs":[{"type":"uint256"}],"outputs":[{"type":"uint256"}]},
 {"type":"function","name":"pick","inputs":[{"type":"uint8"}],"outputs":[{"type":"uint8"}]}]`)
	addr := "0x0000000000000000000000000000000000000001"
	encoded, f, err := encodeContractCall(ContractCall{ABI: a, FunctionName: "balanceOf", Args: []any{addr}})
	if err != nil || encoded != "0x70a08231"+strings.Repeat("0", 63)+"1" || f.Signature() != "balanceOf(address)" {
		t.Fatalf("encoding %s %s %v", encoded, f.Signature(), err)
	}
	for _, name := range []string{"balanceOf", f.Signature(), ponderabi.FunctionSelector(f)} {
		got, _, err := encodeContractCall(ContractCall{ABI: a, FunctionName: name, Args: []any{common.HexToAddress(addr)}})
		if err != nil || got != encoded {
			t.Fatalf("selection %s: %s %v", name, got, err)
		}
	}
	_, f, err = encodeContractCall(ContractCall{ABI: a, FunctionName: "pick", Args: []any{true}})
	if err != nil || f.Signature() != "pick(bool)" {
		t.Fatalf("bool overload %v %v", f, err)
	}
	_, f, err = encodeContractCall(ContractCall{ABI: a, FunctionName: "pick", Args: []any{big.NewInt(7)}})
	if err != nil || f.Signature() != "pick(uint8)" {
		t.Fatalf("last matching overload %v %v", f, err)
	}
	if _, _, err = encodeContractCall(ContractCall{ABI: a, FunctionName: "pick", Args: []any{big.NewInt(256)}}); err == nil {
		t.Fatal("range error silently fell back to uint256 overload")
	}
	if _, _, err = encodeContractCall(ContractCall{ABI: a, FunctionName: "pick(uint256)", Args: []any{big.NewInt(256)}}); err != nil {
		t.Fatal(err)
	}
	for _, call := range []ContractCall{{ABI: a, FunctionName: "missing"}, {ABI: a, FunctionName: "balanceOf"}, {ABI: a, FunctionName: "balanceOf", Args: []any{"0x01"}}, {ABI: a, FunctionName: "pick(uint8)", Args: []any{-1}}, {ABI: a, FunctionName: "pick(uint8)", Args: []any{1.5}}} {
		if _, _, err := encodeContractCall(call); err == nil {
			t.Fatalf("invalid call accepted %+v", call)
		}
	}
}

func TestContractABIAmbiguousOverloads(t *testing.T) {
	for _, other := range []string{"bytes20", "bytes", "string"} {
		t.Run(other, func(t *testing.T) {
			a := actionTestABI(t, `[{"type":"function","name":"f","inputs":[{"type":"address"}],"outputs":[]},{"type":"function","name":"f","inputs":[{"type":"`+other+`"}],"outputs":[]}]`)
			_, _, err := encodeContractCall(ContractCall{ABI: a, FunctionName: "f", Args: []any{"0x0000000000000000000000000000000000000001"}})
			if err == nil || !strings.Contains(err.Error(), "ambiguous") {
				t.Fatalf("ambiguity %v", err)
			}
		})
	}
}

func TestContractABITupleArraysAndSignedBounds(t *testing.T) {
	a := actionTestABI(t, `[{"type":"function","name":"f","inputs":[{"name":"items","type":"tuple[]","components":[{"name":"amount","type":"int16"},{"name":"data","type":"bytes3"},{"name":"flags","type":"bool[2]"}]}],"outputs":[]}]`)
	arg := []any{map[string]any{"amount": -32768, "data": "0x010203", "flags": []bool{true, false}}, map[string]any{"amount": 32767, "data": [3]byte{4, 5, 6}, "flags": [2]bool{false, true}}}
	encoded, f, err := encodeContractCall(ContractCall{ABI: a, FunctionName: "f", Args: []any{arg}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := f.DecodeCall(encoded, "0x")
	if err != nil {
		t.Fatal(err)
	}
	expected := []any{[]any{map[string]any{"amount": int64(-32768), "data": "0x010203", "flags": []any{true, false}}, map[string]any{"amount": int64(32767), "data": "0x040506", "flags": []any{false, true}}}}
	if !reflect.DeepEqual(decoded, expected) {
		t.Fatalf("tuple values %#v", decoded)
	}
	for _, bad := range []any{map[string]any{"amount": -32769, "data": "0x010203", "flags": []bool{true, false}}, map[string]any{"amount": 0, "data": "0x0102", "flags": []bool{true, false}}, map[string]any{"amount": 0, "data": "0x010203", "flags": []bool{true}}, map[string]any{"data": "0x010203", "flags": []bool{true, false}}} {
		if _, _, err := encodeContractCall(ContractCall{ABI: a, FunctionName: "f", Args: []any{[]any{bad}}}); err == nil {
			t.Fatalf("invalid tuple accepted %#v", bad)
		}
	}
}

func TestContractABIResultShapesAndZeroData(t *testing.T) {
	a := actionTestABI(t, `[
 {"type":"function","name":"zero","inputs":[],"outputs":[]},
 {"type":"function","name":"one","inputs":[{"type":"address"}],"outputs":[{"type":"uint256"}]},
 {"type":"function","name":"multi","inputs":[],"outputs":[{"type":"uint8"},{"type":"bool"}]},
 {"type":"function","name":"tuple","inputs":[],"outputs":[{"type":"tuple","components":[{"name":"value","type":"uint256"},{"name":"owner","type":"address"}]}]}]`)
	for _, test := range []struct {
		name   string
		values []any
		want   any
	}{
		{"zero", nil, nil}, {"one", []any{big.NewInt(42)}, big.NewInt(42)}, {"multi", []any{uint8(7), true}, []any{int64(7), true}},
		{"tuple", []any{struct {
			Value *big.Int
			Owner common.Address
		}{big.NewInt(9), common.HexToAddress("0x1")}}, map[string]any{"value": big.NewInt(9), "owner": "0x0000000000000000000000000000000000000001"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := a.Methods[test.name]
			bytes, err := m.Outputs.Pack(test.values...)
			if err != nil {
				t.Fatal(err)
			}
			var f ponderabi.Function
			for _, item := range a.FunctionEntries() {
				if item.Name == test.name {
					f = item
				}
			}
			got, err := decodeContractResult(f, "0x"+hex.EncodeToString(bytes))
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %#v want %#v err %v", got, test.want, err)
			}
			if len(f.Outputs) > 0 {
				_, err = decodeContractResult(f, "0x")
				var zero *ContractFunctionZeroDataError
				if !errors.As(err, &zero) || zero.FunctionName != f.Name {
					t.Fatalf("typed zero-data error %v", err)
				}
			}
			for _, invalid := range []string{"", "00", "0xz1", "0x0"} {
				if _, err := decodeContractResult(f, invalid); err == nil {
					t.Fatalf("invalid result %q accepted", invalid)
				}
			}
		})
	}
}

func TestContractABIUnnamedTupleResult(t *testing.T) {
	// Existing ABI.Parse uses geth, which rejects anonymous tuple fields. The
	// output decoder nevertheless supports source metadata's unnamed tuple shape.
	f := ponderabi.Function{Type: "function", Name: "f", Outputs: []ponderabi.Parameter{{Type: "tuple", Components: []ponderabi.Parameter{{Name: "", HasName: true, Type: "uint8"}, {Name: "yes", HasName: true, Type: "bool"}}}}}
	got, err := decodeContractResult(f, "0x"+strings.Repeat("0", 63)+"3"+strings.Repeat("0", 63)+"1")
	if err != nil || !reflect.DeepEqual(got, []any{int64(3), true}) {
		t.Fatalf("unnamed tuple %#v %v", got, err)
	}
}

func TestContractABINumericWidthsAndInputSnapshot(t *testing.T) {
	a := actionTestABI(t, `[{"type":"function","name":"f","inputs":[{"type":"int24"},{"type":"uint64"},{"type":"bytes"},{"type":"uint16[]"}],"outputs":[]}]`)
	n := new(big.Int).SetUint64(^uint64(0))
	b := []byte{1, 2}
	array := []uint16{3, 4}
	encoded, f, err := encodeContractCall(ContractCall{ABI: a, FunctionName: "f", Args: []any{int32(-8388608), n, b, array}})
	if err != nil {
		t.Fatal(err)
	}
	n.SetInt64(0)
	b[0] = 9
	array[0] = 9
	args, _, err := f.DecodeCall(encoded, "0x")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []any{int64(-8388608), new(big.Int).SetUint64(^uint64(0)), "0x0102", []any{int64(3), int64(4)}}) {
		t.Fatalf("encoded snapshot %#v", args)
	}
	if _, _, err := encodeContractCall(ContractCall{ABI: a, FunctionName: "f", Args: []any{int32(-8388609), uint64(0), []byte{}, []uint16{}}}); err == nil {
		t.Fatal("int24 underflow accepted")
	}
}
