package filter

import (
	"fmt"
	"strings"
)

func IsAddressFactory(v any) bool {
	if nullish(v) {
		return false
	}
	if _, ok := v.(string); ok {
		return false
	}
	if xs, ok := arr(v); ok {
		if len(xs) == 0 {
			return false
		}
		return IsAddressFactory(xs[0])
	}
	return true
}
func IsFragmentAddressFactory(v any) bool {
	if v == nil {
		return false
	}
	_, text := v.(string)
	return !text
}
func FragmentAddressID(v any) any {
	if v == nil {
		return nil
	}
	if _, ok := v.(string); ok {
		return v
	}
	o := object(v)
	return strings.Join([]string{str(get(o, "address")), str(get(o, "eventSelector")), str(get(o, "childAddressLocation")), str(get(o, "fromBlock")), str(get(o, "toBlock"))}, "_")
}

func AddressFragments(address any) []Object {
	out := []Object{}
	if IsAddressFactory(address) {
		f := object(address)
		for _, a := range values(get(f, "address")) {
			v := Object{"address": nullable(a), "eventSelector": get(f, "eventSelector"), "childAddressLocation": get(f, "childAddressLocation"), "fromBlock": nullable(get(f, "fromBlock")), "toBlock": nullable(get(f, "toBlock"))}
			out = append(out, Object{"fragment": v, "adjacentIds": []any{FragmentAddressID(v)}})
		}
	} else {
		if nullish(address) {
			address = nil
		}
		for _, a := range values(address) {
			ids := []any{a}
			if truth(a) {
				ids = append(ids, nil)
			}
			out = append(out, Object{"fragment": a, "adjacentIds": ids})
		}
	}
	return out
}
func FactoryFragments(factory Object) []Object {
	out := []Object{}
	for _, a := range values(get(factory, "address")) {
		out = append(out, Object{"type": "factory_log", "chainId": get(factory, "chainId"), "address": nullable(a), "eventSelector": get(factory, "eventSelector"), "childAddressLocation": get(factory, "childAddressLocation"), "fromBlock": nullable(get(factory, "fromBlock")), "toBlock": nullable(get(factory, "toBlock"))})
	}
	return out
}
func FactoryFragmentIDs(factory Object) []string {
	out := []string{}
	for _, f := range FactoryFragments(factory) {
		out = append(out, EncodeFragment(f))
	}
	return out
}
func FactoryFragmentID(factory Object, parent string) string {
	f := Object{"type": "factory_log", "chainId": get(factory, "chainId"), "address": strings.ToLower(parent), "eventSelector": get(factory, "eventSelector"), "childAddressLocation": get(factory, "childAddressLocation"), "fromBlock": nullable(get(factory, "fromBlock")), "toBlock": nullable(get(factory, "toBlock"))}
	if IsUndefined(get(factory, "address")) {
		f["address"] = nil
	}
	return EncodeFragment(f)
}

func EncodeFragment(f Object) string {
	parts := []any{get(f, "type"), get(f, "chainId")}
	switch str(get(f, "type")) {
	case "block":
		parts = append(parts, get(f, "interval"), get(f, "offset"))
	case "transaction", "trace", "transfer":
		parts = append(parts, FragmentAddressID(get(f, "fromAddress")), FragmentAddressID(get(f, "toAddress")))
		if str(get(f, "type")) == "trace" {
			parts = append(parts, get(f, "functionSelector"))
		}
		if str(get(f, "type")) != "transaction" {
			receipt := 0
			if truth(get(f, "includeTransactionReceipts")) {
				receipt = 1
			}
			parts = append(parts, receipt)
		}
	case "log":
		parts = append(parts, FragmentAddressID(get(f, "address")), get(f, "topic0"), get(f, "topic1"), get(f, "topic2"), get(f, "topic3"))
		receipt := 0
		if truth(get(f, "includeTransactionReceipts")) {
			receipt = 1
		}
		parts = append(parts, receipt)
	case "factory_log":
		parts = append(parts, get(f, "address"), get(f, "eventSelector"), get(f, "childAddressLocation"), get(f, "fromBlock"), get(f, "toBlock"))
	default:
		return ""
	}
	out := make([]string, len(parts))
	for i, v := range parts {
		out[i] = str(v)
	}
	return strings.Join(out, "_")
}
func receiptIDs(filter Object) []any {
	if truth(get(filter, "hasTransactionReceipt")) {
		return []any{1}
	}
	return []any{0, 1}
}
func adjacent(v any) []any {
	if truth(v) {
		return []any{v, nil}
	}
	return []any{nil}
}
func GetFragments(filter Object) ([]Object, error) {
	out := []Object{}
	kind := str(get(filter, "type"))
	chain := get(filter, "chainId")
	if kind == "block" {
		f := Object{"type": kind, "chainId": chain, "interval": get(filter, "interval"), "offset": get(filter, "offset")}
		return []Object{{"fragment": f, "adjacentIds": []any{EncodeFragment(f)}}}, nil
	}
	if kind == "log" {
		for _, address := range AddressFragments(get(filter, "address")) {
			for _, t1 := range values(get(filter, "topic1")) {
				for _, t2 := range values(get(filter, "topic2")) {
					for _, t3 := range values(get(filter, "topic3")) {
						f := Object{"type": kind, "chainId": chain, "address": address["fragment"], "topic0": get(filter, "topic0"), "topic1": nullable(t1), "topic2": nullable(t2), "topic3": nullable(t3), "includeTransactionReceipts": get(filter, "hasTransactionReceipt")}
						ids := []any{}
						for _, a := range values(address["adjacentIds"]) {
							for _, a1 := range adjacent(t1) {
								for _, a2 := range adjacent(t2) {
									for _, a3 := range adjacent(t3) {
										for _, r := range receiptIDs(filter) {
											ids = append(ids, fmt.Sprintf("log_%s_%s_%s_%s_%s_%s_%s", str(chain), str(a), str(get(filter, "topic0")), str(a1), str(a2), str(a3), str(r)))
										}
									}
								}
							}
						}
						out = append(out, Object{"fragment": f, "adjacentIds": ids})
					}
				}
			}
		}
		return out, nil
	}
	if kind != "transaction" && kind != "trace" && kind != "transfer" {
		return nil, fmt.Errorf("unknown filter type %q", kind)
	}
	for _, from := range AddressFragments(get(filter, "fromAddress")) {
		for _, to := range AddressFragments(get(filter, "toAddress")) {
			f := Object{"type": kind, "chainId": chain, "fromAddress": from["fragment"], "toAddress": to["fragment"]}
			if kind == "trace" {
				f["functionSelector"] = get(filter, "functionSelector")
			}
			if kind != "transaction" {
				f["includeTransactionReceipts"] = get(filter, "hasTransactionReceipt")
			}
			ids := []any{}
			for _, a := range values(from["adjacentIds"]) {
				for _, b := range values(to["adjacentIds"]) {
					base := kind + "_" + str(chain) + "_" + str(a) + "_" + str(b)
					if kind == "trace" {
						base += "_" + str(get(filter, "functionSelector"))
					}
					if kind == "transaction" {
						ids = append(ids, base)
					} else {
						for _, r := range receiptIDs(filter) {
							ids = append(ids, base+"_"+str(r))
						}
					}
				}
			}
			out = append(out, Object{"fragment": f, "adjacentIds": ids})
		}
	}
	return out, nil
}

