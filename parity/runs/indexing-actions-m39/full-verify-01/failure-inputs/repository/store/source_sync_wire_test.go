package store

// Test-only live language bridge. Captured expected objects never seed native
// state; new source output IDs bind only to recursively verified native objects.
import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"

	"encoding/json"
	"ponder.local/ponder-go/filter"
)

type sourceLivePair struct{ Key, Value any }
type sourceLiveMap []sourceLivePair
type sourceLiveRegistry struct{ refs map[int64]filter.Object }

func newSourceLiveRegistry() *sourceLiveRegistry {
	return &sourceLiveRegistry{refs: map[int64]filter.Object{}}
}
func sourceLiveNumber(value any) (float64, error) {
	switch v := value.(type) {
	case json.Number:
		return v.Float64()
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("not a source number: %T", value)
	}
}
func sourceLiveID(value any) (int64, error) {
	n, e := sourceLiveNumber(value)
	if e != nil || math.Trunc(n) != n {
		return 0, fmt.Errorf("invalid source object id")
	}
	return int64(n), nil
}
func (r *sourceLiveRegistry) restore(value any) (any, error) {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			x, e := r.restore(item)
			if e != nil {
				return nil, e
			}
			out[i] = x
		}
		return out, nil
	case map[string]any:
		if v["$undefined"] == true {
			return filter.Undefined, nil
		}
		if text, ok := v["$bigint"].(string); ok {
			n, ok := new(big.Int).SetString(text, 10)
			if !ok {
				return nil, fmt.Errorf("invalid source bigint")
			}
			return n, nil
		}
		if text, ok := v["$number"].(string); ok {
			switch text {
			case "NaN":
				return math.NaN(), nil
			case "Infinity":
				return math.Inf(1), nil
			case "-Infinity":
				return math.Inf(-1), nil
			case "-0":
				return math.Copysign(0, -1), nil
			}
			return nil, fmt.Errorf("unknown source number")
		}
		if idValue, ok := v["$ref"]; ok {
			id, e := sourceLiveID(idValue)
			if e != nil {
				return nil, e
			}
			object, ok := r.refs[id]
			if !ok {
				return nil, fmt.Errorf("source input references unbound native object %d", id)
			}
			return object, nil
		}
		if entries, ok := v["$map"].([]any); ok {
			out := sourceLiveMap{}
			for _, entry := range entries {
				pair, ok := entry.([]any)
				if !ok || len(pair) != 2 {
					return nil, fmt.Errorf("invalid source Map entry")
				}
				k, e := r.restore(pair[0])
				if e != nil {
					return nil, e
				}
				x, e := r.restore(pair[1])
				if e != nil {
					return nil, e
				}
				out = append(out, sourceLivePair{k, x})
			}
			return out, nil
		}
		entries, ok := v["$object"].([]any)
		if !ok {
			return nil, fmt.Errorf("untagged source input object")
		}
		id, e := sourceLiveID(v["$id"])
		if e != nil {
			return nil, e
		}
		out, exists := r.refs[id]
		if !exists {
			out = filter.Object{}
			r.refs[id] = out
		} else {
			for key := range out {
				delete(out, key)
			}
		}
		for _, entry := range entries {
			pair, ok := entry.([]any)
			if !ok || len(pair) != 2 {
				return nil, fmt.Errorf("invalid source object entry")
			}
			key, ok := pair[0].(string)
			if !ok {
				return nil, fmt.Errorf("invalid source object key")
			}
			item, e := r.restore(pair[1])
			if e != nil {
				return nil, e
			}
			out[key] = item
		}
		return out, nil
	default:
		return v, nil
	}
}
func sourceLiveSameMap(a, b filter.Object) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}
func (r *sourceLiveRegistry) compare(actual, expected any, location string) error {
	fail := func(reason string) error {
		return fmt.Errorf("%s: %s (native %T=%v, source %T=%v)", location, reason, actual, actual, expected, expected)
	}
	if object, ok := expected.(map[string]any); ok {
		if object["$undefined"] == true {
			if !filter.IsUndefined(actual) {
				return fail("undefined differs")
			}
			return nil
		}
		if text, ok := object["$bigint"].(string); ok {
			n, ok := actual.(*big.Int)
			if !ok || n.String() != text {
				return fail("BigInt differs")
			}
			return nil
		}
		if text, ok := object["$number"].(string); ok {
			n, e := sourceLiveNumber(actual)
			if e != nil {
				return fail("number kind differs")
			}
			switch text {
			case "NaN":
				if math.IsNaN(n) {
					return nil
				}
			case "Infinity":
				if math.IsInf(n, 1) {
					return nil
				}
			case "-Infinity":
				if math.IsInf(n, -1) {
					return nil
				}
			case "-0":
				if n == 0 && math.Signbit(n) {
					return nil
				}
			}
			return fail("special number differs")
		}
		if value, ok := object["$ref"]; ok {
			id, e := sourceLiveID(value)
			if e != nil {
				return e
			}
			want, exists := r.refs[id]
			got, ok := actual.(filter.Object)
			if !exists || !ok || !sourceLiveSameMap(got, want) {
				return fail("original object identity differs")
			}
			return nil
		}
		if entries, ok := object["$map"].([]any); ok {
			pairs, ok := actual.(sourceLiveMap)
			if !ok || len(pairs) != len(entries) {
				return fail("Map size differs")
			}
			for i, entry := range entries {
				pair, ok := entry.([]any)
				if !ok || len(pair) != 2 {
					return fail("invalid expected Map entry")
				}
				if e := r.compare(pairs[i].Key, pair[0], fmt.Sprintf("%s.Map[%d].key", location, i)); e != nil {
					return e
				}
				if e := r.compare(pairs[i].Value, pair[1], fmt.Sprintf("%s.Map[%d].value", location, i)); e != nil {
					return e
				}
			}
			return nil
		}
		entries, ok := object["$object"].([]any)
		if !ok {
			return fail("untagged expected object")
		}
		got, ok := actual.(filter.Object)
		if !ok || len(got) != len(entries) {
			return fail("own property set differs")
		}
		id, e := sourceLiveID(object["$id"])
		if e != nil {
			return e
		}
		if bound, exists := r.refs[id]; exists && !sourceLiveSameMap(got, bound) {
			return fail("bound object identity differs")
		}
		for _, entry := range entries {
			pair, ok := entry.([]any)
			if !ok || len(pair) != 2 {
				return fail("invalid expected object entry")
			}
			key, ok := pair[0].(string)
			if !ok {
				return fail("invalid expected key")
			}
			v, exists := got[key]
			if !exists {
				return fail("missing own property " + key)
			}
			if e := r.compare(v, pair[1], location+"."+key); e != nil {
				return e
			}
		}
		// Only actual, checked native output enters the input identity registry.
		if _, exists := r.refs[id]; !exists {
			r.refs[id] = got
		}
		return nil
	}
	if list, ok := expected.([]any); ok {
		got, ok := sourceArray(actual)
		value := reflect.ValueOf(actual)
		if value.IsValid() && value.Kind() == reflect.Slice && value.IsNil() {
			return fail("nil native slice differs from source array")
		}
		if !ok || len(got) != len(list) {
			return fail("array length/kind differs")
		}
		for i, item := range list {
			if e := r.compare(got[i], item, fmt.Sprintf("%s[%d]", location, i)); e != nil {
				return e
			}
		}
		return nil
	}
	if _, ok := expected.(json.Number); ok {
		want, e := sourceLiveNumber(expected)
		if e != nil {
			return e
		}
		got, e := sourceLiveNumber(actual)
		if e != nil || got != want || got == 0 && math.Signbit(got) != math.Signbit(want) {
			return fail("number differs")
		}
		return nil
	}
	if !reflect.DeepEqual(actual, expected) {
		return fail("scalar differs")
	}
	return nil
}
func sourceLiveObject(value any) (filter.Object, error) {
	v, ok := value.(filter.Object)
	if !ok {
		return nil, fmt.Errorf("source value must be an object, got %T", value)
	}
	return v, nil
}
func sourceLiveList(value any) ([]any, error) {
	v, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("source value must be an array, got %T", value)
	}
	return v, nil
}
func sourceLiveUint(value any) (uint64, error) {
	n, e := sourceLiveNumber(value)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > math.MaxUint64 || math.Trunc(n) != n {
		return 0, fmt.Errorf("invalid source unsigned integer %v", value)
	}
	return uint64(n), nil
}
func sourceLiveHex(value any) (uint64, error) {
	v, ok := value.(string)
	if !ok || len(v) < 3 || v[:2] != "0x" {
		return 0, fmt.Errorf("source quantity is not hex")
	}
	return strconv.ParseUint(v[2:], 16, 64)
}
func sourceLiveObjects(value any) ([]filter.Object, error) {
	list, e := sourceLiveList(value)
	if e != nil {
		return nil, e
	}
	out := make([]filter.Object, len(list))
	for i, v := range list {
		out[i], e = sourceLiveObject(v)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func sourceLiveOutput(value any) any {
	switch v := value.(type) {
	case *SourceIntervalMap:
		out := sourceLiveMap{}
		for _, entry := range v.Entries() {
			values := []any{}
			for _, item := range entry.Values {
				intervals := []any{}
				for _, span := range item.Intervals {
					intervals = append(intervals, []any{span[0], span[1]})
				}
				values = append(values, filter.Object{"fragment": item.Fragment, "intervals": intervals})
			}
			out = append(out, sourceLivePair{entry.Key, values})
		}
		return out
	case *SourceAddressMap:
		out := sourceLiveMap{}
		for _, entry := range v.Entries {
			out = append(out, sourceLivePair{entry.Address, float64(entry.BlockNumber)})
		}
		return out
	case *SourceRecoveryBlock:
		if v == nil {
			return filter.Undefined
		}
		return filter.Object{"number": v.Number, "timestamp": v.Timestamp}
	case SourceBlockRange:
		return filter.Object{"fromBlock": v.FromBlock, "toBlock": v.ToBlock}
	case SourceEventData:
		rows := func(values []filter.Object) []any {
			out := []any{}
			for _, v := range values {
				out = append(out, v)
			}
			return out
		}
		return filter.Object{"blocks": rows(v.Blocks), "transactions": rows(v.Transactions), "transactionReceipts": rows(v.TransactionReceipts), "logs": rows(v.Logs), "traces": rows(v.Traces), "cursor": v.Cursor}
	case []*string:
		out := []any{}
		for _, v := range v {
			if v == nil {
				out = append(out, filter.Undefined)
			} else {
				out = append(out, *v)
			}
		}
		return out
	default:
		return value
	}
}
