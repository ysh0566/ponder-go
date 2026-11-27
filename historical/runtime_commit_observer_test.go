package historical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/store"
)

// Stop after a transaction that commits events for both chains. Each observer
// can read the durable checkpoint and both errors must reach the caller.
func TestRuntimeOmnichainCommitObserver(t *testing.T) {
	g := loadOmniLogsGolden(t)
	tc := omniLogsNamedCase(t, g, "small")
	want := tc.Snapshots[0]
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			var advanced atomic.Bool
			var refreshed [3]atomic.Int32
			p, _ := omniLogsRPCWithSelectors(t, g, nil, func(id uint64, selector string) string {
				if advanced.Load() && selector == "latest" {
					refreshed[id].Add(1)
				}
				return selector
			})
			var calls []string
			h := omniLogsHandlers(func() []omniLogsEvent { return want.Calls }, &calls, nil)
			f := newManagedFiniteFixture(t, backend, omniLogsBuild(t, g, p, h))
			omnichainGoldenStorage(&f)
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "commit-observer", Requesters: runtimeFactory(p, &epochs)}
			o.Historical.QuerySize = tc.QuerySize
			var ticks atomic.Int64
			o.Historical.Clock = func() time.Time {
				ticks.Add(1)
				if advanced.Load() {
					return time.Unix(102, 0)
				}
				return time.Unix(42, 0)
			}
			var r *RuntimeRunner
			var stopping bool
			stops := map[uint64]error{1: errors.New("stop on one"), 2: errors.New("stop on two")}
			last := map[uint64]string{}
			events := 0
			o.Historical.OnCommitted = func(ctx context.Context, group GroupResult) error {
				control, err := r.Control(ctx)
				if err != nil {
					return err
				}
				if err := r.Close(ctx); !errors.Is(err, store.ErrManagedLiveBusy) {
					return fmt.Errorf("recursive close = %v", err)
				}
				id := group.ChainID
				cp, err := checkpoint.Decode(group.Checkpoint)
				if err != nil || cp.ChainID != id || group.Checkpoint <= last[id] || control.LogicalSafe[id] != group.Checkpoint || control.LogicalCursors[id] != group.Checkpoint || !group.Applied || group.Reconciled || group.Attempts != 1 {
					return fmt.Errorf("group %+v differs from committed state %+v", group, control.LogicalSafe)
				}
				expectedEvents := 0
				chains := map[uint64]bool{}
				for _, event := range want.Calls[:len(calls)] {
					pos, err := checkpoint.Decode(event.Checkpoint)
					if err != nil {
						return err
					}
					if event.Checkpoint <= last[pos.ChainID] {
						continue
					}
					chains[pos.ChainID] = true
					if pos.ChainID == id && event.Checkpoint <= group.Checkpoint {
						if h := float64(pos.BlockNumber); h < group.BlockRange[0] || h > group.BlockRange[1] {
							return fmt.Errorf("event %s outside reported range %v", event.Key, group.BlockRange)
						}
						expectedEvents++
					}
				}
				if group.Events == 0 || group.Events != expectedEvents {
					return fmt.Errorf("group %+v covers %d reference events", group, expectedEvents)
				}
				if o.OpenID == "resume" {
					advanced.Store(true)
				}
				last[id] = group.Checkpoint
				events += group.Events
				if o.OpenID == "first" && (stopping || len(chains) == 2) {
					stopping = true
					return stops[id]
				}
				return nil
			}
			groupStop := errors.New("runtime observer stop")
			var stoppingID string
			notified := 0
			o.OnCommitted = func(_ context.Context, commit RuntimeCommit) error {
				if !commit.Historical || !commit.Safe || commit.Head.Hash == "" || commit.OperationID == "" || commit.Checkpoint != last[commit.ChainID] {
					return fmt.Errorf("omnichain group after commit: %+v", commit)
				}
				notified += commit.Events
				if o.OpenID == "first" && stopping {
					if stoppingID != "" && stoppingID != commit.OperationID {
						return fmt.Errorf("groups from distinct commits %s and %s", stoppingID, commit.OperationID)
					}
					stoppingID = commit.OperationID
					return groupStop
				}
				return nil
			}
			for _, owner := range []string{"first", "resume", "finished"} {
				o.OpenID = owner
				r = runtimeOpen(t, backend, o)
				registry := metrics.New()
				result, err := r.Run(metrics.WithHistorical(t.Context(), registry))
				if owner == "first" {
					if !errors.Is(err, groupStop) || !errors.Is(err, stops[1]) || !errors.Is(err, stops[2]) || result.Ready || result.Completed || events != len(calls) || events == len(want.Calls) || ticks.Load() <= 1 {
						t.Fatalf("expected both committed groups before stopping: calls=%d events=%d result=%+v err=%v", len(calls), events, result, err)
					}
				} else if err != nil || !result.Ready || !result.Completed || !result.Recovered || (owner == "finished" && result.HistoricalBlocks != 0) {
					t.Fatalf("%s: %+v, %v", owner, result, err)
				}
				if owner == "resume" && (refreshed[1].Load() != 1 || refreshed[2].Load() != 1) {
					t.Fatalf("clock advance produced head refresh counts %d and %d", refreshed[1].Load(), refreshed[2].Load())
				}
				for _, chain := range r.build.Chains {
					assertBackfillTimes(t, registry, fmt.Sprint(chain["name"]), true, owner != "first")
				}
				text, err := registry.Collect(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, step := range []string{
					`extract_duration{step="format"}`,
					`extract_duration{step="build"}`,
					`extract_duration{step="decode"}`,
					`transform_duration{step="prefetch"}`,
					`transform_duration{step="begin"}`,
					`transform_duration{step="index"}`,
					`transform_duration{step="load"}`,
					`transform_duration{step="finalize"}`,
					`transform_duration{step="commit"}`,
				} {
					if strings.HasPrefix(step, "transform_") || owner != "finished" {
						if got, want := strings.Contains(text, step), owner != "finished"; got != want {
							t.Fatalf("%s: %s present=%t, want %t\n%s", owner, step, got, want, text)
						}
					}
				}
				if owner == "resume" {
					assertHistoricalGroups(t, registry, true)
				}
				control, err := r.Control(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if err := r.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				if owner != "first" {
					assertOmniLogsSnapshot(t, f, control, want)
				}
			}
			if notified != events || ticks.Load() <= 3 || events != len(want.Calls) || !slices.Equal(calls, omniLogsKeys(want.Calls)) {
				t.Fatalf("clock reads=%d, reported events=%d, callbacks=%v", ticks.Load(), events, calls)
			}
		})
	}
}
