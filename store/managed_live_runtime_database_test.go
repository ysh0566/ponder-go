package store

import (
	"context"
	"testing"
)

func TestManagedLiveRuntimeActivation(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			o, opts, _ := liveDBFixture(t, dialect)
			opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
			var d *ManagedLiveDomains
			var err error
			if dialect == "postgres" {
				d, err = OpenManagedLiveRuntimePostgres(ctx, o, ManagedLiveRuntimeOptions{ManagedLiveOptions: opts, StartBlocks: map[uint64]uint64{1: 5, 10: 5}})
			} else {
				d, err = OpenManagedLiveRuntimePGlite(ctx, o, ManagedLiveRuntimeOptions{ManagedLiveOptions: opts, StartBlocks: map[uint64]uint64{1: 5, 10: 5}})
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			if ready, err := d.Ready(ctx); err != nil || ready {
				t.Fatalf("initial ready=%v %v", ready, err)
			}
			if err = d.ActivateRuntime(ctx, "premature-ready", opts.FinalizedHeads); err == nil {
				t.Fatal("activated without a real bootstrap/cutover")
			}
			c, err := d.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			batch := ManagedLiveBatch{ID: "real-empty-bootstrap", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Baselines: cloneLiveHeads(opts.FinalizedHeads)}
			a, err := d.RetainBatch(ctx, batch)
			if err != nil {
				t.Fatal(err)
			}
			run, scope, scopeErr := d.NewRuntimeScope(ctx)
			if scopeErr != nil {
				t.Fatal(scopeErr)
			}
			t.Cleanup(func() { scope.Cancel(); _ = scope.Seal(context.Background()) })
			if _, err = d.ApplyRuntimeBatch(run, batch, a, func(context.Context, *Writer, ManagedLiveBlock) error {
				t.Fatal("empty baseline ran handler")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if e := scope.Seal(ctx); e != nil {
				t.Fatal(e)
			}
			if err = d.ActivateRuntime(ctx, "actual-cutover", opts.FinalizedHeads); err != nil {
				t.Fatalf("real activation=%v", err)
			}
			if ready, err := d.Ready(ctx); err != nil || !ready {
				t.Fatalf("after activation ready=%v %v", ready, err)
			}
		})
	}
}
