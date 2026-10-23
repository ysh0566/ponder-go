package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/interval"
	"ponder.local/ponder-go/schema"
	"strings"
	"testing"
	"time"
)

func TestReorgCrashRecovery(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("PONDER_REORG_CRASH_WORKER") == "1" {
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
		s, e := New(ctx, b, model(false), os.Getenv("PONDER_CRASH_SCHEMA"), schema.Multichain)
		if e != nil {
			t.Fatal(e)
		}
		s.Backend = &crashBackend{Backend: b, stage: os.Getenv("PONDER_CRASH_STAGE")}
		if e = s.RevertAndRewind(ctx, event("", 1, 1).Checkpoint); e != nil {
			t.Fatal(e)
		}
		t.Fatal("crash worker returned")
	}
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, stage := range []string{"after_undo", "after_sync_rewind", "after_commit"} {
			t.Run(dialect+"/"+stage, func(t *testing.T) {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if dialect == "postgres" && url == "" {
					t.Skip("explicit task database required; no parity credit")
				}
				dir := filepath.Join(t.TempDir(), "db")
				namespace := fmt.Sprintf("ponder_go_reorg_crash_%x", time.Now().UnixNano())
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
				s, e := New(ctx, b, model(false), namespace, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				apply(t, s, "kept", 1, 1, Row{"id": "account", "balance": "7"}, Insert)
				apply(t, s, "orphan", 1, 2, Row{"id": "account", "balance": "9"}, Update)
				for _, n := range []uint64{1, 2} {
					if e = s.CompleteBlock(ctx, SyncBlock{ChainID: 1, Number: n, Hash: fmt.Sprint(n), Payload: []byte(`{}`)}, "reorg-crash"); e != nil {
						t.Fatal(e)
					}
					if e = s.RecordFactoryChildren(ctx, "factory", 1, n, fmt.Sprint(n), []FactoryChild{{Address: fmt.Sprintf("0x%040x", n), LogIndex: 0}}); e != nil {
						t.Fatal(e)
					}
				}
				if e = b.Close(); e != nil {
					t.Fatal(e)
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestReorgCrashRecovery$")
				env := []string{}
				for _, v := range os.Environ() {
					if !strings.HasPrefix(v, "PONDER_TEST_POSTGRES=") {
						env = append(env, v)
					}
				}
				if dialect == "postgres" {
					env = append(env, "PONDER_TEST_POSTGRES="+url)
				}
				cmd.Env = append(env, "PONDER_REORG_CRASH_WORKER=1", "PONDER_CRASH_DIR="+dir, "PONDER_CRASH_SCHEMA="+namespace, "PONDER_CRASH_STAGE="+stage)
				out, e := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 73 {
					t.Fatalf("worker %v: %s", e, out)
				}
				b = open()
				defer b.Close()
				s, e = New(ctx, b, model(false), namespace, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				committed := stage == "after_commit"
				wantBalance, wantBlock, wantRows := "9", uint64(2), 2
				if committed {
					wantBalance, wantBlock, wantRows = "7", 1, 1
				}
				if got := balance(t, s, "account"); got != wantBalance {
					t.Fatalf("business state %s; want %s", got, wantBalance)
				}
				progress, e := s.Progress(ctx)
				if e != nil || progress["1"] != checkpoint.MustEncode(event("", 1, wantBlock).Checkpoint) {
					t.Fatalf("progress %v %v", progress, e)
				}
				block, e := s.SyncedBlock(ctx, 1, 2)
				if e != nil || (block == nil) != committed {
					t.Fatalf("sync block %v %v", block, e)
				}
				missing, e := s.Missing(ctx, 1, "reorg-crash", interval.Interval{1, 2})
				if e != nil || len(missing) != map[bool]int{true: 1, false: 0}[committed] {
					t.Fatalf("sync intervals %v %v", missing, e)
				}
				if committed && missing[0] != (interval.Interval{2, 2}) {
					t.Fatalf("wrong rewind gap %v", missing)
				}
				children, e := s.FactoryChildren(ctx, "factory", 1, 2)
				if e != nil || len(children) != wantRows {
					t.Fatalf("factory state %v %v", children, e)
				}
				if children[fmt.Sprintf("0x%040x", 1)] != 1 {
					t.Fatal("canonical child lost", children)
				}
				for _, table := range []string{"_ponder_go_journal", "_ponder_go_events"} {
					r, e := b.Query(ctx, "SELECT * FROM "+s.internal(table))
					if e != nil || len(r.Rows) != wantRows {
						t.Fatalf("%s %v %v", table, r, e)
					}
				}
			})
		}
	}
}
