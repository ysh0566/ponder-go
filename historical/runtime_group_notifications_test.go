package historical

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeGroupNotifications(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			ctx := t.Context()
			p := newFiniteRPC(t, 3)
			calls := 0
			b := runtimeBuild(t, p, 3, 2, func(ctx context.Context, a *indexing.HandlerArgs) error {
				calls++
				return finiteIncrement(ctx, a)
			})
			f := newManagedFiniteFixture(t, backend, b)
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "group-commits", OpenID: "initial", Requesters: runtimeFactory(p, &epochs)}
			stop := errors.New("stop on first unfinalized commit")
			var r *RuntimeRunner
			var stopped RuntimeCommit
			var empty, historical, live, reported int
			var replayed bool
			ids := map[string]bool{}
			o.OnCommitted = func(ctx context.Context, g RuntimeCommit) error {
				control, err := r.Control(ctx)
				if err != nil {
					return err
				}
				if err := r.Close(ctx); !errors.Is(err, store.ErrManagedLiveBusy) {
					return fmt.Errorf("recursive observer close: %v", err)
				}
				n, err := checkpoint.Decode(g.Checkpoint)
				if err != nil || g.Checkpoint != g.Head.Checkpoint || control.Cursors[g.ChainID] != g.Head || n.ChainID != g.ChainID || g.BlockRange != [2]float64{float64(n.BlockNumber), float64(n.BlockNumber)} {
					return fmt.Errorf("group does not match persisted cursor: %+v control=%+v", g, control.Cursors)
				}
				if g.OperationID == "" || ids[g.OperationID] || g.OperationID != control.LastOperation || !g.Applied || g.Reconciled || g.Attempts != 1 || g.Events != calls-reported {
					return fmt.Errorf("receipt or event count: %+v calls=%d reported=%d", g, calls, reported)
				}
				ids[g.OperationID] = true
				reported = calls
				if g.Events == 0 {
					empty++
				}
				if g.Historical {
					historical++
				} else {
					live++
				}
				if g.Safe || g.Historical != (n.BlockNumber < 3) {
					return fmt.Errorf("phase or recovery point: %+v", g)
				}
				if !g.Historical && stopped.OperationID == "" {
					stopped = g
					return stop
				}
				if stopped.Head == g.Head && stopped.ChainID == g.ChainID {
					replayed = true
				}
				return nil
			}
			r = runtimeOpen(t, backend, o)
			result, err := r.Run(ctx)
			if !errors.Is(err, stop) || result.Completed || historical != 4 || live != 1 {
				t.Fatalf("interrupted run: %+v history=%d live=%d err=%v", result, historical, live, err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			o.OpenID = "resume"
			r = runtimeOpen(t, backend, o)
			result, err = r.Run(ctx)
			if err != nil || !result.Completed || !result.Recovered || !replayed || historical != 4 || live != 3 || empty == 0 || reported != calls {
				t.Fatalf("reopened run: %+v replay=%v history=%d live=%d empty=%d calls=%d err=%v", result, replayed, historical, live, empty, calls, err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || rows[0]["balance"] != "2" || rows[1]["balance"] != "2" {
					t.Fatalf("persisted interval callbacks after replay: %+v", rows)
				}
			})
		})
	}
}

func TestRuntimeCommitReceipt(t *testing.T) {
	var notifications []RuntimeCommit
	r := RuntimeRunner{options: RuntimeOptions{OnCommitted: func(_ context.Context, g RuntimeCommit) error {
		notifications = append(notifications, g)
		return nil
	}}}
	pos := checkpoint.Block(100, 1, 1, true)
	cp, err := checkpoint.Encode(pos)
	if err != nil {
		t.Fatal(err)
	}
	groups := []GroupResult{{ChainID: 1, Checkpoint: cp}}
	control := store.ManagedLiveControl{SafeAnchors: map[uint64]string{1: cp}, Cursors: map[uint64]store.ManagedLiveHead{1: {Checkpoint: cp, Hash: "head"}}}
	receipt := store.ManagedLiveBatchResult{OperationID: "batch"}
	if err := r.notifyCommitted(t.Context(), groups, receipt, control, 2, true); err != nil || len(notifications) != 0 {
		t.Fatalf("already applied receipt: %v %v", notifications, err)
	}
	receipt.Applied, receipt.Reconciled = true, true
	if err := r.notifyCommitted(t.Context(), groups, receipt, control, 2, true); err != nil || len(notifications) != 1 {
		t.Fatalf("confirmed commit: %v %v", notifications, err)
	}
	got := notifications[0]
	if !got.Safe || !got.Historical || !got.Applied || !got.Reconciled || got.Attempts != 2 || got.OperationID != receipt.OperationID || got.Head != control.Cursors[1] {
		t.Fatalf("confirmed commit metadata: %+v", got)
	}
}
