package historical

import (
	"context"
	"fmt"
	"os"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"reflect"
	"testing"
	"time"
)

// Supplemental actual RuntimeRunner/native database tests, no mapping credit.
func TestRuntimeContinuousFutureEnd(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained test root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			state := &runtimeRPCState{latest: map[uint64]uint64{1: 2, 2: 2}}
			p := controlledRuntimeRPC(t, state)
			b := runtimeBuild(t, p, 4, 1, indexing.Handler(finiteIncrement))
			for _, c := range b.Executable().Chains {
				c["reorgWindow"] = uint64(100)
			}
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			var r *RuntimeRunner
			advanced := false
			commits := 0
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-future-v1", OpenID: "continuous-future-1", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, RetryInterval: time.Millisecond, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
				if e.Type != "block" {
					return nil
				}
				commits++
				c, err := r.Control(ctx)
				if err != nil {
					return err
				}
				both := true
				for _, h := range c.Cursors {
					cp, err := checkpoint.Decode(h.Checkpoint)
					if err != nil {
						return err
					}
					both = both && cp.BlockNumber >= 2
				}
				if both && !advanced {
					state.mu.Lock()
					state.latest[1], state.latest[2] = 4, 4
					state.mu.Unlock()
					advanced = true
				}
				return nil
			}}}
			r = runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Completed || !out.Ready || out.LiveBlocks != 8 || out.HistoricalBlocks != 0 || commits != 8 || !advanced {
				t.Fatalf("actual continuous runtime result=%+v commits=%d advanced=%v err=%v", out, commits, advanced, err)
			}
			c, err := r.Control(context.Background())
			if err != nil || c.Version != "managed-live-continuous-v6" || c.Tail != 8 || c.SafeOrdinal != 0 {
				t.Fatalf("continuous control=%+v err=%v", c, err)
			}
			published, err := r.PublicCheckpoints(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "4" || fmt.Sprint(rows[1]["balance"]) != "4" {
					t.Fatalf("real future-head writes=%v", rows)
				}
			})
			o.OpenID = "continuous-future-reopen"
			o.Live.OnProgress = nil
			r = runtimeOpen(t, backend, o)
			if ready, err := r.Ready(context.Background()); err != nil || ready {
				t.Fatalf("unsafe bounded reopen exposed ready: %v %v", ready, err)
			}
			reopened, err := r.Control(context.Background())
			if err != nil || len(reopened.ReplayTargets) != 2 {
				t.Fatalf("unsafe bounded end lost replay guard: %+v %v", reopened, err)
			}
			for _, h := range reopened.Cursors {
				cp, err := checkpoint.Decode(h.Checkpoint)
				if err != nil || cp.BlockNumber != 0 {
					t.Fatalf("unsafe bounded end did not rewind private cursor: %+v %v", reopened, err)
				}
			}
			actual, err := r.PublicCheckpoints(context.Background())
			if err != nil || !reflect.DeepEqual(actual, published) {
				t.Fatalf("bounded reopen moved public projection: got=%v want=%v err=%v", actual, published, err)
			}
			out, err = r.Run(context.Background())
			if err != nil || !out.Completed || !out.Ready || !out.Recovered {
				t.Fatalf("bounded end did not replay/reactivate: %+v %v", out, err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "4" || fmt.Sprint(rows[1]["balance"]) != "4" {
					t.Fatalf("bounded replay duplicated business writes: %v", rows)
				}
			})

		})
	}
}
