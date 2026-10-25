package runtime

// Differential replay of unchanged, isolated original test declarations. This
// does not replace the original setup hooks or award pristine source-test credit.
import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

func sourceIndexPureCache(value any) ([]store.SourceIntervalEntry, error) {
	pairs, ok := value.(historicalLiveMap)
	if !ok {
		return nil, fmt.Errorf("cachedIntervals is not a source Map")
	}
	cached := []store.SourceIntervalEntry{}
	for _, pair := range pairs {
		key, ok := pair.Key.(filter.Object)
		if !ok {
			return nil, fmt.Errorf("cache key is not a filter/factory object")
		}
		entry := store.SourceIntervalEntry{Key: key, Values: []store.SourceFragmentIntervals{}}
		values, err := historicalLiveObjects(pair.Value)
		if err != nil {
			return nil, err
		}
		for _, item := range values {
			fragment, ok := item["fragment"].(filter.Object)
			if !ok {
				return nil, fmt.Errorf("fragment is not an object")
			}
			spans, err := historicalLiveList(item["intervals"])
			if err != nil {
				return nil, err
			}
			intervals := []store.SourceInterval{}
			for _, value := range spans {
				span, err := historicalLiveSpan(value)
				if err != nil {
					return nil, err
				}
				intervals = append(intervals, span)
			}
			entry.Values = append(entry.Values, store.SourceFragmentIntervals{Fragment: fragment, Intervals: intervals})
		}
		cached = append(cached, entry)
	}
	return cached, nil
}

func sourceIndexReplayPure(operation string, args filter.Object) (any, error) {
	filters, err := historicalLiveObjects(args["filters"])
	if err != nil {
		return nil, err
	}
	cached, err := sourceIndexPureCache(args["cachedIntervals"])
	if err != nil {
		return nil, err
	}
	var result any
	switch operation {
	case "getCachedBlock":
		value, err := CachedSourceBlock(filters, cached)
		if err != nil {
			return nil, err
		}
		result = filter.Undefined
		if value != nil {
			result = *value
		}
	case "getRequiredIntervals", "getRequiredIntervalsWithFilters":
		span, err := historicalLiveSpan(args["interval"])
		if err != nil {
			return nil, err
		}
		if operation == "getRequiredIntervals" {
			intervals, err := RequiredSourceIntervalUnion(filters, span, cached)
			if err != nil {
				return nil, err
			}
			values := []any{}
			for _, interval := range intervals {
				values = append(values, []any{interval[0], interval[1]})
			}
			result = values
		} else {
			intervals, factories, err := RequiredSourceIntervals(filters, span, cached)
			if err != nil {
				return nil, err
			}
			intervalValues, factoryValues := []any{}, []any{}
			for _, item := range intervals {
				intervalValues = append(intervalValues, filter.Object{"filter": item.Filter, "interval": []any{item.Interval[0], item.Interval[1]}})
			}
			for _, item := range factories {
				factoryValues = append(factoryValues, filter.Object{"factory": item.Factory, "interval": []any{item.Interval[0], item.Interval[1]}})
			}
			result = filter.Object{"intervals": intervalValues, "factoryIntervals": factoryValues}
		}
	default:
		return nil, fmt.Errorf("unexpected operation %s", operation)
	}
	return result, nil
}

func TestSourceRuntimeIndexPureDeclarations(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/source-runtime-index-pure.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceCredit int  `json:"source_credit"`
		PristineFile bool `json:"pristine_file"`
		Cases        []struct {
			ID, Title  string
			Assertions []json.RawMessage
			Calls      []struct {
				Operation           string
				Args, Result, After any
				Error               json.RawMessage
			}
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct {
		title      string
		assertions int
	}{
		"928495e24f9b9eec3013": {"getCachedBlock() no cached intervals", 1},
		"c429e0be63d1e2a5621c": {"getCachedBlock() with cache", 2},
		"4096c895c425e67b73c8": {"getCachedBlock() with incomplete cache", 1},
		"0ad0d344d7cb00ef36b7": {"getCachedBlock() with multiple filters", 3},
		"02613908644ac30deaea": {"getCachedBlock() with factory", 2},
		"cfc3d65d78ce2707a366": {"getRequiredIntervals()", 3},
		"f6920d1d20a903b4f5a4": {"getRequiredIntervalsWithFilters()", 2},
		"1469b5672695375f7fe2": {"getRequiredIntervalsWithFilters() with factory", 1},
		"34b296bcde7be28a3324": {"getRequiredIntervalsWithFilters() with factory and missing parent", 1},
		"82f711b0ecef2ba1a7c9": {"getRequiredIntervals() with factory", 2},
	}
	if fixture.SourceCredit != 0 || fixture.PristineFile || len(fixture.Cases) != len(expected) {
		t.Fatal("require ten isolated declarations and zero pristine source credit")
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		want, ok := expected[c.ID]
		if !ok || seen[c.ID] || want.title != c.Title || len(c.Assertions) != want.assertions || len(c.Calls) != want.assertions {
			t.Fatalf("source census differs for %s", c.Title)
		}
		seen[c.ID] = true
		t.Run(c.Title, func(t *testing.T) {
			for i, call := range c.Calls {
				t.Run(fmt.Sprintf("call_%d", i+1), func(t *testing.T) {
					if len(call.Error) > 0 {
						t.Fatalf("source call failed: %s", call.Error)
					}
					registry := newSourceLiveRegistry()
					restored, err := registry.restore(call.Args)
					if err != nil {
						t.Fatal(err)
					}
					argsList, err := historicalLiveList(restored)
					if err != nil || len(argsList) != 1 {
						t.Fatalf("expected one argument: %v", err)
					}
					args, ok := argsList[0].(filter.Object)
					if !ok {
						t.Fatal("argument is not an object")
					}
					result, err := sourceIndexReplayPure(call.Operation, args)
					if err != nil {
						t.Fatal(err)
					}
					if err := registry.compare(result, call.Result, "result"); err != nil {
						t.Fatal(err)
					}
					if err := registry.compare(restored, call.After, "input_after"); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}
