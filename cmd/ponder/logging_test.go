package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/logging"
)

func parseLogRecords(t *testing.T, s string) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("non-JSON log: %q (%v)", line, err)
		}
		if record["time"] == nil || record["msg"] == nil || record["level"] == nil {
			t.Fatalf("missing log field: %v", record)
		}
		entries = append(entries, record)
	}
	return entries
}

func TestGlobalLogOptions(t *testing.T) {
	t.Setenv("PONDER_LOG_LEVEL", "warn")
	t.Setenv("PONDER_LOG_FORMAT", "pretty")
	cases := []struct {
		args, rest    []string
		level, format string
	}{
		{[]string{"--log-format", "json", "start", "--debug"}, []string{"start"}, "debug", "json"},
		{[]string{"dev", "-v", "--trace", "--log-level=error"}, []string{"dev"}, "error", "pretty"},
		{[]string{"--log-level", "debug", "serve", "-vv"}, []string{"serve"}, "debug", "pretty"},
		{[]string{"checkpoint", "decode", "000", "--log-format=json"}, []string{"checkpoint", "decode", "000"}, "warn", "json"},
		{[]string{"init", "--", "--trace"}, []string{"init", "--", "--trace"}, "warn", "pretty"},
		{[]string{"codegen", "--out", "--trace", "--typed", "-vv"}, []string{"codegen", "--out", "--trace", "--typed"}, "trace", "pretty"},
	}
	for _, tc := range cases {
		rest, cfg, err := logOptions(tc.args)
		if err != nil || cfg.Level != tc.level || cfg.Format != tc.format || !reflect.DeepEqual(rest, tc.rest) {
			t.Fatal(tc, rest, cfg, err)
		}
	}
	for _, args := range [][]string{{"--log-format=json", "--log-level", "bad"}, {"--log-format=json", "--log-level"}, {"--log-format=json", "--log-format=invalid"}} {
		var out, diag strings.Builder
		if err := runCLI(t.Context(), args, &out, &diag); err == nil || out.Len() != 0 {
			t.Fatal(args, out.String(), err)
		}
		if rows := parseLogRecords(t, diag.String()); len(rows) != 1 || rows[0]["level"] != float64(50) {
			t.Fatal(rows)
		}
	}
	var out, diag strings.Builder
	if err := runCLI(t.Context(), []string{"--log-level", "silent", "--log-format=json", "--version"}, &out, &diag); err != nil || out.String() != version+"\n" || diag.Len() != 0 {
		t.Fatal(out.String(), diag.String(), err)
	}
	t.Setenv("PONDER_LOG_LEVEL", "bogus")
	if logging.Environment().Level != "info" {
		t.Fatal("invalid inherited level")
	}
}

func TestGoProcessLogSettings(t *testing.T) {
	const program = `package main
import("fmt";"os")
func main(){fmt.Print(os.Getenv("PONDER_LOG_LEVEL")+"/"+os.Getenv("PONDER_LOG_FORMAT"))}`
	root := writeApplication(t, program)
	for _, mode := range []string{"start", "serve"} {
		var out, diag strings.Builder
		err := runCLI(t.Context(), []string{"--log-format=json", mode, "--root", root, "-vv"}, &out, &diag)
		if err != nil || out.String() != "trace/json" {
			t.Fatal(mode, out.String(), err)
		}
		if len(parseLogRecords(t, diag.String())) != 2 {
			t.Fatal(diag.String())
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc broken(\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var diag strings.Builder
	if err := runCLI(t.Context(), []string{"start", "--root", root, "--log-format=json", "--log-level", "error"}, io.Discard, &diag); err == nil {
		t.Fatal("bad build succeeded")
	}
	records := parseLogRecords(t, diag.String())
	if len(records) < 2 {
		t.Fatalf("missing compiler diagnostic: %v", records)
	}
	for _, r := range records {
		if r["level"] != float64(50) {
			t.Fatal(r)
		}
	}
}

func TestDevProcessLogSettings(t *testing.T) {
	root := writeApplication(t, `package main
import("os";"os/signal";"syscall")
func main(){c:=make(chan os.Signal,1);signal.Notify(c,syscall.SIGINT);os.WriteFile("ready",[]byte(os.Getenv("PONDER_LOG_LEVEL")+"/"+os.Getenv("PONDER_LOG_FORMAT")),0600);<-c}`)
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	var diag strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- runCLI(ctx, []string{"dev", "--root", root, "-vv", "--log-format=json"}, io.Discard, &diag)
	}()
	for {
		if data, err := os.ReadFile(filepath.Join(root, "ready")); err == nil {
			if string(data) != "trace/json" {
				t.Error(string(data))
			}
			break
		}
		select {
		case err := <-done:
			t.Fatalf("dev exited before startup: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(30 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range parseLogRecords(t, diag.String()) {
		seen[fmt.Sprint(r["level"])] = true
	}
	if !seen["10"] || !seen["20"] || !seen["30"] {
		t.Fatal(diag.String())
	}
}
