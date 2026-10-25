package runtime

import (
	"encoding/json"
	"os"
	"testing"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

func TestSourceRequiredIntervalVectors(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/source-required-intervals.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name                string
			Args, Result, After any
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 25 {
		t.Fatal("all25 source helper vectors required")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			registry := newSourceLiveRegistry()
			value, err := registry.restore(c.Args)
			if err != nil {
				t.Fatal(err)
			}
			args := value.(filter.Object)
			filters, err := historicalLiveObjects(args["filters"])
			if err != nil {
				t.Fatal(err)
			}
			span, err := historicalLiveSpan(args["interval"])
			if err != nil {
				t.Fatal(err)
			}
			cached := []store.SourceIntervalEntry{}
			for _, pair := range args["cachedIntervals"].(historicalLiveMap) {
				entry := store.SourceIntervalEntry{Key: pair.Key.(filter.Object), Values: []store.SourceFragmentIntervals{}}
				values, err := historicalLiveObjects(pair.Value)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range values {
					spans, err := historicalLiveList(item["intervals"])
					if err != nil {
						t.Fatal(err)
					}
					intervals := []store.SourceInterval{}
					for _, value := range spans {
						span, err := historicalLiveSpan(value)
						if err != nil {
							t.Fatal(err)
						}
						intervals = append(intervals, span)
					}
					entry.Values = append(entry.Values, store.SourceFragmentIntervals{Fragment: item["fragment"].(filter.Object), Intervals: intervals})
				}
				cached = append(cached, entry)
			}
			required, factories, err := RequiredSourceIntervals(filters, span, cached)
			if err != nil {
				t.Fatal(err)
			}
			actualRequired, actualFactories := []any{}, []any{}
			for _, item := range required {
				actualRequired = append(actualRequired, filter.Object{"filter": item.Filter, "interval": []any{item.Interval[0], item.Interval[1]}})
			}
			for _, item := range factories {
				actualFactories = append(actualFactories, filter.Object{"factory": item.Factory, "interval": []any{item.Interval[0], item.Interval[1]}})
			}
			if err = registry.compare(filter.Object{"intervals": actualRequired, "factoryIntervals": actualFactories}, c.Result, "required"); err != nil {
				t.Fatal(err)
			}
			if err = registry.compare(args, c.After, "input_after"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
