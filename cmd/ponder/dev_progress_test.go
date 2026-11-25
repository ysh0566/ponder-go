package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/internal/devstatus"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestDevProgressLayout(t *testing.T) {
	d := devDashboard{message: "Watching Go project", report: devstatus.Snapshot{
		Address: "127.0.0.1:9000", Ready: true,
		Blocks: map[string]uint64{"z": 0, "a\n\x1b": 10, "b": 9},
	}}
	rows := d.lines(80, 24)
	if strings.Join(rows, "\n") != "Ponder dev | Watching Go project\nReady | http://127.0.0.1:9000\na\\n\\x1b  block 10\nb  block 9\nz  block 0" {
		t.Fatal(rows)
	}
	rows = d.lines(10, 4)
	if len(rows) != 3 || rows[2] != "+3 more" {
		t.Fatal(rows)
	}
	for _, line := range rows {
		if len(line) > 9 {
			t.Fatal(line)
		}
	}
	d.report.Ready = false
	d.report.Blocks = nil
	rows = d.lines(80, 24)
	if rows[2] != "Checkpoints unavailable" || !strings.HasPrefix(rows[1], "Syncing") {
		t.Fatal(rows)
	}
	d.report.Blocks = map[string]uint64{}
	if d.lines(80, 24)[2] != "Waiting for first checkpoint" {
		t.Fatal("empty progress")
	}
	eta := 11.1
	d.report.Chains = map[string]devstatus.ChainMetrics{"a": {RequestsPerSecond: 2.25, Backfill: &devstatus.Backfill{Total: 3, Completed: 2, ETASeconds: &eta}}}
	d.report.Events = map[string]devstatus.EventMetrics{"Token:\x1b": {Count: 8, AverageMillis: 0.125}}
	text := strings.Join(d.lines(100, 24), "\n")
	if !strings.Contains(text, "a  RPC 2.2 req/s  backfill 66.7%  ETA 12s") || !strings.Contains(text, `Token:\x1b  calls 8  avg 0.125 ms`) {
		t.Fatal(text)
	}

}

func TestGeneratedDevTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exercise requires POSIX pseudo-terminal")
	}
	db := os.Getenv("PONDER_TEST_POSTGRES")
	if db == "" {
		t.Skip("requires task PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	root := generatedGoProject(t, ctx)
	t.Setenv("PONDER_LOCAL_DATABASE_URL", db)
	t.Setenv("PONDER_LOCAL_RPC_URL", testanvil.Start(t, "--no-mining"))
	t.Setenv("PONDER_LOCAL_CONTRACT", "0x00000000000000000000000000000000000000ab")
	binary := filepath.Join(t.TempDir(), "ponder")
	build := []string{"build", "-o", binary}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, opt := range info.Settings {
			if opt.Key == "-race" && opt.Value == "true" {
				build = append(build, "-race")
			}
		}
	}
	if out, err := exec.CommandContext(ctx, "go", append(build, ".")...).CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	// The watcher appends a revision hash to this test-owned prefix.
	prefix := fmt.Sprintf("ponder_go_ui_%x", time.Now().UnixNano()&0xffffffffff)
	backend, err := store.OpenPostgres(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	defer func() {
		rows, err := backend.Query(ctx, "SELECT nspname FROM pg_namespace WHERE starts_with(nspname, $1)", prefix+"_")
		if err != nil {
			t.Error(err)
			return
		}
		for _, row := range rows.Rows {
			name := fmt.Sprint(row["nspname"])
			if err := backend.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema.Quote(name)+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
	}()
	cmd := exec.CommandContext(ctx, "python3", "testdata/dev_terminal.py", binary, root, prefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal session: %v: %s", err, out)
	}
}
