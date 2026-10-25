package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Real process exits supplement, rather than replace, the eight source cases.
func TestSyncMigrationProcessCrash(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("PONDER_SYNC_MIGRATION_WORKER") == "1" {
		var b Backend
		var e error
		if url := os.Getenv("PONDER_TEST_POSTGRES"); url != "" {
			b, e = OpenPostgres(ctx, url)
		} else {
			b, e = OpenPGlite(ctx, os.Getenv("PONDER_CRASH_DIR"))
		}
		if e != nil {
			t.Fatal(e)
		}
		m, e := NewSyncMigrator(&syncFaultBackend{Backend: b, stage: os.Getenv("PONDER_CRASH_STAGE"), terminate: func() { os.Exit(73) }}, os.Getenv("PONDER_CRASH_SCHEMA"))
		if e != nil {
			t.Fatal(e)
		}
		if e = m.Migrate(ctx); e != nil {
			t.Fatal(e)
		}
		t.Fatal("sync migration worker did not terminate")
	}
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, stage := range []string{"during_copy", "after_commit"} {
			t.Run(dialect+"/"+stage, func(t *testing.T) {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if dialect == "postgres" && url == "" {
					t.Skip("explicit task PostgreSQL required; no parity credit")
				}
				dir := filepath.Join(t.TempDir(), "db")
				scope := fmt.Sprintf("ponder_go_sync_crash_%x", time.Now().UnixNano())
				open := func() Backend {
					t.Helper()
					var b Backend
					var e error
					if dialect == "postgres" {
						b, e = OpenPostgres(ctx, url)
					} else {
						b, e = OpenPGlite(ctx, dir)
					}
					if e != nil {
						t.Fatal(e)
					}
					return b
				}
				b := open()
				m, e := NewSyncMigrator(b, scope)
				if e != nil {
					t.Fatal(e)
				}
				seedSyncCopyRows(t, m)
				before := syncRowsSnapshot(t, m, false)
				history := sourceSyncSQL(t, m, `SELECT * FROM ponder_sync.kysely_migration ORDER BY name`)
				if e = b.Close(); e != nil {
					t.Fatal(e)
				}
				deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestSyncMigrationProcessCrash$", "-test.count=1")
				for _, env := range os.Environ() {
					if !strings.HasPrefix(env, "PONDER_TEST_POSTGRES=") {
						cmd.Env = append(cmd.Env, env)
					}
				}
				if dialect == "postgres" {
					cmd.Env = append(cmd.Env, "PONDER_TEST_POSTGRES="+url)
				}
				cmd.Env = append(cmd.Env, "PONDER_SYNC_MIGRATION_WORKER=1", "PONDER_CRASH_DIR="+dir, "PONDER_CRASH_SCHEMA="+scope, "PONDER_CRASH_STAGE="+stage)
				output, e := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 73 {
					t.Fatalf("sync migration exit at %s: %v\n%s", stage, e, output)
				}
				b = open()
				defer b.Close()
				m, e = NewSyncMigrator(b, scope)
				if e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(before, syncRowsSnapshot(t, m, false)) {
					t.Fatal("crash changed legacy source rows")
				}
				expectSourceRows(t, sourceSyncSQL(t, m, `SELECT * FROM ponder_sync.kysely_migration ORDER BY name`), history.Rows)
				if stage == "during_copy" {
					r, e := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", m.LatestSchema())
					if e != nil || len(r.Rows) != 0 {
						t.Fatalf("uncommitted partial cache survived: %#v %v", r, e)
					}
				} else {
					assertSyncCopied(t, m, before)
				}
				if e = m.Migrate(ctx); e != nil {
					t.Fatal(e)
				}
				assertSyncCopied(t, m, before)
			})
		}
	}
}
