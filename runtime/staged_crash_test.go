package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/store"
)

func TestStagedRuntimeProcessCrash(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("PONDER_STAGED_WORKER") == "1" {
		dialect := os.Getenv("PONDER_STAGED_DIALECT")
		base := stagedBackend(t, ctx, dialect, os.Getenv("PONDER_STAGED_DIR"))
		fault := &runtimeFaultBackend{Backend: base, terminate: func() { os.Exit(76) }}
		stage, e := strconv.Atoi(os.Getenv("PONDER_STAGED_POINT"))
		if e != nil {
			t.Fatal(e)
		}
		fault.stage.Store(int32(stage))
		r, _ := stagedEngine(t, fault, os.Getenv("PONDER_STAGED_SCHEMA"), os.Getenv("PONDER_STAGED_RPC"), stagedBlockHandlers(nil))
		if e = r.Sync(ctx); e != nil {
			t.Fatal(e)
		}
		t.Fatal("worker did not terminate at persistence boundary")
	}
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, point := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/stage_%d", dialect, point), func(t *testing.T) {
				rpc, url := localChain(t)
				dir := filepath.Join(t.TempDir(), "db")
				base := stagedBackend(t, ctx, dialect, dir)
				ns := fmt.Sprintf("ponder_go_runtime_crash_%x", time.Now().UnixNano())
				r, d := stagedEngine(t, base, ns, url, stagedBlockHandlers(nil))
				if e := r.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				stageRows(t, r.Store, 1)
				if e := d.Close(); e != nil {
					t.Fatal(e)
				}
				if e := base.Close(); e != nil {
					t.Fatal(e)
				}
				var ignored any
				if e := rpc.Call(ctx, "evm_mine", []any{}, &ignored); e != nil {
					t.Fatal(e)
				}
				deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestStagedRuntimeProcessCrash$", "-test.count=1")
				for _, v := range os.Environ() {
					if dialect == "pglite" && strings.HasPrefix(v, "PONDER_TEST_POSTGRES=") {
						continue
					}
					cmd.Env = append(cmd.Env, v)
				}
				cmd.Env = append(cmd.Env, "PONDER_STAGED_WORKER=1", "PONDER_STAGED_DIALECT="+dialect, "PONDER_STAGED_DIR="+dir, "PONDER_STAGED_SCHEMA="+ns, "PONDER_STAGED_RPC="+url, fmt.Sprintf("PONDER_STAGED_POINT=%d", point))
				out, e := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 76 {
					t.Fatalf("process boundary: %v\n%s", e, out)
				}
				base = stagedBackend(t, ctx, dialect, dir)
				defer base.Close()
				r, d = stagedEngine(t, base, ns, url, stagedBlockHandlers(nil))
				defer d.Close()
				// Before lifecycle recovery, only a completed transaction may be visible.
				want := 1
				if point == 3 {
					want = 2
				}
				stageRows(t, r.Store, want)
				if e = r.Sync(ctx); e != nil {
					t.Fatal("automatic lifecycle recovery/replay", e)
				}
				stageRows(t, r.Store, 2)
				stageLatest(t, d, 1)
				rows, e := r.Store.Read(ctx, "account")
				if e != nil {
					t.Fatal(e)
				}
				for _, row := range rows.Rows {
					if row["id"] == "1" && fmt.Sprint(row["balance"]) != "2" {
						t.Fatal("recovery applied block twice", rows)
					}
				}
				if e = r.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				stageRows(t, r.Store, 2)
			})
		}
	}
}

func TestStagedSetupRollback(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, url := localChain(t)
			b := stagedBackend(t, ctx, dialect, "memory://")
			defer b.Close()
			r, d := stagedEngine(t, b, fmt.Sprintf("ponder_go_setup_%x", time.Now().UnixNano()), url, stagedBlockHandlers(nil))
			defer d.Close()
			// The setup callback runs before any block dispatch, and its business
			// writes and initial checkpoint metadata must commit or roll back together.
			calls := 0
			fail := true
			r.Config.Contracts = append(r.Config.Contracts, config.Contract{Name: "Token", Chain: "local", ABI: []byte(transferABI), Address: []string{"0x1000000000000000000000000000000000000001"}})
			r.Handlers["Token:setup"] = func(ctx context.Context, c *Context) error {
				calls++
				if c.Event.SetupBlock != 0 || c.Event.Block.Number != "0x0" {
					return fmt.Errorf("setup input changed")
				}
				if e := c.DB.Write(ctx, "account", store.Row{"id": "setup", "balance": "99"}, store.Insert); e != nil {
					return e
				}
				if fail {
					return errRuntimePersist
				}
				return nil
			}
			// Use a new validated engine for the changed configuration before migration.
			var e error
			r, e = NewStaged(r.Config, d, r.Handlers, StagedOptions{BuildID: "staged-boundaries"})
			if e != nil {
				t.Fatal(e)
			}
			r.Client.RequestsPerSecond = 0
			if e = r.Sync(ctx); !errors.Is(e, errRuntimePersist) {
				t.Fatal(e)
			}
			stageRows(t, r.Store, 0)
			states, e := d.Actions().Checkpoints(ctx)
			if e != nil || len(states) != 0 {
				t.Fatal("failed setup published checkpoint metadata", states, e)
			}
			fail = false
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			stageRows(t, r.Store, 2)
			if calls != 2 {
				t.Fatal("setup retry count", calls)
			}
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			if calls != 2 {
				t.Fatal("committed setup replayed", calls)
			}
		})
	}
}
