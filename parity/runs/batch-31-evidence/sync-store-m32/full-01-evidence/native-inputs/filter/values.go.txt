// Package filter implements fixed-source filter/fragment semantics. Object is
// the explicit dynamic utility API; typed native configuration is separate.
package filter

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Object = map[string]any
type undefinedValue struct{}

var Undefined = undefinedValue{}

func (undefinedValue) MarshalJSON() ([]byte, error) { return []byte(`{"$undefined":true}`), nil }
func IsUndefined(v any) bool                        { _, ok := v.(undefinedValue); return ok }
func nullish(v any) bool                            { return v == nil || IsUndefined(v) }
func object(v any) Object {
	if o, ok := v.(map[string]any); ok {
		return o
	}
	return Object{}
}
func get(o Object, key string) any {
	if v, ok := o[key]; ok {
		return v
	}
	return Undefined
}
func nullable(v any) any {
	if nullish(v) {
		return nil
	}
	return v
}
func arr(v any) ([]any, bool) {
	if xs, ok := v.([]any); ok {
		return xs, true
	}
	if xs, ok := v.([]string); ok {
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = x
		}
		return out, true
	}
	return nil, false
}
func values(v any) []any {
	if xs, ok := arr(v); ok {
		return xs
	}
	return []any{v}
}
func truth(v any) bool {
	if nullish(v) {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case int:
		return x != 0
	case uint64:
		return x != 0
	}
	return true
}
func str(v any) string {
	if IsUndefined(v) {
		return "undefined"
	}
	if v == nil {
		return "null"
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case map[string]any:
		return "[object Object]"
	}
	return fmt.Sprint(v)
}
func num(v any) float64 {
	if v == nil {
		return 0
	}
	if IsUndefined(v) {
		return math.NaN()
	}
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case uint64:
		return float64(x)
	case json.Number:
		n, _ := x.Float64()
		return n
	}
	text := str(v)
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
		n, e := strconv.ParseUint(text[2:], 16, 64)
		if e != nil {
			return math.NaN()
		}
		return float64(n)
	}
	n, e := strconv.ParseFloat(text, 64)
	if e != nil {
		return math.NaN()
	}
	return n
}
func clone(o Object) Object {
	r := Object{}
	for k, v := range o {
		r[k] = v
	}
	return r
}
func dedupe(xs []any) []any {
	out := []any{}
	seen := map[string]bool{}
	for _, v := range xs {
		key := fmt.Sprintf("%T:%s", v, str(v))
		if !seen[key] {
			seen[key] = true
			out = append(out, v)
		}
	}
	return out
}

// FixtureValue decodes lossless fixture tags; ordinary JSON null stays nil.
func FixtureValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if x["$undefined"] == true && len(x) == 1 {
			return Undefined
		}
		if n, ok := x["$number"].(string); ok && len(x) == 1 {
			switch n {
			case "NaN":
				return math.NaN()
			case "Infinity":
				return math.Inf(1)
			case "-Infinity":
				return math.Inf(-1)
			}
		}
		o := Object{}
		for k, v := range x {
			o[k] = FixtureValue(v)
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = FixtureValue(v)
		}
		return out
	}
	return v
}

// RPCValue follows JSON.stringify: undefined object properties are omitted and
// undefined array elements become null. Never send fixture tags to an RPC.
func RPCValue(v any) any {
	switch x := v.(type) {
	case undefinedValue:
		return nil
	case map[string]any:
		o := Object{}
		for k, v := range x {
			if !IsUndefined(v) {
				o[k] = RPCValue(v)
			}
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = RPCValue(v)
		}
		return out
	}
	return v
}
