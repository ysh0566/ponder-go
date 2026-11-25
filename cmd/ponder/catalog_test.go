package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"ponder.local/ponder-go/abi"
)

func TestCodegenABICatalog(t *testing.T) {
	dir := t.TempDir()
	file, out := filepath.Join(dir, "contract.json"), filepath.Join(dir, "bindings.go")
	input := []byte(`[{"type":"function","name":"balance","inputs":[],"outputs":[],"stateMutability":"view"}]`)
	if err := os.WriteFile(file, input, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"codegen", "--abi", file, "--package", "bindings", "--out", out}
	if err := run(t.Context(), args, io.Discard); err != nil {
		t.Fatal(err)
	}
	want, err := abi.GenerateCatalog("bindings", input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("CLI output differs from the ABI catalog", err)
	}
	if err = run(t.Context(), append(args, "--typed"), io.Discard); err == nil {
		t.Fatal("accepted conflicting code generators")
	}
	if err = os.WriteFile(file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), args, io.Discard); err == nil {
		t.Fatal("generated an invalid ABI")
	}
	got, err = os.ReadFile(out)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("invalid input overwrote generated code", err)
	}
}
