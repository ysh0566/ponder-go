package historical

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"testing"
	"time"
)

func TestRuntimeSourceCommitRecovery(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 2)
			inject := true
			setupCalls := 0
			blocks := 0
			setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				setupCalls++
				_, e := a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("setup-%d", finiteChain(a)), "balance": "99"}, store.Conflict{})
				return e
			})
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error { blocks++; return finiteIncrement(ctx, a) })
			b := sourceMatrixBuild(t, s.p, 1, 2, 1, "both", false, h, setup)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "source-commit-recovery", OpenID: "source-before-finalize", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(_ context.Context, p RuntimeLiveProgress) error {
				if inject && p.Type == "source-block" {
					return fmt.Errorf("controlled stop after real first block commit before finalize")
				}
				return nil
			}}}
			r := runtimeOpen(t, backend, o)
			out, e := r.Run(context.Background())
			if e == nil || out.Ready || out.Completed {
				t.Fatalf("post-commit fault accepted %+v %v", out, e)
			}
			c, e := r.Control(context.Background())
			if e != nil || c.Finalizations != 0 || c.Tail != 1 || blocks != 1 || setupCalls != 2 {
				t.Fatalf("fault not between native batch and finality %+v blocks=%d setup=%d err=%v", c, blocks, setupCalls, e)
			}
			public, e := r.PublicCheckpoints(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			inject = false
			o.OpenID = "source-after-commit-reopen"
			r = runtimeOpen(t, backend, o)
			c, e = r.Control(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			for id, pos := range c.LogicalCursors {
				cp, e := checkpoint.Decode(pos)
				if e != nil || cp.BlockNumber != 1 || store.ManagedLiveReachedBlock(c, id, 1) {
					t.Fatalf("recovery skipped inclusive first block %+v %v", c, e)
				}
			}
			got, e := r.PublicCheckpoints(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			for id, want := range public {
				if got[id] != want {
					t.Fatalf("recovery changed public head before replay %v -> %v", public, got)
				}
			}
			if yes, e := r.Ready(context.Background()); e != nil || yes {
				t.Fatalf("ready before replay %v %v", yes, e)
			}
			out, e = r.Run(context.Background())
			if e != nil || !out.Completed || !out.Ready || !out.Recovered || setupCalls != 2 || blocks != 5 {
				t.Fatalf("real replay closure %+v setup=%d blocks=%d err=%v", out, setupCalls, blocks, e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 4 || fmt.Sprint(rows[0]["balance"]) != "2" || fmt.Sprint(rows[1]["balance"]) != "2" || fmt.Sprint(rows[2]["balance"]) != "99" || fmt.Sprint(rows[3]["balance"]) != "99" {
					t.Fatalf("actual source replay lost/duplicated side effects %v", rows)
				}
			})
		})
	}
}
