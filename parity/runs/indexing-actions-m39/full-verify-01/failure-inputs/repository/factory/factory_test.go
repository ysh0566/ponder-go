package factory

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestUpstreamFactory(t *testing.T) {
	raw, e := os.ReadFile("../testdata/factory/build-cases.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		ID         string
		FullName   string `json:"full_name"`
		Assertions []struct {
			Input    struct{ Args []json.RawMessage }
			Matcher  string
			Expected json.RawMessage
		}
	}
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			t.Log(tc.FullName)
			for _, a := range tc.Assertions {
				var config Config
				if e = json.Unmarshal(a.Input.Args[0], &config); e != nil {
					t.Fatal(e)
				}
				actual, e := Build(config)
				if a.Matcher == "toThrowError" {
					var expected string
					if err := json.Unmarshal(a.Expected, &expected); err != nil {
						t.Fatal(err)
					}
					if e == nil || !strings.Contains(e.Error(), expected) {
						t.Fatalf("error %v, want substring %q", e, expected)
					}
					continue
				}
				if e != nil {
					t.Fatal(e)
				}
				var expected, got map[string]any
				if e = json.Unmarshal(a.Expected, &expected); e != nil {
					t.Fatal(e)
				}
				encoded, e := json.Marshal(actual)
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal(encoded, &got); e != nil {
					t.Fatal(e)
				}
				for key, want := range expected {
					if !reflect.DeepEqual(got[key], want) {
						t.Fatalf("toMatchObject %s: got %#v; want %#v", key, got[key], want)
					}
				}
			}
		})
	}
}
