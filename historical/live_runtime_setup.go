package historical

import (
	"fmt"
	"math/big"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"reflect"
)

func freezeRuntimeSetup(b *config.ExecutableSourceIndexingBuild) error {
	groups := make([][]indexing.Object, len(b.SetupCallbacks))
	for i, group := range b.SetupCallbacks {
		groups[i] = make([]indexing.Object, len(group))
		for j, cb := range group {
			if err := validateHandler(cb); err != nil {
				return err
			}
			chain, ok := cb["chain"].(map[string]any)
			id, ie := uintQuantity(chain["id"])
			want, we := uintQuantity(b.Chains[i]["id"])
			name, nok := chain["name"].(string)
			expected, eok := b.Chains[i]["name"].(string)
			if !ok || ie != nil || we != nil || id != want || !nok || !eok || name == "" || name != expected {
				return fmt.Errorf("runtime setup chain mismatch")
			}
			n := uint64(0)
			v := cb["block"]
			if v != nil && !filter.IsUndefined(v) {
				var err error
				n, err = uintQuantity(v)
				if err != nil || n > 9007199254740991 {
					return fmt.Errorf("invalid runtime setup RPC block")
				}
			}
			copy := indexing.Object{}
			for k, v := range cb {
				copy[k] = v
			}
			copy["block"] = n
			copy["chain"] = b.Chains[i]
			groups[i][j] = copy
		}
	}
	b.SetupCallbacks = groups
	return nil
}

// The managed plan validates metadata/cycles before this copier is used.
func frozenSetupValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	if v.CanInterface() {
		if filter.IsUndefined(v.Interface()) {
			return v
		}
		if n, ok := v.Interface().(*big.Int); ok {
			if n == nil {
				return v
			}
			return reflect.ValueOf(new(big.Int).Set(n))
		}
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		o := reflect.New(v.Type()).Elem()
		o.Set(frozenSetupValue(v.Elem()))
		return o
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		o := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			o.SetMapIndex(it.Key(), frozenSetupValue(it.Value()))
		}
		return o
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		o := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			o.Index(i).Set(frozenSetupValue(v.Index(i)))
		}
		return o
	case reflect.Array:
		o := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			o.Index(i).Set(frozenSetupValue(v.Index(i)))
		}
		return o
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		o := reflect.New(v.Type().Elem())
		o.Elem().Set(frozenSetupValue(v.Elem()))
		return o
	}
	return v
}
