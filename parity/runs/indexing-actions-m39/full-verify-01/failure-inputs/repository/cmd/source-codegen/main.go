package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ponder.local/ponder-go/config"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: source-codegen <app-bindings.json> <output.go>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	var input struct {
		Package string
		Chains  []string
		ABI     json.RawMessage
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		fail(err)
	}
	code, err := config.GenerateSourceContractBindings(input.Package, input.Chains, input.ABI)
	if err != nil {
		fail(err)
	}
	if err = os.MkdirAll(filepath.Dir(os.Args[2]), 0755); err != nil {
		fail(err)
	}
	if err = os.WriteFile(os.Args[2], code, 0644); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
