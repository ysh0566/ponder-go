package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestCreateEmptyProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "empty")
	if err := run(context.Background(), []string{"init", "--template", "typescript", root}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	reference, err := os.ReadFile("testdata/empty-project.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reference, &want); err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	err = fs.WalkDir(os.DirFS(root), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := fs.ReadFile(os.DirFS(root), path)
		if err != nil {
			return err
		}
		if path == ".env.local" {
			// PGlite is the current embedded database. Normalize the one
			// corrected comment before comparing with the pinned CLI output.
			if !strings.Contains(string(body), "PGlite will be used.") {
				t.Fatal("missing current embedded database name")
			}
			body = []byte(strings.Replace(string(body), "PGlite will be used.", "SQLite will be used. ", 1))
		}
		files[path] = fmt.Sprintf("%x", sha256.Sum256(body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 11 || !reflect.DeepEqual(files, want) {
		t.Fatalf("empty project files: got %v, want %v", files, want)
	}
}

func TestCreateProjectConcurrent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-project")
	done := make(chan error, 2)
	var joined sync.WaitGroup
	for range 2 {
		joined.Add(1)
		go func() {
			defer joined.Done()
			done <- run(context.Background(), []string{"init", "--template", "typescript", dir}, io.Discard)
		}()
	}
	joined.Wait()
	close(done)
	successes := 0
	for err := range done {
		if err == nil {
			successes++
		} else if !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful creators: %d", successes)
	}
	var pkg struct{ Name string }
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Name != "my-project" {
		t.Fatalf("manifest name: %q (%v)", pkg.Name, err)
	}
}

func TestInitGoProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "my-indexer")
	if err := run(t.Context(), []string{"init", root}, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"schema.json", "main.go"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "examples", "erc20", name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("generated %s differs from executable example", name)
		}
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.HasPrefix(string(mod), "module my-indexer\n") {
		t.Fatalf("unexpected module %s: %v", mod, err)
	}
}
