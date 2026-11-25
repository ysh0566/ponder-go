package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/schema"
)

func TestSourceCallbackContext(t *testing.T) {
	var app struct{ Application json.RawMessage }
	var fixture struct {
		Schema schema.Schema
		Cases  []struct{ ID, Title string }
	}
	readJSON(t, "testdata/virtual-names.json", &app)
	readJSON(t, "testdata/callback-context.json", &fixture)
	source, err := config.ParseSourceConfiguration(app.Application)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := config.GenerateApplicationRegistry("app", source)
	if err != nil {
		t.Fatal(err)
	}
	db, err := orm.GenerateCallbackDatabase("model", fixture.Schema)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	caller, err := os.ReadFile("testdata/callback-context-caller.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"app/registry.go": bindings, "model/db.go": db, "go.mod": []byte("module ctx.test\n\ngo 1.25.0\n"), "go.work": fmt.Appendf(nil, "go 1.25.0\nuse (\n.\n%q\n)\n", root), "caller_test.go": caller} {
		filename := filepath.Join(dir, name)
		if err = os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filename, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "caller")
	args := []string{"test", "-c", "-o", bin}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "-race" && s.Value == "true" {
				args = append(args, "-race")
			}
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile callback registry: %v\n%s", err, out)
	}
	for _, c := range fixture.Cases {
		t.Run(c.ID, func(t *testing.T) {
			run := func(t *testing.T, test, backend string) {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), bin, "-test.run", "^Test"+test+"$", "-test.v")
				cmd.Env = append(os.Environ(), "CALLBACK_DB="+backend)
				out, err := cmd.CombinedOutput()
				if err != nil || !strings.Contains(string(out), "--- PASS: Test"+test) {
					t.Fatalf("%s: %v\n%s", c.Title, err, out)
				}
			}
			switch c.Title {
			case "Context db":
				for _, backend := range []string{"postgres", "pglite"} {
					t.Run(backend, func(t *testing.T) {
						if backend == "postgres" && os.Getenv("PONDER_TEST_POSTGRES") == "" {
							t.Skip("requires explicit test PostgreSQL")
						}
						run(t, "DB", backend)
					})
				}
			case "Context client":
				run(t, "Client", "")
			case "Context contracts":
				run(t, "Contracts", "")
			case "Registry":
				run(t, "Registry", "")
			default:
				t.Fatal(c.Title)
			}
		})
	}
}
