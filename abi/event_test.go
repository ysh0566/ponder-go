package abi

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type eventCase struct {
	ID         string
	FullName   string `json:"full_name"`
	Assertions []struct {
		Input struct {
			ABIItem Event `json:"abiItem"`
			Topics  []string
			Data    string
		}
		Matcher  string
		Expected any
		Error    struct {
			Contains *string
			Regex    string
		}
		RawSnapshot string `json:"raw_snapshot"`
	}
}

func canonical(v any) any {
	switch x := v.(type) {
	case *big.Int:
		return map[string]any{"$bigint": x.String()}
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = canonical(v)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			out[k] = canonical(v)
		}
		return out
	case int64:
		return json.Number(fmt.Sprint(x))
	case uint64:
		return json.Number(fmt.Sprint(x))
	case int:
		return json.Number(fmt.Sprint(x))
	}
	return v
}

// These fixtures are extracted from the original callbacks, not from a Go
// execution. Every original expectation is replayed, including the full inline
// snapshot value and all three assertions in "data + event params mismatch".
func TestUpstreamEventLog(t *testing.T) {
	raw, e := os.ReadFile("../testdata/abi/event-cases.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []eventCase
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if e = d.Decode(&cases); e != nil {
		t.Fatal(e)
	}
	for _, tc := range cases {
		t.Run(tc.FullName, func(t *testing.T) {
			for i, a := range tc.Assertions {
				result, e := DecodeEventLog(a.Input.ABIItem, a.Input.Topics, a.Input.Data)
				if a.Matcher == "toThrow" || a.Matcher == "toThrowError" {
					if e == nil {
						t.Fatalf("assertion %d: expected error", i)
					}
					if a.Error.Contains != nil && !strings.Contains(e.Error(), *a.Error.Contains) {
						t.Fatalf("assertion %d: got error %q; expected substring %q", i, e, *a.Error.Contains)
					}
					if a.Error.Regex != "" && !regexp.MustCompile(a.Error.Regex).MatchString(e.Error()) {
						t.Fatalf("assertion %d: error %q does not match %s", i, e, a.Error.Regex)
					}
					continue
				}
				if e != nil {
					t.Fatalf("assertion %d: %v", i, e)
				}
				got := canonical(result)
				if !reflect.DeepEqual(got, a.Expected) {
					t.Fatalf("assertion %d (%s):\ngot %#v\nwant %#v", i, a.Matcher, got, a.Expected)
				}
			}
		})
	}
}
