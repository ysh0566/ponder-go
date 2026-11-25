package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceNamespaces(t *testing.T) {
	for _, c := range []struct{ role, flag string }{{"schema", "--schema"}, {"views schema", "--views-schema"}} {
		t.Run(c.role, func(t *testing.T) {
			t.Setenv("DATABASE_SCHEMA", "public")
			t.Setenv("DATABASE_VIEWS_SCHEMA", "")
			root := t.TempDir()
			for _, reserved := range []string{"ponder_sync", "ponder_rpc_cache_1"} {
				err := run(t.Context(), []string{"start", "--root", root, c.flag, reserved}, io.Discard)
				want := fmt.Sprintf("Invalid %s name. %q is a reserved schema name.", c.role, reserved)
				if err == nil || err.Error() != want {
					t.Fatalf("%s: got %v, want %s", reserved, err, want)
				}
			}
			if _, err := os.Stat(filepath.Join(root, ".ponder-go")); !os.IsNotExist(err) {
				t.Fatal("started building before namespace validation")
			}
		})
	}
}

func TestLaunchNamespaces(t *testing.T) {
	root := t.TempDir()
	project := `package main
import("fmt"; "os")
func main(){fmt.Print(os.Getenv("PONDER_COMMAND"),"/",os.Getenv("DATABASE_SCHEMA"),"/",os.Getenv("DATABASE_VIEWS_SCHEMA"),"/",os.Getenv("PONDER_SHADOW_SCHEMA"))}`
	for name, data := range map[string]string{"go.mod": "module example\n\ngo 1.25\n", "main.go": project} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DATABASE_SCHEMA", "ponder_go_env")
	t.Setenv("DATABASE_VIEWS_SCHEMA", "views_env")
	for _, c := range []struct {
		command string
		args    []string
		want    string
	}{
		{"start", nil, "start/ponder_go_env/views_env/ponder_go_env"},
		{"serve", []string{"--schema", "ponder_go_cli", "--views-schema", "views_cli"}, "serve/ponder_go_cli/views_cli/ponder_go_cli"},
	} {
		var out bytes.Buffer
		if err := run(t.Context(), append([]string{c.command, "--root", root}, c.args...), &out); err != nil || strings.TrimSpace(out.String()) != c.want {
			t.Fatal(err, out.String())
		}
	}
}
