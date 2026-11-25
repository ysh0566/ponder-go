package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/internal/devstatus"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type devReadiness struct{ unavailable atomic.Bool }

func (r *devReadiness) Ready(context.Context) (bool, error) {
	if r.unavailable.Load() {
		return false, errors.New("readiness source unavailable")
	}
	return true, nil
}

func TestApplicationDevCheckpoints(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "status")
			t.Setenv("PONDER_COMMAND", "dev")
			t.Setenv(devstatus.FileEnv, path)
			b := stagedBackend(t, ctx, dialect, "memory://")
			defer b.Close()
			ns := fmt.Sprintf("ponder_go_dev_progress_%d", time.Now().UnixNano())
			db, err := store.New(ctx, b, schema.Schema{}, ns, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Exec(context.Background(), "DROP SCHEMA "+schema.Quote(ns)+", "+schema.Quote(db.SyncNamespace)+" CASCADE")
			routes := query.New(db)
			routes.ChainNames[7] = "test"
			readiness := new(devReadiness)
			routes.Readiness = readiness
			app, err := LaunchApplication(ctx, nil, routes, "127.0.0.1", 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				if err := app.Wait(); err != nil {
					t.Error(err)
				}
			}()
			await := func(want map[string]uint64, unavailable bool) {
				t.Helper()
				for ctx.Err() == nil {
					data, err := os.ReadFile(path)
					var report devstatus.Snapshot
					if err == nil && json.Unmarshal(data, &report) == nil && report.Ready != unavailable && report.ReadinessUnavailable == unavailable &&
						report.Address == app.Addr().String() && report.Blocks != nil &&
						len(report.Blocks) == len(want) && report.Blocks["test"] == want["test"] {
						return
					}
					time.Sleep(30 * time.Millisecond)
				}
				t.Fatalf("no committed checkpoint report for %v", want)
			}
			await(map[string]uint64{}, false)
			ev := store.Event{ID: "first", Name: "Head:block", Checkpoint: checkpoint.Block(10, 7, 5, true)}
			if err := db.Apply(ctx, ev, func(*store.Writer) error { return nil }); err != nil {
				t.Fatal(err)
			}
			await(map[string]uint64{"test": 5}, false)
			readiness.unavailable.Store(true)
			await(map[string]uint64{"test": 5}, true)
		})
	}
}
