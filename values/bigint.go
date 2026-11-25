// Package values transforms source-shaped Ethereum values.
package values

import (
	"fmt"
	"math/big"
	"reflect"
)

// ReplaceBigInts copies JSON-like maps and lists, calling replace for each
// big integer. Big integers passed to replace are private copies. Go maps
// have no field order. Use a generated struct when callback order matters.
func ReplaceBigInts[R any](value any, replace func(*big.Int) R) (any, error) {
	if replace == nil {
		return nil, fmt.Errorf("missing big integer replacer")
	}
	type ancestor struct {
		kind   reflect.Kind
		ptr    uintptr
		length int
	}
	active := map[ancestor]bool{}
	var visit func(any) (any, error)
	visit = func(value any) (any, error) {
		switch n := value.(type) {
		case *big.Int:
			if n == nil {
				return nil, nil
			}
			return replace(new(big.Int).Set(n)), nil
		case big.Int:
			return replace(new(big.Int).Set(&n)), nil
		}
		v := reflect.ValueOf(value)
		switch v.Kind() {
		case reflect.Slice, reflect.Map:
			if v.IsNil() {
				return nil, nil
			}
			key := ancestor{v.Kind(), v.Pointer(), v.Len()}
			if active[key] {
				return nil, fmt.Errorf("cyclic input in bigint replacement")
			}
			active[key] = true
			defer delete(active, key)
		}
		switch v.Kind() {
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				return nil, fmt.Errorf("expected string map keys, got %s", v.Type())
			}
			copy := make(map[string]any, v.Len())
			for _, k := range v.MapKeys() {
				item, err := visit(v.MapIndex(k).Interface())
				if err != nil {
					return nil, err
				}
				copy[k.String()] = item
			}
			return copy, nil
		case reflect.Array, reflect.Slice:
			copy := make([]any, v.Len())
			for i := range copy {
				item, err := visit(v.Index(i).Interface())
				if err != nil {
					return nil, err
				}
				copy[i] = item
			}
			return copy, nil
		case reflect.Invalid, reflect.Bool, reflect.String, reflect.Float32, reflect.Float64,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return value, nil
		default:
			return nil, fmt.Errorf("expected a source scalar, list or object, got %T", value)
		}
	}
	return visit(value)
}
