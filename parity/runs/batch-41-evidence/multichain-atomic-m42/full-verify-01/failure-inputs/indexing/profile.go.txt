package indexing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"

	ponderabi "ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/filter"
)

// ProfileValue is the RPC profiler's constant or event-property path. This is
// unrelated to store.ProfilePattern, which predicts database rows.
type ProfileValue struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}
type ProfilePattern struct {
	Address      ProfileValue
	ABI          ponderabi.ABI
	ABIValue     any
	FunctionName string
	// Nil represents JavaScript undefined; an explicitly empty slice is distinct
	// for a supplied hint, although new records normalize empty call args to nil.
	Args  []ProfileValue
	Cache string
}
type RecordedProfile struct {
	Pattern     ProfilePattern
	HasConstant bool
}
type ProfileRequest struct {
	ContractCall
	BlockNumber any
	ChainID     any
}

func profileGet(object any, key string) any {
	if object, ok := object.(Object); ok {
		if value, exists := object[key]; exists {
			return value
		}
	}
	return filter.Undefined
}
func profilePath(event Object, path ...string) any {
	var result any = event["event"]
	for _, key := range path {
		result = profileGet(result, key)
	}
	return result
}
func profilePrimitive(value any) bool {
	if filter.IsUndefined(value) {
		return true
	}
	if value == nil {
		return false
	}
	switch value.(type) {
	case *big.Int, big.Int:
		return true
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
func profileTruthy(value any) bool {
	if value == nil || filter.IsUndefined(value) {
		return false
	}
	switch n := value.(type) {
	case *big.Int:
		return n != nil && n.Sign() != 0
	case big.Int:
		return n.Sign() != 0
	case json.Number:
		f, e := n.Float64()
		return e == nil && f != 0 && !math.IsNaN(f)
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
func profileNumber(value any) (float64, bool) {
	if n, ok := value.(json.Number); ok {
		f, e := n.Float64()
		return f, e == nil
	}
	if value == nil {
		return 0, false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint()), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	}
	return 0, false
}
func profileBigInt(value any) (*big.Int, bool) {
	switch n := value.(type) {
	case *big.Int:
		return n, n != nil
	case big.Int:
		return &n, true
	}
	return nil, false
}
func profileString(value any) string {
	if filter.IsUndefined(value) {
		return "undefined"
	}
	if value == nil {
		return "null"
	}
	if n, ok := profileBigInt(value); ok {
		return n.String()
	}
	if n, ok := profileNumber(value); ok {
		if n == 0 {
			return "0"
		}
		// JSON numbers use the same fixed/exponent cutoffs as JS Number.toString.
		if math.IsNaN(n) {
			return "NaN"
		}
		if math.IsInf(n, 1) {
			return "Infinity"
		}
		if math.IsInf(n, -1) {
			return "-Infinity"
		}
		raw, _ := json.Marshal(n)
		return string(raw)
	}
	if a, ok := value.([]any); ok {
		out := make([]string, len(a))
		for i, v := range a {
			if v != nil && !filter.IsUndefined(v) {
				out[i] = profileString(v)
			}
		}
		return strings.Join(out, ",")
	}
	if !profilePrimitive(value) {
		return "[object Object]"
	}
	return fmt.Sprint(value)
}
func profileEqual(a, b any) bool {
	if filter.IsUndefined(a) || filter.IsUndefined(b) {
		return filter.IsUndefined(a) && filter.IsUndefined(b)
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if x, ok := profileNumber(a); ok {
		if y, ok := profileNumber(b); ok {
			return x == y
		}
	}
	if x, ok := profileBigInt(a); ok {
		if y, ok := profileBigInt(b); ok {
			return x.Cmp(y) == 0
		}
	}
	// Reference equality, not deep equality, is the source's first comparison.
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	if av.Type() == bv.Type() {
		switch av.Kind() {
		case reflect.Map:
			if av.UnsafePointer() == bv.UnsafePointer() {
				return true
			}
		case reflect.Slice, reflect.Pointer:
			if av.Pointer() == bv.Pointer() {
				return true
			}
		default:
			if av.Type().Comparable() && av.Interface() == bv.Interface() {
				return true
			}
		}
	}
	return profileTruthy(a) && profileTruthy(b) && profileString(a) == profileString(b)
}
func profileDerived(path ...string) ProfileValue { return ProfileValue{Type: "derived", Value: path} }

// profileFieldKeys models Object.keys: integer indices first, then insertion
// order. Go maps lack insertion order, so callers replaying JS must supply
// fieldOrder for args/result. Without metadata non-index keys sort by name;
// this deterministic native facade is not a claim about JS map insertion order.
func profileFieldKeys(object Object, order []string) []string {
	indices := []string{}
	names := []string{}
	seen := map[string]bool{}
	isIndex := func(k string) bool {
		n, e := strconv.ParseUint(k, 10, 32)
		return e == nil && n < 4294967295 && strconv.FormatUint(n, 10) == k
	}
	for k := range object {
		if isIndex(k) {
			indices = append(indices, k)
		}
	}
	sort.Slice(indices, func(i, j int) bool {
		a, _ := strconv.ParseUint(indices[i], 10, 32)
		b, _ := strconv.ParseUint(indices[j], 10, 32)
		return a < b
	})
	for _, k := range order {
		if _, ok := object[k]; ok && !seen[k] && !isIndex(k) {
			names = append(names, k)
			seen[k] = true
		}
	}
	remainder := []string{}
	for k := range object {
		if !seen[k] && !isIndex(k) {
			remainder = append(remainder, k)
		}
	}
	sort.Strings(remainder)
	return append(append(indices, names...), remainder...)
}
func profileFields(event Object, component string, target any, order map[string][]string) *ProfileValue {
	fields, ok := profilePath(event, component).(Object)
	if !ok {
		return nil
	}
	for _, key := range profileFieldKeys(fields, order[component]) {
		candidate := fields[key]
		if profilePrimitive(candidate) && profileEqual(candidate, target) {
			p := profileDerived(component, key)
			return &p
		}
	}
	return nil
}

type profileCandidate struct {
	component, key string
	optional       bool
}

func profileCandidates(kind string, address bool) []profileCandidate {
	out := []profileCandidate{}
	add := func(component string, keys ...string) {
		for _, key := range keys {
			optional := key == "miner" || key == "to" || key == "contractAddress"
			out = append(out, profileCandidate{component, key, optional})
		}
	}
	if kind == "transfer" {
		add("transfer", "from", "to")
		out[len(out)-1].optional = false
	}
	if kind == "trace" || kind == "transfer" {
		add("trace", "from", "to")
	}
	if kind == "log" {
		add("log", "address")
		if !address {
			add("log", "logIndex")
		}
	}
	switch kind {
	case "block", "transaction", "log", "trace", "transfer":
		if !address {
			add("block", "hash", "number", "timestamp")
		}
		add("block", "miner")
	}
	if kind != "block" {
		switch kind {
		case "transaction", "log", "trace", "transfer":
			if !address {
				add("transaction", "hash")
			}
			add("transaction", "from", "to")
			if !address {
				add("transaction", "transactionIndex")
			}
			add("transactionReceipt", "contractAddress")
		}
	}
	return out
}
func profileMatch(event Object, target any, address bool, order map[string][]string) []ProfileValue {
	kind, _ := event["type"].(string)
	matches := []ProfileValue{}
	if kind == "log" || kind == "trace" {
		if p := profileFields(event, "args", target, order); p != nil {
			matches = append(matches, *p)
		}
		if kind == "trace" {
			if p := profileFields(event, "result", target, order); p != nil {
				// The source overwrites address but pushes BOTH argument derivations.
				if address {
					matches = nil
				}
				matches = append(matches, *p)
			}
		}
		if len(matches) > 0 {
			return matches
		}
	}
	for _, candidate := range profileCandidates(kind, address) {
		value := profilePath(event, candidate.component, candidate.key)
		if (!candidate.optional || profileTruthy(value)) && profileEqual(value, target) {
			return []ProfileValue{profileDerived(candidate.component, candidate.key)}
		}
	}
	return nil
}

// RecordProfilePattern observes actual call arguments and the raw event, without
// reading PropertyView accessors (the source disables proxy instrumentation).
// Object/null call arguments are not profiled. A matching supplied hint is
// returned by identity, including the source no-args cache-check bypass.
func RecordProfilePattern(event Object, call ContractCall, cache string, hints []*RecordedProfile, fieldOrder map[string][]string) (*RecordedProfile, error) {
	for _, hint := range hints {
		if hint == nil {
			return nil, fmt.Errorf("nil RPC profile hint")
		}
		recovered, err := RecoverProfilePattern(hint.Pattern, event)
		if err != nil {
			return nil, err
		}
		if recovered.FunctionName != call.FunctionName || recovered.Address != call.Address {
			continue
		}
		if recovered.Args == nil && call.Args == nil {
			return hint, nil
		}
		if (recovered.Args == nil) != (call.Args == nil) || len(recovered.Args) != len(call.Args) {
			continue
		}
		matched := true
		for i, arg := range recovered.Args {
			if !profileEqual(arg, call.Args[i]) {
				matched = false
				break
			}
		}
		if !matched || (recovered.BlockNumber == "latest") != (cache == "immutable") {
			continue
		}
		return hint, nil
	}
	result := &RecordedProfile{Pattern: ProfilePattern{ABI: call.ABI, ABIValue: call.ABIValue, FunctionName: call.FunctionName, Cache: cache}}
	address := profileMatch(event, call.Address, true, fieldOrder)
	if len(address) > 0 {
		result.Pattern.Address = address[0]
	} else {
		result.Pattern.Address = ProfileValue{Type: "constant", Value: call.Address}
		result.HasConstant = true
	}
	if len(call.Args) == 0 {
		return result, nil
	}
	result.Pattern.Args = make([]ProfileValue, 0, len(call.Args))
	for _, arg := range call.Args {
		if !profilePrimitive(arg) {
			return nil, nil
		}
		matched := profileMatch(event, arg, false, fieldOrder)
		if len(matched) > 0 {
			result.Pattern.Args = append(result.Pattern.Args, matched...)
		} else {
			result.Pattern.Args = append(result.Pattern.Args, ProfileValue{Type: "constant", Value: arg})
			result.HasConstant = true
		}
	}
	return result, nil
}
func recoverProfileValue(value ProfileValue, event Object) (any, error) {
	if value.Type == "constant" {
		return value.Value, nil
	}
	var path []string
	switch p := value.Value.(type) {
	case []string:
		path = p
	case []any:
		for _, v := range p {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("RPC profile path contains non-string")
			}
			path = append(path, s)
		}
	default:
		return nil, fmt.Errorf("invalid RPC profile path")
	}
	var result any = event["event"]
	for _, key := range path {
		if result == nil || filter.IsUndefined(result) {
			return nil, fmt.Errorf("cannot read RPC profile property %q of %s", key, profileString(result))
		}
		result = profileGet(result, key)
	}
	return result, nil
}
func RecoverProfilePattern(pattern ProfilePattern, event Object) (ProfileRequest, error) {
	address, err := recoverProfileValue(pattern.Address, event)
	if err != nil {
		return ProfileRequest{}, err
	}
	text, ok := address.(string)
	if !ok {
		return ProfileRequest{}, fmt.Errorf("recovered RPC profile address is not a string")
	}
	request := ProfileRequest{ContractCall: ContractCall{ABI: pattern.ABI, ABIValue: pattern.ABIValue, Address: text, FunctionName: pattern.FunctionName}, BlockNumber: profilePath(event, "block", "number"), ChainID: profileGet(event["chain"], "id")}
	if pattern.Cache == "immutable" {
		request.BlockNumber = "latest"
	}
	if pattern.Args != nil {
		request.Args = make([]any, 0, len(pattern.Args))
		for _, arg := range pattern.Args {
			v, e := recoverProfileValue(arg, event)
			if e != nil {
				return ProfileRequest{}, e
			}
			request.Args = append(request.Args, v)
		}
	}
	return request, nil
}
func profileKeyValue(v any) (any, error) {
	if n, ok := profileBigInt(v); ok {
		return n.String(), nil
	}
	if filter.IsUndefined(v) {
		return v, nil
	}
	switch a := v.(type) {
	case []string:
		return a, nil
	case []any:
		out := make([]any, len(a))
		for i, v := range a {
			x, e := profileKeyValue(v)
			if e != nil {
				return nil, e
			}
			if filter.IsUndefined(x) {
				x = nil
			}
			out[i] = x
		}
		return out, nil
	case Object:
		out := Object{}
		for k, v := range a {
			if filter.IsUndefined(v) {
				continue
			}
			x, e := profileKeyValue(v)
			if e != nil {
				return nil, e
			}
			out[k] = x
		}
		return out, nil
	}
	if f, ok := profileNumber(v); ok {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, nil
		}
	}
	return v, nil
}

// GetProfilePatternKey intentionally excludes ABI and cache policy. Do not add
// either: the pinned implementation aliases those otherwise distinct patterns.
func GetProfilePatternKey(pattern ProfilePattern) (string, error) {
	value := func(v ProfileValue) (Object, error) {
		out := Object{"type": v.Type}
		if !filter.IsUndefined(v.Value) {
			x, e := profileKeyValue(v.Value)
			if e != nil {
				return nil, e
			}
			out["value"] = x
		}
		return out, nil
	}
	address, e := value(pattern.Address)
	if e != nil {
		return "", e
	}
	object := Object{"address": address, "functionName": pattern.FunctionName}
	if pattern.Args != nil {
		args := make([]any, len(pattern.Args))
		for i, arg := range pattern.Args {
			v, e := value(arg)
			if e != nil {
				return "", e
			}
			args[i] = v
		}
		object["args"] = args
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if e := encoder.Encode(object); e != nil {
		return "", e
	}
	// encoding/json additionally escapes U+2028/U+2029; JSON.stringify does not.
	result := strings.TrimSuffix(buffer.String(), "\n")
	var out strings.Builder
	for i := 0; i < len(result); i++ {
		if result[i] == '\\' && i+1 < len(result) {
			if strings.HasPrefix(result[i:], `\u2028`) {
				out.WriteRune('\u2028')
				i += 5
				continue
			}
			if strings.HasPrefix(result[i:], `\u2029`) {
				out.WriteRune('\u2029')
				i += 5
				continue
			}
			out.WriteByte(result[i])
			i++
			out.WriteByte(result[i])
			continue
		}
		out.WriteByte(result[i])
	}
	result = out.String()
	return result, nil
}
