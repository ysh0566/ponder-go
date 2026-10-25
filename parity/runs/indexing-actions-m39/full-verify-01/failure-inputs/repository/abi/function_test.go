package abi

import (
	"encoding/json"
	"testing"
)

// Supplementary metadata boundary; no original source record credit.
func TestFunctionEntriesPreserveDuplicatesAndCollisions(t *testing.T) {
	entries := []Function{{Type: "function", Name: "burn", Inputs: []Parameter{{Type: "uint256"}}}, {Type: "function", Name: "collate_propagate_storage", Inputs: []Parameter{{Type: "bytes16"}}}, {Type: "function", Name: "f", Inputs: []Parameter{{Type: "uint256", Name: "x"}}}, {Type: "function", Name: "f", Inputs: []Parameter{{Type: "uint256", Name: "x"}}}}
	if FunctionSelector(entries[0]) != FunctionSelector(entries[1]) {
		t.Fatal("known selector collision fixture")
	}
	raw, e := json.Marshal(entries)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := Parse(string(raw))
	if e != nil {
		t.Fatal(e)
	}
	actual := parsed.FunctionEntries()
	if len(actual) != 4 {
		t.Fatal("entries collapsed", actual)
	}
	for i, entry := range actual {
		if entry.Name != entries[i].Name {
			t.Fatal("ABI order changed")
		}
	}
	if actual[2].SafeName(true) != "f(uint256 x)" {
		t.Fatal("duplicate-name source format", actual[2].SafeName(true))
	}
}
