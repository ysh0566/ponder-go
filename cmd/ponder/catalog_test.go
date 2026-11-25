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

func TestCodegenValueUtilities(t *testing.T) {
	dir := t.TempDir()
	one, two, out := filepath.Join(dir, "one.json"), filepath.Join(dir, "two.json"), filepath.Join(dir, "generated.go")
	for file, content := range map[string]string{one: `[{"type":"constructor","inputs":[]}]`, two: `[{"type":"function","name":"a","inputs":[],"outputs":[]}]`} {
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--merge-abi", one, "--merge-abi", two}, "Item0 FunctionItemA"},
		{[]string{"--bigint-shape", "[2]BigInt"}, "[2]BigInt"},
	} {
		args := append([]string{"codegen", "--out", out}, c.args...)
		if err := run(t.Context(), args, io.Discard); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(out)
		if err != nil || !bytes.Contains(data, []byte(c.want)) {
			t.Fatalf("missing %s in output: %v", c.want, err)
		}
		if err := run(t.Context(), append(args, "--typed"), io.Discard); err == nil {
			t.Fatal("accepted conflicting modes")
		}
		after, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(after, data) {
			t.Fatal("failed call changed the output", err)
		}
	}
}

func TestCodegenApplicationBindings(t *testing.T) {
	dir := t.TempDir()
	file, out := filepath.Join(dir, "app.json"), filepath.Join(dir, "bindings.go")
	if err := os.WriteFile(file, []byte(`{"contracts":{"token":{"abi":[{"type":"event","name":"Mint","inputs":[]}]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"codegen", "--app-config", file, "--package", "bindings", "--out", out}
	for _, dynamic := range []bool{false, true} {
		call := args
		if dynamic {
			call = append(call, "--dynamic-abi", "token")
		}
		if err := run(t.Context(), call, io.Discard); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(out)
		if err != nil || !bytes.Contains(data, []byte("NameTokenSetup struct")) || bytes.Contains(data, []byte("NameTokenMint struct")) == dynamic {
			t.Fatal(string(data), err)
		}
	}
	if err := run(t.Context(), append(args, "--typed"), io.Discard); err == nil {
		t.Fatal("accepted two generators")
	}
	if err := run(t.Context(), []string{"codegen", "--dynamic-abi", "token"}, io.Discard); err == nil {
		t.Fatal("accepted a dynamic contract without an application")
	}
}
