package store

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"

	"ponder.local/ponder-go/schema"
)

// ProfileEvent is the data read by the upstream event profiler. FieldOrder
// preserves JS object insertion order for args/results with equal values. A
// native caller without that metadata gets deterministic sorted field order.
type ProfileEvent struct {
	Callback   string
	Count      uint64
	Type       string
	ChainID    uint64
	Data       Row
	FieldOrder map[string][]string
}

type ProfilePattern map[string]ProfileValue
type ProfileValue struct {
	Type      string         `json:"type"`
	Value     []string       `json:"value,omitempty"`
	Values    []ProfileValue `json:"values,omitempty"`
	Delimiter string         `json:"delimiter,omitempty"`
	DivideBy  int64          `json:"-"` // Native representation of the source BigInt callback.
}

func profileObject(value any) (Row, bool) {
	switch value := value.(type) {
	case Row:
		return value, true
	case map[string]any:
		return Row(value), true
	}
	return nil, false
}
func (event ProfileEvent) value(path ...string) any {
	var value any = event.Data
	for _, part := range path {
		object, ok := profileObject(value)
		if !ok {
			return Undefined
		}
		value, ok = object[part]
		if !ok {
			return Undefined
		}
	}
	return value
}
func profileTruthy(value any) bool {
	if value == nil || isUndefined(value) {
		return false
	}
	if n, ok := value.(*big.Int); ok {
		return n != nil && n.Sign() != 0
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.String:
		return v.Len() != 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return v.Float() != 0 && !math.IsNaN(v.Float())
	}
	return true
}
func profileEqual(target, value any) bool {
	// All Go numeric widths here represent the one JS Number primitive.
	numeric := func(value any) bool {
		if value == nil {
			return false
		}
		kind := reflect.TypeOf(value).Kind()
		return kind >= reflect.Int && kind <= reflect.Float64 && kind != reflect.Uintptr
	}
	if numeric(target) && numeric(value) {
		left, a := new(big.Rat).SetString(fmt.Sprint(target))
		right, b := new(big.Rat).SetString(fmt.Sprint(value))
		if a && b {
			return left.Cmp(right) == 0
		}
	}
	if reflect.DeepEqual(target, value) {
		return true
	}
	return profileTruthy(target) && profileTruthy(value) && fmt.Sprint(target) == fmt.Sprint(value)
}
func profilePrimitive(value any) bool {
	if isUndefined(value) {
		return true
	}
	if value == nil {
		return false
	}
	if _, ok := value.(*big.Int); ok {
		return true
	}
	v := reflect.ValueOf(value)
	return v.Kind() != reflect.Map && v.Kind() != reflect.Slice && v.Kind() != reflect.Array && v.Kind() != reflect.Struct && v.Kind() != reflect.Pointer
}

func matchProfileValue(event ProfileEvent, value any) *ProfileValue {
	derived := func(path ...string) *ProfileValue { return &ProfileValue{Type: "derived", Value: path} }
	if profileEqual(event.ChainID, value) {
		return derived("chainId")
	}
	if profileEqual(event.value("id"), value) {
		return derived("id")
	}
	matchFields := func(name string) *ProfileValue {
		object, ok := profileObject(event.value(name))
		if !ok {
			return nil
		}
		names := event.FieldOrder[name]
		if names == nil {
			for key := range object {
				names = append(names, key)
			}
			sort.Strings(names)
		}
		for _, key := range names {
			if candidate, ok := object[key]; ok && profilePrimitive(candidate) && profileEqual(candidate, value) {
				return derived(name, key)
			}
		}
		return nil
	}
	matchPath := func(optional bool, path ...string) *ProfileValue {
		candidate := event.value(path...)
		if (!optional || profileTruthy(candidate)) && profileEqual(candidate, value) {
			return derived(path...)
		}
		return nil
	}
	matchBlock := func() *ProfileValue {
		for _, key := range []string{"hash", "number", "timestamp"} {
			if p := matchPath(false, "block", key); p != nil {
				return p
			}
		}
		if timestamp, ok := event.value("block", "timestamp").(*big.Int); ok && timestamp != nil {
			for _, divisor := range []int64{60, 3600, 86400} {
				if profileEqual(new(big.Int).Quo(timestamp, big.NewInt(divisor)), value) {
					p := derived("block", "timestamp")
					p.DivideBy = divisor
					return p
				}
			}
		}
		return matchPath(true, "block", "miner")
	}
	matchTransaction := func() *ProfileValue {
		for _, key := range []string{"hash", "from", "to", "transactionIndex"} {
			if p := matchPath(key == "to", "transaction", key); p != nil {
				return p
			}
		}
		return matchPath(true, "transactionReceipt", "contractAddress")
	}
	switch event.Type {
	case "block":
		if p := matchBlock(); p != nil {
			return p
		}
	case "transaction":
		if p := matchBlock(); p != nil {
			return p
		}
		if p := matchTransaction(); p != nil {
			return p
		}
	case "log", "trace", "transfer":
		if event.Type != "transfer" {
			if p := matchFields("args"); p != nil {
				return p
			}
			if event.Type == "trace" {
				if p := matchFields("result"); p != nil {
					return p
				}
			}
		}
		if event.Type == "log" {
			for _, key := range []string{"address", "logIndex"} {
				if p := matchPath(false, "log", key); p != nil {
					return p
				}
			}
		} else {
			if event.Type == "transfer" {
				for _, key := range []string{"from", "to"} {
					if p := matchPath(false, "transfer", key); p != nil {
						return p
					}
				}
			}
			for _, key := range []string{"from", "to"} {
				if p := matchPath(key == "to", "trace", key); p != nil {
					return p
				}
			}
		}
		if p := matchBlock(); p != nil {
			return p
		}
		if p := matchTransaction(); p != nil {
			return p
		}
	}
	if text, ok := value.(string); ok {
	delimiters:
		for _, delimiter := range []string{"-", "_", ":", "#", "$"} {
			parts := strings.Split(text, delimiter)
			if len(parts) < 2 {
				continue
			}
			pattern := &ProfileValue{Type: "delimiter", Delimiter: delimiter}
			for _, part := range parts {
				p := matchProfileValue(event, part)
				if p == nil || p.Type != "derived" {
					continue delimiters
				}
				pattern.Values = append(pattern.Values, *p)
			}
			return pattern
		}
	}
	return nil
}

