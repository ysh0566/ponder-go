package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

// Test-only transport decoration faults after the real DELETE or COMMIT. It
// does not alter production SQL or add a production bypass/failure hook.
type sealingFaultBackend struct {
	Backend
	stage string
	exit  bool
}
type sealingFaultTx struct {
	Transaction
	fault *sealingFaultBackend
}

var errSealing = errors.New("injected initial-safe persistence failure")

func (b *sealingFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &sealingFaultTx{tx, b}, nil
}
func (tx *sealingFaultTx) trip() error {
	if tx.fault.exit {
		os.Exit(74)
	}
	return errSealing
}
func (tx *sealingFaultTx) Exec(ctx context.Context, sql string, args ...any) error {
	if e := tx.Transaction.Exec(ctx, sql, args...); e != nil {
		return e
	}
	if tx.fault.stage == "before_commit" && strings.HasPrefix(sql, "DELETE FROM ") && strings.Contains(sql, `."_ponder_go_journal"`) {
		return tx.trip()
	}
	return nil
}
func (tx *sealingFaultTx) Commit(ctx context.Context) error {
	if e := tx.Transaction.Commit(ctx); e != nil {
		return e
	}
	if tx.fault.stage == "after_commit" {
		return tx.trip()
	}
	return nil
}
func seedHistoricalSealing(t *testing.T, a *Actions) {
	t.Helper()
	ctx := context.Background()
	if e := a.HistoricalBatch(ctx, 1, func(w *Writer) error {
		return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert)
	}); e != nil {
		t.Fatal(e)
	}
	if e := a.CommitBlock(ctx, "account", actionCheckpoint(9)); e != nil {
		t.Fatal(e)
	}
}
func checkHistoricalSealing(t *testing.T, a *Actions, sealed bool) {
	t.Helper()
	ctx := context.Background()
	states, e := a.Checkpoints(ctx)
	want := 0
	if sealed {
		want = 1
	}
	if e != nil || len(states) != want {
		t.Fatalf("initial-safe metadata: %#v %v", states, e)
	}
	journal, e := a.Journal(ctx, "account")
	if e != nil || len(journal) != 1-want {
		t.Fatalf("initial-safe undo prefix: %#v %v", journal, e)
	}
	rows, e := a.Select(ctx, "account")
	if e != nil || len(rows) != 1 || fmt.Sprint(rows[0]["balance"]) != "10" {
		t.Fatalf("historical data changed: %#v %v", rows, e)
	}
}
func TestInitialSafeFailureAtomicity(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			fault := &sealingFaultBackend{Backend: sourceBackend(t, ctx, dialect)}
			a, e := NewActions(ctx, fault, sourceActionsModel(), fmt.Sprintf("ponder_go_seal_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			seedHistoricalSealing(t, a)
			fault.stage = "before_commit"
			if e = a.InitializeCheckpoints(ctx, []CheckpointState{initialState(1, 10, "mainnet")}); !errors.Is(e, errSealing) {
				t.Fatal("expected persistence failure", e)
			}
			fault.stage = ""
			checkHistoricalSealing(t, a, false)
			if e = a.InitializeCheckpoints(ctx, []CheckpointState{initialState(1, 10, "mainnet")}); e != nil {
				t.Fatal(e)
			}
			checkHistoricalSealing(t, a, true)
			if e = a.CrashRecovery(ctx); e != nil {
				t.Fatal(e)
			}
			checkHistoricalSealing(t, a, true)
		})
	}
}
func TestInitialSafeProcessCrash(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("PONDER_SEAL_WORKER") == "1" {
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
		fault := &sealingFaultBackend{Backend: b, exit: true}
		a, e := NewActions(ctx, fault, sourceActionsModel(), os.Getenv("PONDER_CRASH_SCHEMA"), schema.Multichain)
		if e != nil {
			t.Fatal(e)
		}
		fault.stage = os.Getenv("PONDER_CRASH_STAGE")
		if e = a.InitializeCheckpoints(ctx, []CheckpointState{initialState(1, 10, "mainnet")}); e != nil {
			t.Fatal(e)
		}
		t.Fatal("process did not terminate")
	}
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, stage := range []string{"before_commit", "after_commit"} {
			t.Run(dialect+"/"+stage, func(t *testing.T) {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if dialect == "postgres" && url == "" {
					t.Skip("explicit task PostgreSQL required; no parity credit")
				}
				dir := filepath.Join(t.TempDir(), "db")
				ns := fmt.Sprintf("ponder_go_seal_crash_%x", time.Now().UnixNano())
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
				a, e := NewActions(ctx, b, sourceActionsModel(), ns, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				seedHistoricalSealing(t, a)
				if e = b.Close(); e != nil {
					t.Fatal(e)
				}
				deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestInitialSafeProcessCrash$", "-test.count=1")
				for _, env := range os.Environ() {
					if !strings.HasPrefix(env, "PONDER_TEST_POSTGRES=") {
						cmd.Env = append(cmd.Env, env)
					}
				}
				if dialect == "postgres" {
					cmd.Env = append(cmd.Env, "PONDER_TEST_POSTGRES="+url)
				}
				cmd.Env = append(cmd.Env, "PONDER_SEAL_WORKER=1", "PONDER_CRASH_DIR="+dir, "PONDER_CRASH_SCHEMA="+ns, "PONDER_CRASH_STAGE="+stage)
				output, e := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 74 {
					t.Fatalf("%s: %v\n%s", stage, e, output)
				}
				b = open()
				defer b.Close()
				a, e = NewActions(ctx, b, sourceActionsModel(), ns, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				checkHistoricalSealing(t, a, stage == "after_commit")
				if stage == "before_commit" {
					if e = a.InitializeCheckpoints(ctx, []CheckpointState{initialState(1, 10, "mainnet")}); e != nil {
						t.Fatal(e)
					}
				}
				if e = a.CrashRecovery(ctx); e != nil {
					t.Fatal(e)
				}
				checkHistoricalSealing(t, a, true)
			})
		}
	}
}
