package filter

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

const AddressChunkSize = 50

type ChildAddress struct {
	Address string
	Block   uint64
}
type RequiredInterval struct {
	Filter   Object
	Interval [2]int64
}

func toValues(v any) []any {
	if nullish(v) {
		return nil
	}
	return values(v)
}
func union(left, right []any) []any {
	if left == nil || right == nil {
		return nil
	}
	return dedupe(append(append([]any{}, left...), right...))
}
func sanitizeTopics(topics []any) []any {
	for len(topics) > 0 && topics[len(topics)-1] == nil {
		topics = topics[:len(topics)-1]
	}
	return topics
}

func MergeLogFiltersToBlockRequest(logFilters, factories []Object) any {
	address := []any{}
	topics := [][]any{{}, {}, {}, {}}
	empty := true
	add := func(a []any, ts [][]any) {
		empty = false
		address = union(address, a)
		for i := range topics {
			topics[i] = union(topics[i], ts[i])
		}
	}
	for _, f := range logFilters {
		a := get(f, "address")
		if xs, ok := arr(a); ok && len(xs) == 0 {
			continue
		}
		av := toValues(a)
		if IsAddressFactory(a) {
			av = nil
		}
		add(av, [][]any{toValues(get(f, "topic0")), toValues(get(f, "topic1")), toValues(get(f, "topic2")), toValues(get(f, "topic3"))})
	}
	for _, f := range factories {
		a := get(f, "address")
		if xs, ok := arr(a); ok && len(xs) == 0 {
			continue
		}
		add(toValues(a), [][]any{{get(f, "eventSelector")}, nil, nil, nil})
	}
	if empty {
		return Undefined
	}
	request := Object{"address": Undefined, "topics": Undefined}
	if address != nil && len(address) <= AddressChunkSize {
		request["address"] = address
	}
	ts := []any{}
	for _, v := range topics {
		if v == nil || len(v) > AddressChunkSize {
			ts = append(ts, nil)
		} else {
			ts = append(ts, v)
		}
	}
	ts = sanitizeTopics(ts)
	if len(ts) > 0 {
		request["topics"] = ts
	}
	return request
}
func requestValue(request Object, key string) any {
	if key == "address" {
		return get(request, key)
	}
	topics, _ := arr(get(request, "topics"))
	i, _ := strconv.Atoi(key[5:])
	if i >= len(topics) || topics[i] == nil {
		return Undefined
	}
	return topics[i]
}
func sameValue(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func MergeLogFiltersToRequests(filters []RequiredInterval, children map[string][]ChildAddress, threshold int) ([]Object, error) {
	requests := []Object{}
	keys := []string{"address", "topic0", "topic1", "topic2", "topic3"}
	for _, item := range filters {
		f := item.Filter
		if get(f, "type") != "log" {
			continue
		}
		address := get(f, "address")
		if IsAddressFactory(address) {
			entries, ok := children[str(get(object(address), "id"))]
			if !ok {
				return nil, fmt.Errorf("missing factory child map")
			}
			if len(entries) == 0 {
				continue
			}
			if len(entries) >= threshold {
				address = Undefined
			} else {
				xs := []any{}
				for _, entry := range entries {
					xs = append(xs, entry.Address)
				}
				address = xs
			}
		}
		ts := []any{get(f, "topic0"), nullable(get(f, "topic1")), nullable(get(f, "topic2")), nullable(get(f, "topic3"))}
		candidate := Object{"address": address, "topics": sanitizeTopics(ts), "fromBlock": fmt.Sprintf("0x%x", item.Interval[0]), "toBlock": fmt.Sprintf("0x%x", item.Interval[1])}
		merged := false
		for _, request := range requests {
			p := object(request["params"].([]any)[0])
			from, to := int64(num(get(p, "fromBlock"))), int64(num(get(p, "toBlock")))
			overlap := min(to, item.Interval[1]) - max(from, item.Interval[0]) + 1
			smaller := min(to-from+1, item.Interval[1]-item.Interval[0]+1)
			if overlap <= 0 || float64(overlap)/float64(smaller) < 0.8 {
				continue
			}
			differences := 0
			for _, key := range keys {
				if !sameValue(requestValue(p, key), requestValue(candidate, key)) {
					differences++
				}
			}
			if differences > 1 {
				continue
			}
			for _, key := range keys {
				a, b := requestValue(p, key), requestValue(candidate, key)
				if sameValue(a, b) {
					continue
				}
				v := union(toValues(a), toValues(b))
				if key == "address" {
					if v == nil {
						p[key] = Undefined
					} else {
						p[key] = v
					}
				} else {
					ts, _ := arr(get(p, "topics"))
					ts = append([]any{}, ts...)
					i, _ := strconv.Atoi(key[5:])
					for len(ts) <= i {
						ts = append(ts, nil)
					}
					if v == nil {
						ts[i] = nil
					} else {
						ts[i] = v
					}
					p["topics"] = sanitizeTopics(ts)
				}
			}
			p["fromBlock"] = fmt.Sprintf("0x%x", int64(math.Min(float64(from), float64(item.Interval[0]))))
			p["toBlock"] = fmt.Sprintf("0x%x", int64(math.Max(float64(to), float64(item.Interval[1]))))
			merged = true
			break
		}
		if !merged {
			requests = append(requests, Object{"method": "eth_getLogs", "params": []any{candidate}})
		}
	}
	return requests, nil
}
