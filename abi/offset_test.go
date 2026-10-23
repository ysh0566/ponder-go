package abi

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestUpstreamOffset(t *testing.T) {
	raw, e := os.ReadFile("../testdata/abi/offset-cases.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		ID         string
		FullName   string `json:"full_name"`
		Assertions []struct {
			Input struct {
				Operation string
				Args      []json.RawMessage
			}
			Expected uint64
		}
	}
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			t.Log(tc.FullName)
			for i, a := range tc.Assertions {
				var p Parameter
				if e = json.Unmarshal(a.Input.Args[0], &p); e != nil {
					t.Fatal(e)
				}
				var actual uint64
				var e error
				switch a.Input.Operation {
				case "getBytesConsumedByParam":
					actual, e = BytesConsumed(p)
				case "getNestedParamOffset":
					var names []string
					if e = json.Unmarshal(a.Input.Args[1], &names); e == nil {
						actual, e = NestedOffset(p, names)
					}
				default:
					e = fmt.Errorf("unexpected symbolic operation")
				}
				if e != nil {
					t.Fatal(e)
				}
				if actual != a.Expected {
					t.Fatalf("assertion %d: got %d, want %d", i, actual, a.Expected)
				}
			}
		})
	}
}
