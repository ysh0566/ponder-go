// Package events implements the normalized source event construction boundary.
// Object is explicit protocol data; native handler configuration is separate.
package events

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"ponder.local/ponder-go/filter"
)

type Object = map[string]any

func get(o Object, k string) any {
	if v, ok := o[k]; ok {
		return v
	}
	return filter.Undefined
}
func object(v any) Object {
	if o, ok := v.(map[string]any); ok {
		return o
	}
	return nil
}
func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func nullish(v any) bool { return v == nil || filter.IsUndefined(v) }
func lower(v any) any {
	if nullish(v) {
		return v
	}
	return strings.ToLower(text(v))
}
func integer(v any) (*big.Int, error) {
	switch x := v.(type) {
	case *big.Int:
		return new(big.Int).Set(x), nil
	case uint64:
		return new(big.Int).SetUint64(x), nil
	case int:
		return big.NewInt(int64(x)), nil
	case float64:
		if x < 0 || x != float64(uint64(x)) {
			return nil, fmt.Errorf("invalid quantity %v", v)
		}
		return new(big.Int).SetUint64(uint64(x)), nil
	case json.Number:
		v = string(x)
	}
	if s, ok := v.(string); ok {
		base := 10
		if strings.HasPrefix(s, "0x") {
			base = 16
			s = s[2:]
		}
		if n, ok := new(big.Int).SetString(s, base); ok {
			return n, nil
		}
	}
	return nil, fmt.Errorf("invalid integer quantity %v", v)
}
func quantity(v any) (uint64, error) {
	n, e := integer(v)
	if e != nil {
		return 0, e
	}
	if !n.IsUint64() {
		return 0, fmt.Errorf("quantity exceeds uint64")
	}
	return n.Uint64(), nil
}
func mustNumber(v any) uint64 { n, _ := quantity(v); return n }
func copyFields(input Object, keys ...string) Object {
	o := Object{}
	for _, k := range keys {
		o[k] = get(input, k)
	}
	return o
}
func bigFields(o, input Object, keys ...string) error {
	for _, k := range keys {
		n, e := integer(get(input, k))
		if e != nil {
			return fmt.Errorf("%s: %w", k, e)
		}
		o[k] = n
	}
	return nil
}
func numberFields(o, input Object, keys ...string) error {
	for _, k := range keys {
		n, e := quantity(get(input, k))
		if e != nil {
			return fmt.Errorf("%s: %w", k, e)
		}
		o[k] = n
	}
	return nil
}
func optionalBig(v any) (any, error) {
	if nullish(v) || v == "" {
		return nil, nil
	}
	return integer(v)
}
func transactionType(v any) any {
	switch v {
	case "0x0":
		return "legacy"
	case "0x1":
		return "eip2930"
	case "0x2":
		return "eip1559"
	case "0x7e":
		return "deposit"
	}
	return v
}
func indexKey(o Object) string {
	return strconv.FormatUint(mustNumber(o["blockNumber"]), 10) + ":" + strconv.FormatUint(mustNumber(o["transactionIndex"]), 10)
}
