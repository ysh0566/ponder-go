package config

// Capture wire objects are explicitly tagged to preserve JS property order and
// avoid marker collisions with real ABI/filter argument object keys.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"

	"ponder.local/ponder-go/filter"
)

type sourceObject struct {
	keys   []string
	values map[string]any
}

func sourceNewObject() *sourceObject { return &sourceObject{values: map[string]any{}} }
func (o *sourceObject) get(key string) any {
	if value, ok := o.values[key]; ok {
		return value
	}
	return filter.Undefined
}
func (o *sourceObject) has(key string) bool { _, ok := o.values[key]; return ok }
func (o *sourceObject) set(key string, value any) {
	if !o.has(key) {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}
func (o *sourceObject) entries() []string { return append([]string{}, o.keys...) }
func (o *sourceObject) clone() *sourceObject {
	result := sourceNewObject()
	for _, key := range o.keys {
		result.set(key, o.values[key])
	}
	return result
}
func (o *sourceObject) overlay(other *sourceObject) *sourceObject {
	result := o.clone()
	for _, key := range other.keys {
		result.set(key, other.values[key])
	}
	return result
}

type sourceFunctionRef struct{ ID int64 }
type sourceRPCRef struct {
	ID        int64
	Hostnames []string
}

func sourceDecodeWire(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return sourceRestoreWire(value)
}
func sourceRestoreWire(value any) (any, error) {
	switch v := value.(type) {
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			restored, err := sourceRestoreWire(item)
			if err != nil {
				return nil, err
			}
			result[i] = restored
		}
		return result, nil
	case map[string]any:
		if len(v) != 1 {
			return nil, fmt.Errorf("source capture object requires one explicit tag")
		}
		if pairs, ok := v["$object"].([]any); ok {
			result := sourceNewObject()
			for _, pair := range pairs {
				items, ok := pair.([]any)
				if !ok || len(items) != 2 {
					return nil, fmt.Errorf("invalid source capture property")
				}
				key, ok := items[0].(string)
				if !ok {
					return nil, fmt.Errorf("source property name must be a string")
				}
				restored, err := sourceRestoreWire(items[1])
				if err != nil {
					return nil, err
				}
				result.set(key, restored)
			}
			return result, nil
		}
		if v["$undefined"] == true {
			return filter.Undefined, nil
		}
		if marker, ok := v["$number"].(string); ok {
			switch marker {
			case "NaN":
				return math.NaN(), nil
			case "+Infinity":
				return math.Inf(1), nil
			case "-Infinity":
				return math.Inf(-1), nil
			case "-0":
				return math.Copysign(0, -1), nil
			}
			return nil, fmt.Errorf("unknown source number marker")
		}
		if value, ok := v["$bigint"].(string); ok {
			integer, ok := new(big.Int).SetString(value, 10)
			if !ok {
				return nil, fmt.Errorf("invalid source bigint")
			}
			return integer, nil
		}
		if value, ok := v["$function"].(json.Number); ok {
			id, err := value.Int64()
			return sourceFunctionRef{ID: id}, err
		}
		// RPC objects require session association with real native clients.
		// Hostnames alone are never used as a substituted mock transport.
		if fields, ok := v["$rpc"].(map[string]any); ok {
			id, ok := fields["id"].(json.Number)
			if !ok {
				return nil, fmt.Errorf("invalid source RPC reference")
			}
			number, err := id.Int64()
			if err != nil {
				return nil, err
			}
			names, ok := fields["hostnames"].([]any)
			if !ok {
				return nil, fmt.Errorf("source RPC hostnames required")
			}
			hosts := make([]string, len(names))
			for i, name := range names {
				host, ok := name.(string)
				if !ok {
					return nil, fmt.Errorf("invalid source RPC hostname")
				}
				hosts[i] = host
			}
			return sourceRPCRef{ID: number, Hostnames: hosts}, nil
		}
		return nil, fmt.Errorf("unknown source capture tag")
	default:
		return value, nil
	}
}
func sourceNullish(value any) bool { return value == nil || filter.IsUndefined(value) }
func sourceType(value any) string {
	if filter.IsUndefined(value) {
		return "undefined"
	}
	switch value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, json.Number:
		return "number"
	case *big.Int:
		return "bigint"
	case sourceFunctionRef:
		return "function"
	default:
		return "object"
	}
}
func sourceNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case json.Number:
		n, err := number.Float64()
		return n, err == nil
	case float64:
		return number, true
	default:
		return 0, false
	}
}
func sourceInteger(value any) bool {
	number, ok := sourceNumber(value)
	return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number
}
func sourceString(value any) string {
	if filter.IsUndefined(value) {
		return "undefined"
	}
	if value == nil {
		return "null"
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case json.Number:
		return v.String()
	case float64:
		if math.IsNaN(v) {
			return "NaN"
		}
		if math.IsInf(v, 1) {
			return "Infinity"
		}
		if math.IsInf(v, -1) {
			return "-Infinity"
		}
		if v == 0 {
			return "0"
		}
		data, _ := json.Marshal(v)
		return string(data)
	case *big.Int:
		return v.String()
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if !sourceNullish(item) {
				parts[i] = sourceString(item)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}
