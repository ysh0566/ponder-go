// abi-codegen emits Go ABI decoders from a JSON list of named parameter schemas.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"ponder.local/ponder-go/abi"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: abi-codegen <input.json> <output-directory>")
		os.Exit(2)
	}
	raw, e := os.ReadFile(os.Args[1])
	if e != nil {
		fail(e)
	}
	var schemas []struct {
		Package string
		Name    string
		Params  []abi.Parameter
	}
	if e = json.Unmarshal(raw, &schemas); e != nil {
		fail(e)
	}
	if e = os.MkdirAll(os.Args[2], 0755); e != nil {
		fail(e)
	}
	for _, schema := range schemas {
		code, e := abi.GenerateParameters(schema.Package, schema.Name, schema.Params)
		if e != nil {
			fail(e)
		}
		if e = os.WriteFile(filepath.Join(os.Args[2], schema.Name+".go"), code, 0644); e != nil {
			fail(e)
		}
	}
}
func fail(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
