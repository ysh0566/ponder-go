package events

import (
	"sort"
	"strconv"

	"ponder.local/ponder-go/abi"
)

// ProfileFieldOrder derives the source Object.keys order from an actual decoded
// envelope and its immutable canonical ABI. It never reads callback.filter:
// learned includes can be changing concurrently with profiling. Both row and RPC
// profilers must use this same projection. Arrays, scalars and absent values are
// deliberately excluded, as they are by the source profilers.
// This does not extend the decoder to JavaScript prototype-setter ABI names.
func ProfileFieldOrder(event Object) map[string][]string {
	body, ok := event["event"].(Object)
	if !ok {
		return nil
	}
	callback, ok := event["eventCallback"].(Object)
	if !ok || callback["type"] != "contract" {
		return nil
	}
	var component string
	var names []string
	switch event["type"] {
	case "log":
		component = "args"
		var item abi.Event
		if metadata(callback["abiItem"], &item) != nil {
			return nil
		}
		for _, p := range item.Inputs {
			if p.Name == "" {
				return nil
			}
		}
		// Source writes topics first, then data. Reassignment retains first position.
		for _, indexed := range []bool{true, false} {
			for _, p := range item.Inputs {
				if p.Indexed == indexed {
					names = append(names, p.Name)
				}
			}
		}
	case "trace":
		component = "result"
		var item abi.Function
		if metadata(callback["abiItem"], &item) != nil || len(item.Outputs) != 1 || item.Outputs[0].Type != "tuple" {
			return nil
		}
		for _, p := range item.Outputs[0].Components {
			if p.Name == "" {
				return nil
			}
			names = append(names, p.Name)
		}
	default:
		return nil
	}
	actual, ok := body[component].(Object)
	if !ok {
		return nil
	}
	keys, ok := decodedObjectKeys(actual, names)
	if !ok {
		return nil
	}
	return map[string][]string{component: keys}
}

func decodedObjectKeys(actual Object, insertion []string) ([]string, bool) {
	seen := map[string]bool{}
	integers, other := []string{}, []string{}
	for _, name := range insertion {
		if _, present := actual[name]; !present || seen[name] {
			continue
		}
		seen[name] = true
		n, err := strconv.ParseUint(name, 10, 32)
		if err == nil && n < 4294967295 && strconv.FormatUint(n, 10) == name {
			integers = append(integers, name)
		} else {
			other = append(other, name)
		}
	}
	// An arbitrary caller-created object may not have ABI insertion order. Never
	// manufacture a claim about keys not actually attributable to the decoder.
	if len(seen) != len(actual) {
		return nil, false
	}
	sort.Slice(integers, func(i, j int) bool {
		a, _ := strconv.ParseUint(integers[i], 10, 32)
		b, _ := strconv.ParseUint(integers[j], 10, 32)
		return a < b
	})
	return append(integers, other...), true
}
