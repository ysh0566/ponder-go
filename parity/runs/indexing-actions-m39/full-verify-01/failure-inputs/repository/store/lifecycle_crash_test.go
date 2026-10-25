package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

func TestLifecycleProcessCrash(t *testing.T) {
	ctx := context.Background()
	options := LifecycleOptions{HeartbeatInterval: 25 * time.Millisecond, HeartbeatTimeout: 250 * time.Millisecond}
	if os.Getenv("PONDER_LIFECYCLE_CRASH_WORKER") == "1" {
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
		model := sourceActionsModel()
		model.Tables[0].Indexes = []schema.Index{{Name: "account_balance_index", Columns: []string{"balance"}}}
		fault := &migrationFaultBackend{Backend: b, stage: os.Getenv("PONDER_CRASH_STAGE"), terminate: func() { os.Exit(73) }}
		d, e := NewLifecycle(fault, model, os.Getenv("PONDER_CRASH_SCHEMA"), schema.Multichain, options)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = d.Migrate(ctx, lifecycleMigration()); e != nil {
			t.Fatal(e)
		}
		t.Fatal("migration crash injection did not terminate")
	}
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, stage := range []string{"first_migration_before_commit", "recovery_during_undo", "recovery_before_commit", "recovery_after_commit"} {
			t.Run(dialect+"/"+stage, func(t *testing.T) {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if dialect == "postgres" && url == "" {
					t.Skip("explicit task PostgreSQL required; no parity credit")
				}
				dir := filepath.Join(t.TempDir(), "db")
				ns := fmt.Sprintf("ponder_go_life_crash_%x", time.Now().UnixNano())
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
				d := newTestLifecycle(t, b, ns, options)
				first := stage == "first_migration_before_commit"
				var before lifecycleSnapshot
				if !first {
					seedLifecycleRecovery(t, d)
					before = snapshotLifecycle(t, d)
				}
				if e := b.Close(); e != nil {
					t.Fatal(e)
				}
				deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestLifecycleProcessCrash$", "-test.count=1")
				for _, env := range os.Environ() {
					if !strings.HasPrefix(env, "PONDER_TEST_POSTGRES=") {
						cmd.Env = append(cmd.Env, env)
					}
				}
				if dialect == "postgres" {
					cmd.Env = append(cmd.Env, "PONDER_TEST_POSTGRES="+url)
				}
				point := strings.TrimPrefix(stage, "recovery_")
				if first {
					point = "before_commit"
				}
				cmd.Env = append(cmd.Env, "PONDER_LIFECYCLE_CRASH_WORKER=1", "PONDER_CRASH_DIR="+dir, "PONDER_CRASH_SCHEMA="+ns, "PONDER_CRASH_STAGE="+point)
				output, e := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 73 {
					t.Fatalf("process stage %s: %v\n%s", stage, e, output)
				}
				b = open()
				defer b.Close()
				reopened := newTestLifecycle(t, b, ns, options)
				if first {
					r, e := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1 OR nspname=$2", ns, ns+"_sync")
					if e != nil || len(r.Rows) != 0 {
						t.Fatalf("aborted startup persisted schema: %#v %v", r, e)
					}
				} else {
					after := snapshotLifecycle(t, reopened)
					if stage != "recovery_after_commit" {
						if !reflect.DeepEqual(before, after) {
							t.Fatalf("process exit leaked uncommitted recovery: before %+v after %+v", before, after)
						}
					} else {
						if len(after.Data.Rows) != 1 || after.Data.Rows[0]["balance"] != "10" || len(after.Journal.Rows) != 0 || len(after.Indexes.Rows) != 1 || len(after.Triggers.Rows) != 2 {
							t.Fatalf("committed recovery state: %+v", after)
						}
						if !reflect.DeepEqual(before.Checkpoints, after.Checkpoints) {
							t.Fatal("migration altered latest checkpoint")
						}
						var app AppMetadata
						if e := json.Unmarshal([]byte(fmt.Sprint(after.Metadata.Rows[0]["value"])), &app); e != nil {
							t.Fatal(e)
						}
						if app.IsReady != 0 || app.IsLocked != 1 {
							t.Fatalf("committed metadata: %+v", app)
						}
					}
				}
				result, e := reopened.Migrate(ctx, lifecycleMigration())
				if e != nil {
					t.Fatal(e)
				}
				if !first && len(result) != 1 {
					t.Fatalf("recovery retry checkpoint: %#v", result)
				}
				rows, e := reopened.Actions().Select(ctx, "account")
				want := 1
				if first {
					want = 0
				}
				if e != nil || len(rows) != want {
					t.Fatalf("retry state: %#v %v", rows, e)
				}
				if !first && fmt.Sprint(rows[0]["balance"]) != "10" {
					t.Fatal("retry lost finalized baseline", rows)
				}
				if e := reopened.Close(); e != nil {
					t.Fatal(e)
				}
			})
		}
	}
}