func DecodeFragment(id string) (any, error) {
	parts := strings.Split(id, "_")
	if len(parts) < 2 {
		return Undefined, nil
	}
	kind := parts[0]
	if kind != "block" && kind != "transaction" && kind != "trace" && kind != "log" && kind != "transfer" {
		return Undefined, nil
	}
	at := func(i int) any {
		if i >= len(parts) {
			return Undefined
		}
		return parts[i]
	}
	f := Object{"type": kind, "chainId": num(at(1))}
	if kind == "block" {
		f["interval"] = num(at(2))
		f["offset"] = num(at(3))
		return f, nil
	}
	address := func(start int) (any, int) {
		if start+2 < len(parts) && (strings.HasPrefix(parts[start+2], "topic") || strings.HasPrefix(parts[start+2], "offset")) {
			from, to := any(nil), any(nil)
			if str(at(start+3)) != "null" {
				from = num(at(start + 3))
			}
			if str(at(start+4)) != "null" {
				to = num(at(start + 4))
			}
			a := nullable(at(start))
			if a == "null" {
				a = nil
			}
			return Object{"address": a, "eventSelector": at(start + 1), "childAddressLocation": at(start + 2), "fromBlock": from, "toBlock": to}, start + 5
		}
		a := at(start)
		if a == "null" {
			a = nil
		}
		return a, start + 1
	}
	a, next := address(2)
	if kind == "log" {
		f["address"] = a
		f["topic0"] = at(next)
		for i := 1; i < 4; i++ {
			v := at(next + i)
			if v == "null" {
				v = nil
			}
			f[fmt.Sprintf("topic%d", i)] = v
		}
		f["includeTransactionReceipts"] = str(at(next+4)) == "1"
		return f, nil
	}
	f["fromAddress"] = a
	f["toAddress"], next = address(next)
	if kind == "trace" {
		f["functionSelector"] = at(next)
		next++
	}
	if kind != "transaction" {
		f["includeTransactionReceipts"] = str(at(next)) == "1"
	}
	return f, nil
}

func RecoverFilter(base Object, fragments []Object) Object {
	o := clone(base)
	kind := str(get(base, "type"))
	if kind == "block" {
		return o
	}
	recoverAddress := func(key string) any {
		v := get(base, key)
		if _, ok := arr(v); !ok {
			return v
		}
		xs := []any{}
		for _, f := range fragments {
			xs = append(xs, get(f, key))
		}
		return dedupe(xs)
	}
	if kind == "log" {
		o["address"] = recoverAddress("address")
		for i := 1; i < 4; i++ {
			key := fmt.Sprintf("topic%d", i)
			v := get(base, key)
			if _, text := v.(string); v != nil && !text {
				xs := []any{}
				for _, f := range fragments {
					xs = append(xs, get(f, key))
				}
				o[key] = dedupe(xs)
			} else {
				o[key] = v
			}
		}
	} else if kind == "trace" || kind == "transfer" || kind == "transaction" {
		o["fromAddress"] = recoverAddress("fromAddress")
		o["toAddress"] = recoverAddress("toAddress")
	}
	return o
}
func RecoverFactory(base Object, fragments []Object) Object {
	o := clone(base)
	if _, ok := arr(get(base, "address")); ok {
		xs := []any{}
		for _, f := range fragments {
			xs = append(xs, get(f, "address"))
		}
		o["address"] = dedupe(xs)
	}
	return o
}
