package historical

import (
	"context"
	"errors"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
	"os"
	"reflect"
	"strings"
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
			registry := metrics.New()
			ids := map[string]bool{}
			visits := map[store.ManagedLiveHead]int{}
			o.OnCommitted = func(_ context.Context, group RuntimeCommit) error {
				if group.OperationID == "" || ids[group.OperationID] || group.Head.Hash == "" || group.Head.Checkpoint != group.Checkpoint || group.Safe || group.Historical || group.Events != 1 || !group.Applied {
					return fmt.Errorf("unexpected continuous commit: %+v", group)
				}
				cp, err := checkpoint.Decode(group.Head.Checkpoint)
				if err != nil {
					return err
				}
				live, complete := 1, -1
				if cp.BlockNumber == 4 {
					live, complete = 0, 1
				}
				name := fmt.Sprint(b.Executable().Chains[group.ChainID-1]["name"])
				assertSyncMetrics(t, registry, name, group.Head, live, complete)
				at, err := checkpoint.Decode(group.Head.Checkpoint)
				if err != nil {
					return err
				}
				assertIndexingTime(t, registry, name, at.BlockTimestamp)
				if !group.Historical {
					for _, chain := range b.Executable().Chains {
						assertBackfillTimes(t, registry, fmt.Sprint(chain["name"]), true, true)
					}
				}
				ids[group.OperationID] = true
				visits[group.Head]++
				return nil
			}
			r = runtimeOpen(t, backend, o)
			measurement := metrics.WithHistorical(metrics.WithRealtime(t.Context(), registry), registry)
			measurement = metrics.WithChainSync(measurement, registry)
			measurement = metrics.WithHandlers(measurement, registry)
			out, err := r.Run(measurement)
			if err != nil || !out.Completed || !out.Ready || out.LiveBlocks != 8 || out.HistoricalBlocks != 0 || commits != 8 || !advanced {
				t.Fatalf("actual continuous runtime result=%+v commits=%d advanced=%v err=%v", out, commits, advanced, err)
			}
			checkAcceptedHeads := func(count, executions int) {
				t.Helper()
				body, err := registry.Collect(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, chain := range []string{"one", "two"} {
					want := fmt.Sprintf("ponder_indexing_function_duration_count{chain=%q,event=%q} %d\n", chain, "Blocks:block", executions)
					if !strings.Contains(body, want) {
						t.Fatalf("missing %q in metrics:\n%s", want, body)
					}
					for _, name := range []string{"ponder_realtime_latency", "ponder_realtime_block_arrival_latency"} {
						want := fmt.Sprintf("%s_count{chain=%q} %d\n", name, chain, count)
						if !strings.Contains(body, want) {
							t.Fatalf("missing %q in metrics:\n%s", want, body)
						}
					}
				}
			}
			checkAcceptedHeads(2, 4) // Only the polled heads 2 and 4, excluding gap blocks 1 and 3.
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
			out, err = r.Run(measurement)
			if err != nil || !out.Completed || !out.Ready || !out.Recovered {
				t.Fatalf("bounded end did not replay/reactivate: %+v %v", out, err)
			}
			checkAcceptedHeads(3, 8) // Cold replay polls head 4 once more.
			if len(visits) != 8 || len(ids) != 16 {
				t.Fatalf("continuous replay: heads=%d operations=%d", len(visits), len(ids))
			}
			for head, count := range visits {
				if count != 2 {
					t.Fatalf("head %+v observed %d times", head, count)
				}
			}
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			// Each opening has a historical window, even with zero historical
			// events. Replaying live blocks must not create historical work samples.
			for _, family := range families {
				name := family.GetName()
				if strings.HasPrefix(name, "ponder_historical_") && name != "ponder_historical_start_timestamp_seconds" && name != "ponder_historical_end_timestamp_seconds" {
					t.Fatalf("live replay published historical work in %s", name)
				}
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

func TestRuntimeFailedHeadLatency(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			state := &runtimeRPCState{latest: map[uint64]uint64{1: 1, 2: 1}}
			p := controlledRuntimeRPC(t, state)
			stop := errors.New("reject live head in handler")
			b := runtimeBuild(t, p, 1, 1, func(ctx context.Context, a *indexing.HandlerArgs) error {
				if err := finiteIncrement(ctx, a); err != nil {
					return err
				}
				return stop
			})
			for _, chain := range b.Executable().Chains {
				chain["reorgWindow"] = uint64(100)
			}
			f := newManagedFiniteFixture(t, backend, b)
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "rejected-live-head", OpenID: "first", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
			r := runtimeOpen(t, backend, o)
			defer func() {
				if err := r.Close(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			registry := metrics.New()
			ctx := metrics.WithChainSync(metrics.WithHandlers(metrics.WithRealtime(t.Context(), registry), registry), registry)
			if _, err := r.Run(ctx); err == nil || !strings.Contains(err.Error(), stop.Error()) {
				t.Fatalf("failed live callback: %v", err)
			}
			body, err := registry.Collect(ctx)
			if err != nil || strings.Contains(body, "ponder_realtime_") || strings.Contains(body, "ponder_indexing_function_duration") {
				t.Fatalf("uncommitted head metrics=%q err=%v", body, err)
			}
			for id, name := range []string{"one", "two"} {
				ts, err := uintQuantity(finiteBlock(uint64(id+1), 0)["timestamp"])
				if err != nil {
					t.Fatal(err)
				}
				assertIndexingTime(t, registry, name, ts)
			}
		})
	}
}
