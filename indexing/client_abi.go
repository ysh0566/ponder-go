package indexing

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"

	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ponderabi "ponder.local/ponder-go/abi"
)

// ContractCall describes a read-only ABI function call. FunctionName accepts a
// name or selector; canonical signatures are also accepted as a Go convenience.
type ContractCall struct {
	ABI          ponderabi.ABI
	Address      string
	FunctionName string
	Args         []any
}

// ContractFunctionZeroDataError marks a successfully transported eth_call whose
// empty result cannot satisfy the selected function's nonempty outputs.
type ContractFunctionZeroDataError struct{ FunctionName string }

func (e *ContractFunctionZeroDataError) Error() string {
	return fmt.Sprintf("function %s returned no data (0x)", e.FunctionName)
}

func encodeContractCall(call ContractCall) (string, ponderabi.Function, error) {
	var candidates []ponderabi.Function
	for _, f := range call.ABI.FunctionEntries() {
		if f.Name == call.FunctionName || f.Signature() == call.FunctionName || ponderabi.FunctionSelector(f) == call.FunctionName {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) == 0 {
		return "", ponderabi.Function{}, fmt.Errorf("ABI function %q not found", call.FunctionName)
	}
	selected := candidates[0]
	matched := false
	// viem selects the last type-compatible overload (not the first that packs).
	// Range and byte-length errors are checked only after this selection.
	if len(candidates) > 1 {
		for _, f := range candidates {
			if len(f.Inputs) != len(call.Args) {
				continue
			}
			if len(call.Args) == 0 {
				selected = f
				break
			}
			ok := true
			for i, p := range f.Inputs {
				if !contractArgMatches(call.Args[i], p) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			if matched && contractAmbiguous(f.Inputs, selected.Inputs, call.Args) {
				return "", ponderabi.Function{}, fmt.Errorf("ambiguous ABI overloads %s and %s", f.Signature(), selected.Signature())
			}
			selected, matched = f, true
		}
	}
	var method *ethabi.Method
	for _, m := range call.ABI.Methods {
		if m.Sig == selected.Signature() {
			copy := m
			method = &copy
			break
		}
	}
	if method == nil {
		return "", selected, fmt.Errorf("ABI method metadata missing for %s", selected.Signature())
	}
	if len(method.Inputs) != len(call.Args) {
		return "", selected, fmt.Errorf("%s expects %d arguments, got %d", selected.Signature(), len(method.Inputs), len(call.Args))
	}
	args := make([]any, len(call.Args))
	for i, arg := range call.Args {
		value, err := contractNativeValue(method.Inputs[i].Type, arg)
		if err != nil {
			return "", selected, fmt.Errorf("%s argument %d: %w", selected.Signature(), i, err)
		}
		args[i] = value.Interface()
	}
	encoded, err := method.Inputs.Pack(args...)
	if err != nil {
		return "", selected, err
	}
	return "0x" + hex.EncodeToString(append(append([]byte{}, method.ID...), encoded...)), selected, nil
}

func decodeContractResult(f ponderabi.Function, encoded string) (any, error) {
	if encoded == "0x" && len(f.Outputs) > 0 {
		return nil, &ContractFunctionZeroDataError{FunctionName: f.Name}
	}
	if !strings.HasPrefix(encoded, "0x") || len(encoded)%2 != 0 {
		return nil, fmt.Errorf("invalid ABI result hex")
	}
	if _, err := hex.DecodeString(encoded[2:]); err != nil {
		return nil, fmt.Errorf("invalid ABI result hex: %w", err)
	}
	// DecodeCall normalizes tuple name presence for viem. Only its output decoder
	// is needed here; remove inputs from a copy and use that copy's selector.
	resultOnly := f
	resultOnly.Inputs = nil
	_, values, err := resultOnly.DecodeCall(ponderabi.FunctionSelector(resultOnly), encoded)
	if err != nil {
		return nil, err
	}
	switch len(values) {
	case 0:
		return nil, nil
	case 1:
		return values[0], nil
	default:
		return values, nil
	}
}

func contractArgMatches(value any, p ponderabi.Parameter) bool {
	if strings.HasSuffix(p.Type, "]") {
		i := strings.LastIndexByte(p.Type, '[')
		if i < 0 {
			return false
		}
		v := reflect.ValueOf(value)
		if !v.IsValid() || v.Kind() != reflect.Array && v.Kind() != reflect.Slice {
			return false
		}
		p.Type = p.Type[:i]
		for j := 0; j < v.Len(); j++ {
			if !contractArgMatches(v.Index(j).Interface(), p) {
				return false
			}
		}
		return true
	}
	switch p.Type {
	case "address":
		_, ok := contractAddress(value)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "tuple":
		for i, c := range p.Components {
			v, ok := contractTupleValue(value, c.Name, i)
			if !ok || !contractArgMatches(v, c) {
				return false
			}
		}
		return true
	case "function":
		_, ok := value.(string)
		return ok
	}
	if strings.HasPrefix(p.Type, "uint") || strings.HasPrefix(p.Type, "int") {
		_, err := contractInteger(value)
		return err == nil
	}
	if strings.HasPrefix(p.Type, "bytes") {
		_, s := value.(string)
		if s {
			return true
		}
		v := reflect.ValueOf(value)
		return v.IsValid() && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) && v.Type().Elem().Kind() == reflect.Uint8
	}
	return false
}

func contractAmbiguous(a, b []ponderabi.Parameter, args []any) bool {
	for i, p := range a {
		if i >= len(b) || i >= len(args) {
			return false
		}
		q := b[i]
		if p.Type == "tuple" && q.Type == "tuple" {
			nested := make([]any, len(p.Components))
			for j, c := range p.Components {
				nested[j], _ = contractTupleValue(args[i], c.Name, j)
			}
			return contractAmbiguous(p.Components, q.Components, nested)
		}
		if p.Type != "address" && q.Type != "address" {
			continue
		}
		other := q.Type
		if q.Type == "address" {
			other = p.Type
		}
		if other == "bytes20" {
			return true
		}
		if other == "string" || other == "bytes" {
			if _, ok := contractAddress(args[i]); ok {
				return true
			}
		}
	}
	return false
}

func contractAddress(value any) (common.Address, bool) {
	if a, ok := value.(common.Address); ok {
		return a, true
	}
	s, ok := value.(string)
	if !ok || !common.IsHexAddress(s) || !strings.HasPrefix(s, "0x") {
		return common.Address{}, false
	}
	return common.HexToAddress(s), true
}

func contractInteger(value any) (*big.Int, error) {
	switch v := value.(type) {
	case *big.Int:
		if v != nil {
			return new(big.Int).Set(v), nil
		}
	case big.Int:
		return new(big.Int).Set(&v), nil
	case json.Number:
		if n, ok := new(big.Int).SetString(string(v), 10); ok {
			return n, nil
		}
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) && math.Trunc(v) == v && math.Abs(v) <= 9007199254740991 {
			return big.NewInt(int64(v)), nil
		}
	default:
		r := reflect.ValueOf(value)
		if r.IsValid() {
			switch r.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				return big.NewInt(r.Int()), nil
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				return new(big.Int).SetUint64(r.Uint()), nil
			}
		}
	}
	return nil, fmt.Errorf("expected exact integer, got %T", value)
}

