package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	project := `package main
import("fmt"; "os")
func main(){fmt.Print(os.Getenv("PONDER_COMMAND"),"/",os.Getenv("DATABASE_SCHEMA"),"/",os.Getenv("DATABASE_VIEWS_SCHEMA"),"/",os.Getenv("PONDER_SHADOW_SCHEMA"))}`
	root := writeApplication(t, project)
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

func writeApplication(t *testing.T, program string) string {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string]string{"go.mod": "module example\n\ngo 1.25\n", "main.go": program} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestStartRejectsNonpositiveShutdownTimeout(t *testing.T) {
	for _, timeout := range []string{"0s", "-1s"} {
		err := run(t.Context(), []string{"start", "--root", t.TempDir(), "--shutdown-timeout", timeout}, io.Discard)
		if err == nil || err.Error() != "shutdown timeout must be positive" {
			t.Fatalf("shutdown timeout %s: %v", timeout, err)
		}
	}
}

func TestLaunchGracefulShutdown(t *testing.T) {
	program := `package main
import("os";"os/signal";"syscall")
func main() {
	sig:=make(chan os.Signal,1)
	signal.Notify(sig,os.Interrupt,syscall.SIGTERM)
	if err:=os.WriteFile("ready",nil,0600);err!=nil{panic(err)}
	<-sig
	if err:=os.WriteFile("closed",nil,0600);err!=nil{panic(err)}
}`
	root := writeApplication(t, program)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	exit := make(chan error, 1)
	go func() { exit <- run(ctx, []string{"start", "--root", root}, io.Discard) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(root, "ready")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-exit:
			t.Fatalf("child exited before readiness: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	cancel()
	if err := <-exit; err != nil {
		t.Errorf("graceful stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "closed")); err != nil {
		t.Fatal("child did not finish shutdown", err)
	}
}
