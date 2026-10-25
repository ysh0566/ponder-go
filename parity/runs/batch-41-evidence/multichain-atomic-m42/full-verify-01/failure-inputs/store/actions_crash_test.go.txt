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

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

// Real process exits distinguish a pending durable journal from an aborted
// transaction and from an assigned/finalized checkpoint. Supplemental tests.
func TestStagedActionCrashRecovery(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("PONDER_ACTION_CRASH_WORKER") == "1" {
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
		a, e := NewActions(ctx, b, sourceActionsModel(), os.Getenv("PONDER_CRASH_SCHEMA"), schema.Multichain)
		if e != nil {
			t.Fatal(e)
		}
		stage := os.Getenv("PONDER_CRASH_STAGE")
		block := uint64(1)
		if stage == "during_recovery" {
			block = 2
		}
		e = a.RawBatch(ctx, 1, func(w *Writer) error {
			if stage == "during_recovery" {
				if e := w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "9"}, Update); e != nil {
					return e
				}
			} else {
				if e := w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert); e != nil {
					return e
				}
			}
			if stage == "before_raw_commit" {
				os.Exit(73)
			}
			if stage == "assigned_before_raw_commit" {
				if e := w.CommitBlock(ctx, "account", actionCheckpoint(block)); e != nil {
					return e
				}
				if e := w.AdvanceLatest(ctx, actionCheckpoint(block)); e != nil {
					return e
				}
				os.Exit(73)
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if stage == "after_raw_commit" {
			os.Exit(73)
		}
		if e = a.CommitBlock(ctx, "account", actionCheckpoint(block)); e != nil {
			t.Fatal(e)
		}
		if stage == "after_block_commit" {
			os.Exit(73)
		}
		if e = a.AdvanceLatest(ctx, actionCheckpoint(block)); e != nil {
			t.Fatal(e)
		}
		if stage == "after_latest" {
			os.Exit(73)
		}
		if stage == "during_recovery" {
			a.s.Backend = &crashBackend{Backend: b, stage: "after_undo"}
			if e = a.CrashRecovery(ctx); e != nil {
				t.Fatal(e)
			}
			t.Fatal("recovery did not terminate")
		}
		if e = a.Finalize(ctx, actionCheckpoint(block)); e != nil {
			t.Fatal(e)
		}
		os.Exit(73)
	}
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, stage := range []string{"before_raw_commit", "assigned_before_raw_commit", "after_raw_commit", "after_block_commit", "after_latest", "after_finalize", "during_recovery"} {
			t.Run(dialect+"/"+stage, func(t *testing.T) {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if dialect == "postgres" && url == "" {
					t.Skip("explicit task database required; no parity credit")
				}
				dir := filepath.Join(t.TempDir(), "db")
				namespace := fmt.Sprintf("ponder_go_action_crash_%x", time.Now().UnixNano())
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
				a, e := NewActions(ctx, b, sourceActionsModel(), namespace, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				zero := checkpoint.MustEncode(actionCheckpoint(0))
				if e = a.InitializeCheckpoints(ctx, []CheckpointState{{ChainName: "mainnet", ChainID: 1, SafeCheckpoint: zero, FinalizedCheckpoint: zero, LatestCheckpoint: zero}}); e != nil {
					t.Fatal(e)
				}
				if e = a.CreateTriggers(ctx); e != nil {
					t.Fatal(e)
				}
				if stage == "during_recovery" {
					if e = a.RawBatch(ctx, 1, func(w *Writer) error {
						return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "7"}, Insert)
					}); e != nil {
						t.Fatal(e)
					}
					if e = a.CommitBlock(ctx, "account", actionCheckpoint(1)); e != nil {
						t.Fatal(e)
					}
					if e = a.AdvanceLatest(ctx, actionCheckpoint(1)); e != nil {
						t.Fatal(e)
					}
					if e = a.Finalize(ctx, actionCheckpoint(1)); e != nil {
						t.Fatal(e)
					}
				}
				if e = b.Close(); e != nil {
					t.Fatal(e)
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestStagedActionCrashRecovery$")
				env := []string{}
				for _, item := range os.Environ() {
					if !strings.HasPrefix(item, "PONDER_TEST_POSTGRES=") {
						env = append(env, item)
					}
				}
				if dialect == "postgres" {
					env = append(env, "PONDER_TEST_POSTGRES="+url)
				}
				cmd.Env = append(env, "PONDER_ACTION_CRASH_WORKER=1", "PONDER_CRASH_DIR="+dir, "PONDER_CRASH_SCHEMA="+namespace, "PONDER_CRASH_STAGE="+stage)
				output, e := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 73 {
					t.Fatalf("worker %v: %s", e, output)
				}
				b = open()
				defer b.Close()
				a, e = NewActions(ctx, b, sourceActionsModel(), namespace, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				rows, e := a.Select(ctx, "account")
				if e != nil {
					t.Fatal(e)
				}
				journal, e := a.Journal(ctx, "account")
				if e != nil {
					t.Fatal(e)
				}
				states, e := a.Checkpoints(ctx)
				if e != nil {
					t.Fatal(e)
				}
				aborted := stage == "before_raw_commit" || stage == "assigned_before_raw_commit"
				if aborted {
					if len(rows) != 0 || len(journal) != 0 || states[0].LatestCheckpoint != zero {
						t.Fatalf("aborted staged write persisted: %v %v %v", rows, journal, states)
					}
				} else {
					if len(rows) != 1 {
						t.Fatal(rows)
					}
					value := int64(10)
					if stage == "during_recovery" {
						value = 9
					}
					expectSourceBalance(t, rows[0], value)
					wantJournal := 1
					if stage == "after_finalize" {
						wantJournal = 0
					}
					if len(journal) != wantJournal {
						t.Fatal("journal durability", journal)
					}
					if stage == "after_raw_commit" && journal[0]["checkpoint"] != checkpoint.MaxString {
						t.Fatal("lost MAX sentinel", journal)
					}
					if stage == "after_block_commit" && journal[0]["checkpoint"] != checkpoint.MustEncode(actionCheckpoint(1)) {
						t.Fatal("block assignment missing", journal)
					}
				}
				if e = a.CrashRecovery(ctx); e != nil {
					t.Fatal(e)
				}
				rows, e = a.Select(ctx, "account")
				if e != nil {
					t.Fatal(e)
				}
				journal, e = a.Journal(ctx, "account")
				if e != nil || len(journal) != 0 {
					t.Fatalf("journal recovery %v %v", journal, e)
				}
				if stage == "during_recovery" {
					if len(rows) != 1 {
						t.Fatal(rows)
					}
					expectSourceBalance(t, rows[0], 7)
				} else if stage == "after_finalize" {
					if len(rows) != 1 {
						t.Fatal(rows)
					}
					expectSourceBalance(t, rows[0], 10)
				} else if len(rows) != 0 {
					t.Fatal("unfinalized rows survived", rows)
				}
				unchanged, e := a.Checkpoints(ctx)
				if e != nil || unchanged[0] != states[0] {
					t.Fatalf("crashRecovery primitive changed metadata: %v %v", unchanged, e)
				}
				safe, e := checkpoint.Decode(states[0].SafeCheckpoint)
				if e != nil {
					t.Fatal(e)
				}
				if e = a.AdvanceLatest(ctx, safe); e != nil {
					t.Fatal(e)
				}
				reset, e := a.Checkpoints(ctx)
				if e != nil || reset[0].LatestCheckpoint != states[0].SafeCheckpoint {
					t.Fatalf("runtime latest reset %v %v", reset, e)
				}
			})
		}
	}
}