func contractTupleValue(value any, name string, index int) (any, bool) {
	if object, ok := value.(map[string]any); ok {
		v, exists := object[name]
		return v, exists
	}
	r := reflect.ValueOf(value)
	if !r.IsValid() {
		return nil, false
	}
	if r.Kind() == reflect.Ptr {
		if r.IsNil() {
			return nil, false
		}
		r = r.Elem()
	}
	switch r.Kind() {
	case reflect.Slice, reflect.Array:
		if index < r.Len() {
			return r.Index(index).Interface(), true
		}
	case reflect.Struct:
		f := r.FieldByName(ethabi.ToCamelCase(name))
		if f.IsValid() && f.CanInterface() {
			return f.Interface(), true
		}
	}
	return nil, false
}

func contractNativeValue(t ethabi.Type, value any) (reflect.Value, error) {
	fail := func() (reflect.Value, error) {
		return reflect.Value{}, fmt.Errorf("invalid %s argument (%T)", t.String(), value)
	}
	switch t.T {
	case ethabi.IntTy, ethabi.UintTy:
		n, err := contractInteger(value)
		if err != nil {
			return reflect.Value{}, err
		}
		min, max := new(big.Int), new(big.Int)
		if t.T == ethabi.UintTy {
			max.Sub(new(big.Int).Lsh(big.NewInt(1), uint(t.Size)), big.NewInt(1))
		} else {
			max.Lsh(big.NewInt(1), uint(t.Size-1))
			min.Neg(max)
			max.Sub(max, big.NewInt(1))
		}
		if n.Cmp(min) < 0 || n.Cmp(max) > 0 {
			return reflect.Value{}, fmt.Errorf("integer outside %s range", t.String())
		}
		if t.GetType() == reflect.TypeOf((*big.Int)(nil)) {
			return reflect.ValueOf(n), nil
		}
		r := reflect.New(t.GetType()).Elem()
		if t.T == ethabi.UintTy {
			r.SetUint(n.Uint64())
		} else {
			r.SetInt(n.Int64())
		}
		return r, nil
	case ethabi.AddressTy:
		a, ok := contractAddress(value)
		if !ok {
			return fail()
		}
		return reflect.ValueOf(a), nil
	case ethabi.BoolTy:
		v, ok := value.(bool)
		if !ok {
			return fail()
		}
		return reflect.ValueOf(v), nil
	case ethabi.StringTy:
		v, ok := value.(string)
		if !ok {
			return fail()
		}
		return reflect.ValueOf(v), nil
	case ethabi.BytesTy, ethabi.FixedBytesTy, ethabi.FunctionTy:
		var bytes []byte
		if s, ok := value.(string); ok {
			if !strings.HasPrefix(s, "0x") {
				return fail()
			}
			var err error
			bytes, err = hex.DecodeString(s[2:])
			if err != nil {
				return fail()
			}
		} else {
			r := reflect.ValueOf(value)
			if !r.IsValid() || (r.Kind() != reflect.Array && r.Kind() != reflect.Slice) || r.Type().Elem().Kind() != reflect.Uint8 {
				return fail()
			}
			bytes = make([]byte, r.Len())
			for i := range bytes {
				bytes[i] = byte(r.Index(i).Uint())
			}
		}
		if t.T == ethabi.BytesTy {
			return reflect.ValueOf(bytes), nil
		}
		if len(bytes) != t.GetType().Len() {
			return fail()
		}
		r := reflect.New(t.GetType()).Elem()
		reflect.Copy(r, reflect.ValueOf(bytes))
		return r, nil
	case ethabi.SliceTy, ethabi.ArrayTy:
		v := reflect.ValueOf(value)
		if !v.IsValid() || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) {
			return fail()
		}
		var r reflect.Value
		if t.T == ethabi.ArrayTy {
			if v.Len() != t.Size {
				return fail()
			}
			r = reflect.New(t.GetType()).Elem()
		} else {
			r = reflect.MakeSlice(t.GetType(), v.Len(), v.Len())
		}
		for i := 0; i < v.Len(); i++ {
			x, err := contractNativeValue(*t.Elem, v.Index(i).Interface())
			if err != nil {
				return reflect.Value{}, fmt.Errorf("element %d: %w", i, err)
			}
			r.Index(i).Set(x)
		}
		return r, nil
	case ethabi.TupleTy:
		r := reflect.New(t.GetType()).Elem()
		for i, elem := range t.TupleElems {
			v, ok := contractTupleValue(value, t.TupleRawNames[i], i)
			if !ok {
				return fail()
			}
			x, err := contractNativeValue(*elem, v)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("tuple member %d: %w", i, err)
			}
			r.Field(i).Set(x)
		}
		return r, nil
	}
	return fail()
}
