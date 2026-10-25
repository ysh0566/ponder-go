package runtime

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"ponder.local/ponder-go/store"
)

// Supplementary isolated original helper vectors; no original test mappings.
func TestSourceHistoricalMergeVectors(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/source-historical-merge.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name       string
			Generators []struct {
				Interval store.SourceInterval
				Yields   []store.SourceInterval
			}
			Result []store.SourceInterval
			Trace  []map[string]any
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 12 {
		t.Fatal("all12 actual source vectors required")
	}
	normalize := func(value any) any {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err = json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			trace := []map[string]any{}
			generators := []SourceIntervalGenerator{}
			for index, g := range c.Generators {
				position := 0
				generators = append(generators, SourceIntervalGenerator{Interval: g.Interval, Next: func(ctx context.Context) <-chan SourceIntervalStep {
					task := make(chan SourceIntervalStep, 1)
					if position > 0 {
						trace = append(trace, map[string]any{"kind": "generator_resume", "index": index})
					}
					if position == len(g.Yields) {
						trace = append(trace, map[string]any{"kind": "generator_done", "index": index})
						task <- SourceIntervalStep{Done: true}
					} else {
						interval := g.Yields[position]
						trace = append(trace, map[string]any{"kind": "generator_yield", "index": index, "interval": interval})
						position++
						task <- SourceIntervalStep{Interval: interval}
					}
					close(task)
					return task
				}})
			}
			result := []store.SourceInterval{}
			for interval, err := range MergeSourceGeneratorIntervals(context.Background(), generators) {
				if err != nil {
					t.Fatal(err)
				}
				trace = append(trace, map[string]any{"kind": "consumer", "interval": interval})
				result = append(result, interval)
			}
			if !reflect.DeepEqual(normalize(result), normalize(c.Result)) {
				t.Fatal("actual source ranges", result, c.Result)
			}
			if !reflect.DeepEqual(normalize(trace), normalize(c.Trace)) {
				t.Fatal("actual source next/yield/resume ordering", trace, c.Trace)
			}
		})
	}
}
