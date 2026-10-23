package abi

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

type parameterInput struct {
	Params        []Parameter
	Data          string
	FormatAddress string `json:"format_address"`
}
type parameterCase struct {
	ID          string
	FullName    string `json:"full_name"`
	SourceState string `json:"source_state"`
	Assertions  []struct {
		Input    parameterInput
		Matcher  string
		Expected any
		Error    struct{ Contains *string }
	}
}

func TestUpstreamParameters(t *testing.T) {
	raw, e := os.ReadFile("../testdata/abi/parameter-cases.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []parameterCase
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if e = d.Decode(&cases); e != nil {
		t.Fatal(e)
	}
	for _, tc := range cases {
		// Upstream skips remain inventoried. They are not run or counted as Go
		// passes, and no mapping gives them active parity credit.
		if tc.SourceState != "active" {
			continue
		}
		t.Run(tc.ID, func(t *testing.T) {
			t.Log(tc.FullName)
			for i, a := range tc.Assertions {
				if a.Matcher == "expectTypeOf" {
					continue
				} // Required separately by the compile-fixture ledger.
				options := DecodeOptions{}
				if a.Input.FormatAddress == "lowercase" {
					options.FormatAddress = strings.ToLower
				}
				value, e := DecodeParameters(a.Input.Params, a.Input.Data, options)
				if strings.HasPrefix(a.Matcher, "toThrow") {
					if e == nil {
						t.Fatalf("assertion %d: expected error", i)
					}
					if a.Error.Contains != nil && !strings.Contains(e.Error(), *a.Error.Contains) {
						t.Fatalf("assertion %d: got %q; want substring %q", i, e, *a.Error.Contains)
					}
					continue
				}
				if e != nil {
					t.Fatalf("assertion %d: %v", i, e)
				}
				if !reflect.DeepEqual(canonical(value), a.Expected) {
					t.Fatalf("assertion %d: got %#v; want %#v", i, canonical(value), a.Expected)
				}
			}
		})
	}
}
