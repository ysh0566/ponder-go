package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBridgeStartupReportsProcessFailure(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "node"), []byte("#!/bin/sh\nexit 23\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	backend, err := OpenPGlite(context.Background(), "memory://")
	if backend != nil || err == nil || !strings.Contains(err.Error(), "exit status 23") {
		t.Fatalf("startup must retain the child process failure: backend=%v error=%v", backend, err)
	}
}
