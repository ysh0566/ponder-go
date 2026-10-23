package filter

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

func objects(v any) []Object {
	out := []Object{}
	for _, x := range v.([]any) {
		out = append(out, object(x))
	}
	return out
}
func children(v any) []ChildAddress {
	out := []ChildAddress{}
	for _, entry := range object(v)["$map"].([]any) {
		pair := entry.([]any)
		out = append(out, ChildAddress{str(pair[0]), uint64(num(pair[1]))})
	}
	return out
}
func differentialCall(operation string, args []any) (any, error) {
	a := object(args[0])
	switch operation {
	case "isAddressFactory":
		return IsAddressFactory(args[0]), nil
	case "isFragmentAddressFactory":
		return IsFragmentAddressFactory(args[0]), nil
	case "fragmentAddressToId":
		return FragmentAddressID(args[0]), nil
	case "getAddressFragments":
		return AddressFragments(args[0]), nil
	case "getFactoryFragments":
		return FactoryFragments(a), nil
	case "getFactoryFragmentIds":
		return FactoryFragmentIDs(a), nil
	case "getFactoryFragmentId":
		return FactoryFragmentID(a, str(args[1])), nil
	case "recoverFactory":
		return RecoverFactory(a, objects(args[1])), nil
	case "getFragments":
		return GetFragments(a)
	case "encodeFragment":
		return EncodeFragment(a), nil
	case "decodeFragment":
		return DecodeFragment(str(args[0]))
	case "recoverFilter":
		return RecoverFilter(a, objects(args[1])), nil
	case "getFilterFactories":
		return GetFilterFactories(a), nil
	case "getFilterFromBlock":
		return FilterFromBlock(a), nil
	case "getFilterToBlock":
		return FilterToBlock(a), nil
	case "isBlockInFilter":
		return IsBlockInFilter(a, num(args[1])), nil
	case "isLogFactoryMatched":
		return IsLogFactoryMatched(object(a["factory"]), object(a["log"])), nil
	case "isLogFilterMatched":
		return IsLogFilterMatched(object(a["filter"]), object(a["log"])), nil
	case "isTransactionFilterMatched":
		return IsTransactionFilterMatched(object(a["filter"]), object(a["transaction"])), nil
	case "isTraceFilterMatched":
		return IsTraceFilterMatched(object(a["filter"]), object(a["trace"]), object(a["block"])), nil
	case "isTransferFilterMatched":
		return IsTransferFilterMatched(object(a["filter"]), object(a["trace"]), object(a["block"]))
	case "isBlockFilterMatched":
		return IsBlockFilterMatched(object(a["filter"]), object(a["block"])), nil
	case "isAddressMatched":
		return IsAddressMatched(get(a, "address"), num(a["blockNumber"]), children(a["childAddresses"])), nil
	case "mergeLogFiltersToBlockRequest":
		return MergeLogFiltersToBlockRequest(objects(a["logFilters"]), objects(a["factories"])), nil
	case "mergeLogFiltersToRequests":
		filters := []RequiredInterval{}
		for _, value := range args[0].([]any) {
			item := object(value)
			interval := item["interval"].([]any)
			filters = append(filters, RequiredInterval{object(item["filter"]), [2]int64{int64(num(interval[0])), int64(num(interval[1]))}})
		}
		childMap := map[string][]ChildAddress{}
		for _, value := range object(args[1])["$map"].([]any) {
			pair := value.([]any)
			childMap[str(pair[0])] = children(pair[1])
		}
		return MergeLogFiltersToRequests(filters, childMap, int(num(args[2])))
	default:
		if strings.HasPrefix(operation, "unionFilterInclude") {
			data := strings.TrimPrefix(operation, "unionFilterInclude")
			data = strings.ToLower(data[:1]) + data[1:]
			return UnionInclude(objects(args[0]), data), nil
		}
		return nil, fmt.Errorf("unknown differential operation %s", operation)
	}
}
func marked(v any) any {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) {
			return Object{"$number": "NaN"}
		}
		if math.IsInf(x, 1) {
			return Object{"$number": "Infinity"}
		}
		if math.IsInf(x, -1) {
			return Object{"$number": "-Infinity"}
		}
	case map[string]any:
		out := Object{}
		for k, v := range x {
			out[k] = marked(v)
		}
		return out
	case []Object:
		out := []any{}
		for _, v := range x {
			out = append(out, marked(v))
		}
		return out
	case []any:
		out := []any{}
		for _, v := range x {
			out = append(out, marked(v))
		}
		return out
	}
	return v
}

// Additional generated vectors are never counted as original source records.
func TestFilterDifferential(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/filter-differential.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Vectors []struct {
			sourceCall
			Error any
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, v := range fixture.Vectors {
		t.Run(fmt.Sprintf("%04d_%s", i, v.Operation), func(t *testing.T) {
			result, err := differentialCall(v.Operation, FixtureValue(v.Args).([]any))
			if v.Error != nil {
				if err == nil {
					t.Fatal("source throws; native must reject", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, e := json.Marshal(marked(result))
			if e != nil {
				t.Fatal(e)
			}
			want, e := json.Marshal(v.Result)
			if e != nil {
				t.Fatal(e)
			}
			if string(got) != string(want) {
				t.Fatalf("%s input=%s\ngot %s\nwant %s", v.Operation, mustJSON(v.Args), got, want)
			}
		})
	}
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