func RecordProfilePattern(event ProfileEvent, table schema.Table, key Row, hints []ProfilePattern) (ProfilePattern, error) {
	for _, hint := range hints {
		recovered, e := RecoverProfilePattern(hint, event)
		if e != nil {
			return nil, e
		}
		expected, e := cacheKey(table, key)
		if e != nil {
			return nil, e
		}
		actual, e := cacheKey(table, recovered)
		if e != nil {
			return nil, e
		}
		if expected == actual {
			return hint, nil
		}
	}
	pattern := ProfilePattern{}
	for _, physical := range table.Keys() {
		name := physical
		for _, column := range table.Columns {
			if column.Name == physical {
				name = property(column)
				break
			}
		}
		value, ok := key[name]
		if !ok {
			value = Undefined
		}
		match := matchProfileValue(event, value)
		if match == nil {
			return nil, nil
		}
		pattern[name] = *match
	}
	return pattern, nil
}

func RecoverProfilePattern(pattern ProfilePattern, event ProfileEvent) (Row, error) {
	recoverValue := func(pattern ProfileValue) (any, error) {
		if len(pattern.Value) == 0 {
			return nil, fmt.Errorf("empty profile path")
		}
		var value any
		if pattern.Value[0] == "chainId" {
			value = event.ChainID
		} else {
			value = event.value(pattern.Value...)
		}
		if isUndefined(value) {
			return nil, fmt.Errorf("missing profile path %v", pattern.Value)
		}
		if pattern.DivideBy != 0 {
			n, ok := value.(*big.Int)
			if !ok || n == nil {
				return nil, fmt.Errorf("profile timestamp requires bigint")
			}
			value = new(big.Int).Quo(n, big.NewInt(pattern.DivideBy))
		}
		return cloneValue(value), nil
	}
	row := Row{}
	for name, value := range pattern {
		if value.Type == "derived" {
			v, e := recoverValue(value)
			if e != nil {
				return nil, e
			}
			row[name] = v
		} else if value.Type == "delimiter" {
			parts := make([]string, len(value.Values))
			for i, part := range value.Values {
				v, e := recoverValue(part)
				if e != nil {
					return nil, e
				}
				parts[i] = fmt.Sprint(v)
			}
			row[name] = strings.Join(parts, value.Delimiter)
		} else {
			return nil, fmt.Errorf("unsupported profile pattern type %q", value.Type)
		}
	}
	return row, nil
}

// JSON encoding sorts object keys, matching orderObject. DivideBy represents a
// function and is omitted just as JSON.stringify omits source function fields.
func ProfilePatternKey(pattern ProfilePattern) (string, error) {
	raw, e := json.Marshal(pattern)
	if e != nil {
		return "", e
	}
	var object any
	if e = json.Unmarshal(raw, &object); e != nil {
		return "", e
	}
	canonical, e := json.Marshal(object)
	return string(canonical), e
}
