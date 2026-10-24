package store

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// Exact original four assertions, also exercised by the complete source bridge.
func TestUpstreamFilterBlockRange(t *testing.T) {
	n := func(v float64) *float64 { return &v }
	if got := GetSourceFilterBlockRange([]SourceFilterRange{{n(10), n(80)}, {n(20), n(90)}}, 0, 100); got != (SourceBlockRange{10, 90}) {
		t.Fatal(got)
	}
	if got := GetSourceFilterBlockRange([]SourceFilterRange{{n(10), n(80)}, {nil, nil}}, 20, 70); got != (SourceBlockRange{20, 70}) {
		t.Fatal(got)
	}
	if got := GetSourceFilterBlockRange([]SourceFilterRange{{n(0), n(30)}}, 20, 90); got != (SourceBlockRange{20, 30}) {
		t.Fatal(got)
	}
	if got := GetSourceFilterBlockRange(nil, 20, 90); got != (SourceBlockRange{20, 90}) {
		t.Fatal(got)
	}
}

// Supplementary differential inputs, including the source Number domain.
func TestSourceFilterBlockRangeVectors(t *testing.T) {
	raw, err := os.ReadFile("../testdata/rpc/source-range.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name string
			Args struct {
				Filters            []map[string]any
				FromBlock, ToBlock any
			}
			Result struct{ FromBlock, ToBlock any }
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 12 {
		t.Fatal("all12 actual source vectors required")
	}
	number := func(value any) float64 {
		if n, ok := value.(float64); ok {
			return n
		}
		marker, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("invalid source number: %#v", value)
		}
		switch marker["$number"] {
		case "NaN":
			return math.NaN()
		case "Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		case "-0":
			return math.Copysign(0, -1)
		}
		t.Fatalf("invalid source number marker: %#v", value)
		return 0
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			filters := []SourceFilterRange{}
			bound := func(value any) *float64 {
				if value == nil {
					return nil
				}
				if m, ok := value.(map[string]any); ok && m["$undefined"] == true {
					return nil
				}
				n := number(value)
				return &n
			}
			for _, f := range c.Args.Filters {
				filters = append(filters, SourceFilterRange{bound(f["fromBlock"]), bound(f["toBlock"])})
			}
			got := GetSourceFilterBlockRange(filters, number(c.Args.FromBlock), number(c.Args.ToBlock))
			for _, pair := range [][2]float64{{got.FromBlock, number(c.Result.FromBlock)}, {got.ToBlock, number(c.Result.ToBlock)}} {
				if math.IsNaN(pair[1]) {
					if !math.IsNaN(pair[0]) {
						t.Fatal("expected NaN", got)
					}
					continue
				}
				if pair[0] != pair[1] || (pair[1] == 0 && math.Signbit(pair[0]) != math.Signbit(pair[1])) {
					t.Fatal("source range mismatch", pair, got)
				}
			}
		})
	}
}
