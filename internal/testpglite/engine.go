// Package testpglite provides PGlite engine fault injection for integration tests.
package testpglite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Track returns a function that kills the child engine opened by this Go test
// for directory. The next open after the interruption is left uninstrumented.
func Track(t *testing.T, directory string) func() error {
	t.Helper()
	temp := t.TempDir()
	registration := filepath.Join(temp, "child.json")
	preload := filepath.Join(temp, "register.cjs")
	const source = `if (process.argv.at(-1) === process.env.PONDER_TEST_ENGINE_DIRECTORY) {
  require("node:fs").writeFileSync(process.env.PONDER_TEST_ENGINE_REGISTRATION,
    JSON.stringify({ pid: process.pid, parent: process.ppid, directory: process.argv.at(-1) }), {flag: "wx"});
}`
	if err := os.WriteFile(preload, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PONDER_TEST_ENGINE_DIRECTORY", directory)
	t.Setenv("PONDER_TEST_ENGINE_REGISTRATION", registration)
	t.Setenv("NODE_OPTIONS", strings.TrimSpace(os.Getenv("NODE_OPTIONS")+" --require="+strconv.Quote(preload)))
	return func() error {
		t.Setenv("PONDER_TEST_ENGINE_DIRECTORY", "")
		data, err := os.ReadFile(registration)
		if err != nil {
			return err
		}
		var child struct {
			PID, Parent int
			Directory   string
		}
		if err = json.Unmarshal(data, &child); err != nil {
			return err
		}
		if child.Parent != os.Getpid() || child.PID <= 1 || child.Directory != directory {
			return fmt.Errorf("unexpected PGlite child registration: %s", data)
		}
		p, err := os.FindProcess(child.PID)
		if err != nil {
			return err
		}
		defer p.Release()
		return p.Kill()
	}
}
