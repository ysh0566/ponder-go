package filter

import (
	"encoding/json"
	"os"
	"testing"
)

type sourceCall struct {
	Operation string
	Args      []any
	Result    any
}
type sourceAssertion struct {
	Matcher   string
	CallIndex int `json:"call_index"`
	Expected  any
}
type fragmentSourceCase struct {
	ID         string
	Name       string `json:"source_name"`
	Calls      []sourceCall
	Assertions []sourceAssertion
}

func TestUpstreamFragments(t *testing.T) {
	raw, e := os.ReadFile("../testdata/runtime/fragments.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct{ Cases []fragmentSourceCase }
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			results := make([]any, len(c.Calls))
			for i, call := range c.Calls {
				args := FixtureValue(call.Args).([]any)
				var result any
				var err error
				switch call.Operation {
				case "getFragments":
					result, err = GetFragments(object(args[0]))
				case "encodeFragment":
					result = EncodeFragment(object(args[0]))
				case "decodeFragment":
					result, err = DecodeFragment(str(args[0]))
				case "recoverFilter":
					xs := []Object{}
					for _, x := range args[1].([]any) {
						xs = append(xs, object(x))
					}
					result = RecoverFilter(object(args[0]), xs)
				default:
					t.Fatalf("unknown source operation %q", call.Operation)
				}
				if err != nil {
					t.Fatal(err)
				}
				results[i] = result
				actual, e := json.Marshal(result)
				if e != nil {
					t.Fatal(e)
				}
				want, e := json.Marshal(FixtureValue(call.Result))
				if e != nil {
					t.Fatal(e)
				}
				if string(actual) != string(want) {
					t.Fatalf("source operation %d %s:\ngot %s\nwant %s", i, call.Operation, actual, want)
				}
			}
			for i, a := range c.Assertions {
				if a.CallIndex < 0 || a.CallIndex >= len(results) {
					t.Fatal("source assertion has no operation", i)
				}
				actual, _ := json.Marshal(results[a.CallIndex])
				want, _ := json.Marshal(FixtureValue(a.Expected))
				if string(actual) != string(want) {
					t.Fatalf("source assertion %d %s:\ngot %s\nwant %s", i, a.Matcher, actual, want)
				}
			}
		})
	}
}
