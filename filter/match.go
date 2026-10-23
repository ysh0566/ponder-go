package filter

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

func IsValueMatched(filterValue, eventValue any) bool {
	if nullish(filterValue) {
		return true
	}
	if IsUndefined(eventValue) {
		return false
	}
	if a, ok := filterValue.(string); ok && a == eventValue {
		return true
	}
	value := strings.ToLower(str(eventValue))
	if xs, ok := arr(filterValue); ok {
		for _, v := range xs {
			if v == value {
				return true
			}
		}
		return false
	}
	return filterValue == value
}
func IsBlockInFilter(filter Object, number float64) bool {
	from, to := get(filter, "fromBlock"), get(filter, "toBlock")
	if nullish(from) {
		from = 0
	}
	if nullish(to) {
		to = math.Inf(1)
	}
	return !(number < num(from) || number > num(to))
}
func IsAddressMatched(address any, number float64, children []ChildAddress) bool {
	if IsUndefined(address) {
		return false
	}
	value := strings.ToLower(str(address))
	for _, child := range children {
		if child.Address == value && float64(child.Block) <= number {
			return true
		}
	}
	return false
}
func GetChildAddress(log, factory Object) string {
	location := str(get(factory, "childAddressLocation"))
	text := ""
	start := 26
	if strings.HasPrefix(location, "offset") {
		offset, _ := strconv.Atoi(strings.TrimPrefix(location, "offset"))
		text = str(get(log, "data"))
		start += offset * 2
	} else {
		topics, _ := arr(get(log, "topics"))
		i := 3
		if location == "topic1" {
			i = 1
		} else if location == "topic2" {
			i = 2
		}
		if i >= len(topics) {
			return "0x"
		}
		text = str(topics[i])
	}
	if start < 0 {
		start = 0
	}
	if start > len(text) {
		start = len(text)
	}
	end := min(start+40, len(text))
	return "0x" + text[start:end]
}
func IsLogFactoryMatched(factory, log Object) bool {
	address := get(factory, "address")
	if !IsUndefined(address) {
		matched := false
		for _, a := range values(address) {
			if a == strings.ToLower(str(get(log, "address"))) {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	topics, _ := arr(get(log, "topics"))
	if len(topics) == 0 || get(factory, "eventSelector") != strings.ToLower(str(topics[0])) {
		return false
	}
	return IsBlockInFilter(factory, num(get(log, "blockNumber")))
}
func IsLogFilterMatched(filter, log Object) bool {
	if !IsBlockInFilter(filter, num(get(log, "blockNumber"))) {
		return false
	}
	topics, _ := arr(get(log, "topics"))
	for i := 0; i < 4; i++ {
		v := any(Undefined)
		if i < len(topics) {
			v = topics[i]
		}
		if !IsValueMatched(get(filter, "topic"+strconv.Itoa(i)), v) {
			return false
		}
	}
	return IsAddressFactory(get(filter, "address")) || IsValueMatched(get(filter, "address"), get(log, "address"))
}
func addressesMatched(filter, event Object) bool {
	for _, key := range []string{"from", "to"} {
		v := get(event, key)
		if nullish(v) {
			v = Undefined
		}
		base := get(filter, key+"Address")
		if !IsAddressFactory(base) && !IsValueMatched(base, v) {
			return false
		}
	}
	return true
}
func IsTransactionFilterMatched(filter, transaction Object) bool {
	return IsBlockInFilter(filter, num(get(transaction, "blockNumber"))) && addressesMatched(filter, transaction)
}
func IsTraceFilterMatched(filter, trace, block Object) bool {
	if !IsBlockInFilter(filter, num(get(block, "number"))) || !addressesMatched(filter, trace) {
		return false
	}
	input := str(get(trace, "input"))
	if len(input) > 10 {
		input = input[:10]
	}
	if !IsValueMatched(get(filter, "functionSelector"), input) {
		return false
	}
	call := get(filter, "callType")
	return IsUndefined(call) || call == get(trace, "type")
}
func IsTransferFilterMatched(filter, trace, block Object) (bool, error) {
	if !IsBlockInFilter(filter, num(get(block, "number"))) {
		return false, nil
	}
	value := get(trace, "value")
	if nullish(value) {
		return false, nil
	}
	n, ok := new(big.Int).SetString(str(value), 0)
	if !ok {
		n, ok = new(big.Int).SetString(str(value), 10)
	}
	if !ok {
		return false, strconv.ErrSyntax
	}
	if n.Sign() == 0 {
		return false, nil
	}
	kind := get(trace, "type")
	if kind == "DELEGATECALL" || kind == "CALLCODE" {
		return false, nil
	}
	return addressesMatched(filter, trace), nil
}
func IsBlockFilterMatched(filter, block Object) bool {
	n := num(get(block, "number"))
	return IsBlockInFilter(filter, n) && math.Mod(n-num(get(filter, "offset")), num(get(filter, "interval"))) == 0
}
func GetFilterFactories(filter Object) []Object {
	out := []Object{}
	keys := []string{"fromAddress", "toAddress"}
	if get(filter, "type") == "log" {
		keys = []string{"address"}
	} else if get(filter, "type") == "block" {
		return out
	}
	for _, key := range keys {
		v := get(filter, key)
		if IsAddressFactory(v) {
			out = append(out, object(v))
		}
	}
	return out
}
func FilterFromBlock(filter Object) float64 {
	v := get(filter, "fromBlock")
	if nullish(v) {
		v = 0
	}
	n := num(v)
	for _, f := range GetFilterFactories(filter) {
		v = get(f, "fromBlock")
		if nullish(v) {
			v = 0
		}
		n = math.Min(n, num(v))
	}
	return n
}
func FilterToBlock(filter Object) float64 {
	v := get(filter, "toBlock")
	if nullish(v) {
		v = math.Inf(1)
	}
	n := num(v)
	for _, f := range GetFilterFactories(filter) {
		v = get(f, "toBlock")
		if nullish(v) {
			v = math.Inf(1)
		}
		n = math.Max(n, num(v))
	}
	return n
}
func UnionInclude(filters []Object, data string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, f := range filters {
		xs, _ := arr(get(f, "include"))
		for _, x := range xs {
			parts := strings.Split(str(x), ".")
			if len(parts) > 1 && parts[0] == data && !seen[parts[1]] {
				seen[parts[1]] = true
				out = append(out, parts[1])
			}
		}
	}
	return out
}
