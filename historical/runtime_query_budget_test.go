package historical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeSyncParameterBudget(t *testing.T) {
	g := loadOmniLogsGolden(t)
	tc := omniLogsNamedCase(t, g, "paged_failure")
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			ctx := t.Context()
			p, _ := omniLogsRPC(t, g)
			want := tc.Snapshots[0]
			stop := errors.New("stop during a historical callback")
			var calls []string
			handlers := omniLogsHandlers(func() []omniLogsEvent { return want.Calls }, &calls, func(key string) error {
				if want.Error != nil && key == tc.FailKey {
					return stop
				}
				return nil
			})
			f := newManagedFiniteFixture(t, backend, omniLogsBuild(t, g, p, handlers))
			omnichainGoldenStorage(&f)
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "runtime-sync-budget", OpenID: "too-small", Requesters: runtimeFactory(p, &epochs)}
			o.Historical.QuerySize = tc.QuerySize
			o.Historical.MaxQueryParameters = 4
			r := runtimeOpen(t, backend, o)
			result, err := r.Run(ctx)
			if err == nil || !strings.Contains(err.Error(), "parameter budget smaller than encoded row") || result.Ready || result.Completed || len(calls) != 0 {
				t.Fatalf("insufficient budget: result=%+v calls=%v err=%v", result, calls, err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			assertOmniLogsRows(t, f, omniLogsSnapshot{})

			budgets := []int{32, 64, 0}
			for i, snapshot := range tc.Snapshots {
				o.OpenID = fmt.Sprintf("resume-%d", i)
				o.Historical.MaxQueryParameters = budgets[i]
				want, calls = snapshot, nil
				r := runtimeOpen(t, backend, o)
				result, err := r.Run(ctx)
				if snapshot.Error != nil {
					if err == nil || !strings.Contains(err.Error(), stop.Error()) || result.Ready || result.Completed {
						t.Fatalf("interrupted run: %+v, %v", result, err)
					}
				} else if err != nil || !result.Completed || !result.Ready {
					t.Fatalf("changed budget %d: result=%+v err=%v", budgets[i], result, err)
				}
				if !slices.Equal(calls, omniLogsKeys(snapshot.Calls)) {
					t.Fatalf("budget %d: calls=%v, want=%v", budgets[i], calls, omniLogsKeys(snapshot.Calls))
				}
				control, err := r.Control(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				assertOmniLogsSnapshot(t, f, control, snapshot)
			}
		})
	}
}

func TestRuntimeInvalidSyncBudget(t *testing.T) {
	p := newFiniteRPC(t, 3)
	build := runtimeBuild(t, p, 3, 1, indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil }))
	for _, budget := range []int{-1, 1, 2, 3} {
		o := RuntimeOptions{Historical: Options{Build: build, MaxQueryParameters: budget}, BuildID: "invalid-budget", OpenID: "invalid", Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
			t.Fatal("opened RPC with an invalid sync budget")
			return nil, nil
		}}
		if _, err := OpenRuntimePostgres(t.Context(), o); err == nil || !strings.Contains(err.Error(), "at least 4") {
			t.Fatalf("budget %d: %v", budget, err)
		}
	}
}

func TestLiveRuntimeSyncBudget(t *testing.T) {
	for _, order := range []schema.Ordering{schema.Multichain, schema.Isolated} {
		for _, backend := range []string{"postgres", "pglite"} {
			t.Run(string(order)+"/"+backend, func(t *testing.T) {
				continuousRoot(t)
				ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
				defer cancel()
				state := &runtimeRPCState{latest: map[uint64]uint64{1: 1, 2: 1}}
				p := controlledRuntimeRPC(t, state)
				raw := fmt.Sprintf(`{"ordering":%q,"chains":{"one":{"id":1,"rpc":%q,"reorgWindow":0},"two":{"id":2,"rpc":%q,"reorgWindow":0}},"blocks":{"Head":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":2,"interval":1}}}`, order, p.server.URL+"/1", p.server.URL+"/2")
				calls := map[uint64]int{}
				handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					id := finiteChain(a)
					height, err := continuousNumber(a)
					if err != nil {
						return err
					}
					calls[id]++
					_, err = a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "height": int(height)}, store.Conflict{})
					return err
				})
				b := runtimeSourceBuild(t, raw, config.SourceIndexingFunction{Name: "Head:block", Fn: handler})
				f := newManagedFiniteFixture(t, backend, b)
				f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "seen", PrimaryKey: []string{"chain_id", "height"}, Columns: []schema.Column{
					{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "height", Type: "integer", NotNull: true},
				}}}}
				var epochs []uint64
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), SourceCheckpoints: true, BuildID: "live-budget", OpenID: "small", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				o.Historical.MaxQueryParameters = 4
				r := runtimeOpen(t, backend, o)
				res, err := r.Run(ctx)
				if err == nil || !strings.Contains(err.Error(), "parameter budget smaller than encoded row") || res.Ready || res.Completed || len(calls) != 0 {
					t.Fatalf("undersized backfill: %+v, %v, calls=%v", res, err, calls)
				}
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}

				o.Historical.MaxQueryParameters = 32
				o.OpenID = "resumed"
				o.OnActivated = func(context.Context) error {
					state.mu.Lock()
					state.latest[1], state.latest[2] = 2, 2
					state.mu.Unlock()
					return nil
				}
				r = runtimeOpen(t, backend, o)
				res, err = r.Run(ctx)
				if err != nil || !res.Ready || !res.Completed || res.HistoricalBlocks != 2 || res.LiveBlocks != 2 {
					t.Fatalf("resumed history and live range: %+v, %v", res, err)
				}
				control, err := r.Control(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range []uint64{1, 2} {
					cursor, err := checkpoint.Decode(control.Cursors[id].Checkpoint)
					if err != nil || cursor.ChainID != id || cursor.BlockNumber != 2 || calls[id] != 2 {
						t.Fatalf("chain %d: cursor=%+v calls=%d err=%v", id, cursor, calls[id], err)
					}
				}
			})
		}
	}
}
