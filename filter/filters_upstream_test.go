package filter

import (
	"encoding/json"
	"os"
	"testing"

	"ponder.local/ponder-go/internal/testanvil"
	"ponder.local/ponder-go/rpc"
)

func sourceAnvilFixture(t *testing.T) *rpc.Client {
	t.Helper()
	c := rpc.New(testanvil.Start(t))
	c.Retries = 0
	c.RequestsPerSecond = 0
	return c
}

func TestUpstreamFilters(t *testing.T) {
	raw, e := os.ReadFile("../testdata/runtime/filters.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Name       string `json:"source_name"`
			Calls      []sourceCall
			Assertions []struct {
				sourceAssertion
				Path []string
			}
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			sourceAnvilFixture(t)
			results := make([]any, len(c.Calls))
			for i, call := range c.Calls {
				args := FixtureValue(call.Args).([]any)
				input := object(args[0])
				var result any
				switch call.Operation {
				case "getChildAddress":
					result = GetChildAddress(object(input["log"]), object(input["factory"]))
				case "mergeLogFiltersToBlockRequest":
					filters, factories := []Object{}, []Object{}
					for _, x := range input["logFilters"].([]any) {
						filters = append(filters, object(x))
					}
					for _, x := range input["factories"].([]any) {
						factories = append(factories, object(x))
					}
					result = MergeLogFiltersToBlockRequest(filters, factories)
				default:
					t.Fatal("unimplemented source operation", call.Operation)
				}
				results[i] = result
				actual, _ := json.Marshal(result)
				want, _ := json.Marshal(FixtureValue(call.Result))
				if string(actual) != string(want) {
					t.Fatalf("source operation %d:\ngot %s\nwant %s", i, actual, want)
				}
			}
			for i, a := range c.Assertions {
				actual := results[a.CallIndex]
				for _, key := range a.Path {
					actual = get(object(actual), key)
				}
				if a.Matcher == "toHaveLength" {
					xs, ok := arr(actual)
					if !ok || float64(len(xs)) != num(a.Expected) {
						t.Fatal("source length assertion", i, actual, a.Expected)
					}
					continue
				}
				got, _ := json.Marshal(actual)
				want, _ := json.Marshal(FixtureValue(a.Expected))
				if string(got) != string(want) {
					t.Fatalf("source assertion %d %s:\ngot %s\nwant %s", i, a.Matcher, got, want)
				}
			}
		})
	}
}
