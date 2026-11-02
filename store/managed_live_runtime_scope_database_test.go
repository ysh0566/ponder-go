package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestManagedRuntimeScopeRejectsUnscopedBypass(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			o, opts, _ := liveDBFixture(t, backend)
			opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
			in := ManagedLiveRuntimeOptions{ManagedLiveOptions: opts, StartBlocks: map[uint64]uint64{1: 5, 10: 5}}
			var d *ManagedLiveDomains
			var err error
			if backend == "postgres" {
				d, err = OpenManagedLiveRuntimePostgres(ctx, o, in)
			} else {
				d, err = OpenManagedLiveRuntimePGlite(ctx, o, in)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			run, scope, err := d.NewRuntimeScope(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { scope.Cancel(); _ = scope.Seal(ctx) })
			if err = d.WithRuntimeSync(run, func(context.Context, *SourceSyncStore) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if err = scope.Seal(ctx); err != nil {
				t.Fatal(err)
			}
			called := false
			if err = d.WithSync(ctx, func(*SourceSyncStore) error { called = true; return nil }); !errors.Is(err, ErrManagedLiveLegacyMutation) || called {
				t.Fatalf("unscoped cache bypass err=%v called=%v", err, called)
			}
			if err = d.WithRuntimeSync(context.WithoutCancel(run), func(context.Context, *SourceSyncStore) error { called = true; return nil }); err == nil || called {
				t.Fatal("sealed scope reused")
			}
			key := `[1,"eth_getBalance",["0x1111111111111111111111111111111111111111","0x5"]]`
			if err := <-d.BeginRuntimeCachePut(context.WithoutCancel(run), 1, key, json.RawMessage(`"0x2a"`)); err == nil {
				t.Fatal("sealed asynchronous cache bypass")
			}
			c, err := d.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			batch := ManagedLiveBatch{ID: "unscoped-bootstrap", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Baselines: cloneLiveHeads(opts.FinalizedHeads)}
			a, err := d.RetainBatch(ctx, batch)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.ApplyBatch(ctx, batch, a, nil); !errors.Is(err, ErrManagedLiveLegacyMutation) {
				t.Fatal("unscoped business bypass", err)
			}
		})
	}
}
