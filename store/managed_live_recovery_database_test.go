package store

import (
	"context"
	"errors"
	"testing"
)

func liveRecoveryFixture(t *testing.T, dialect string) (IndependentStorageOptions, ManagedLiveOptions, func(ManagedLiveOptions) (*IndependentStorage, error)) {
	t.Helper()
	o, opts, _ := liveDBFixture(t, dialect)
	opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
	open := func(in ManagedLiveOptions) (*IndependentStorage, error) {
		var s *IndependentStorage
		var err error
		if dialect == "postgres" {
			s, err = OpenManagedLiveRecoveryIndependentPostgres(context.Background(), o, in)
		} else {
			s, err = OpenManagedLiveRecoveryIndependentPGlite(context.Background(), o, in)
		}
		if err == nil {
			t.Cleanup(func() { managedTestClose(t, s) })
		}
		return s, err
	}
	return o, opts, open
}

func TestManagedLiveRecoveryDatabase(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			t.Run("nonempty_reopen_undoes_global_suffix_and_replays_empty_segment", func(t *testing.T) {
				ctx := context.Background()
				_, opts, open := liveRecoveryFixture(t, dialect)
				s, err := open(opts)
				if err != nil {
					t.Fatal(err)
				}
				m := s.ManagedLive()
				batch := liveSegmentsUnitBatch(m)
				oldAcquisition := liveBatchPGRetain(t, m, batch)
				calls := 0
				var writer *Writer
				if _, err := m.ApplyBatch(ctx, batch, oldAcquisition, liveBatchPGSharedHandler(t, &calls, &writer)); err != nil {
					t.Fatal(err)
				}
				if calls != 3 {
					t.Fatalf("initial callbacks=%d", calls)
				}
				public, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
				if err != nil {
					t.Fatal(err)
				}
				managedTestClose(t, s)
				opts.OpenID = "recover-nonempty-2"
				s, err = open(opts)
				if err != nil {
					t.Fatalf("nonempty recovery failed: %v", err)
				}
				m = s.ManagedLive()
				control, err := m.Control(ctx)
				if err != nil || control.Generation != 2 || control.Tail != 0 || control.Next != 4 || control.Batches != 1 || control.Version != "managed-live-recovery-v3" {
					t.Fatalf("recovered control=%+v err=%v", control, err)
				}
				liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "missing")
				after, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
				if err != nil || managedHash(after) != managedHash(public) {
					t.Fatalf("recovery changed public checkpoints: %v %v %v", public, after, err)
				}
				if ready, err := m.Ready(ctx); err != nil || ready {
					t.Fatalf("recovery readiness=%v %v", ready, err)
				}
				if _, err := m.ApplyBatch(ctx, batch, oldAcquisition, nil); !errors.Is(err, ErrManagedLiveBatchConflict) {
					t.Fatalf("old generation admitted: %v", err)
				}
				replay := ManagedLiveBatch{ID: "replay-real-group", Generation: 2, ExpectedLastOperation: control.LastOperation, ExpectedTail: 0, Blocks: batch.Blocks}
				calls = 0
				result, err := m.ApplyBatch(ctx, replay, liveBatchPGRetain(t, m, replay), liveBatchPGSharedHandler(t, &calls, &writer))
				if err != nil || calls != 3 || len(result.Segments) != 3 || result.Segments[0].Ordinal != 4 || result.Segments[2].Ordinal != 6 || result.Segments[2].Journal.Count != 0 {
					t.Fatalf("real replay=%+v calls=%d err=%v", result, calls, err)
				}
				liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "20")
				if _, err := m.Control(ctx); err != nil {
					t.Fatal(err)
				}
				if retry, err := m.ApplyBatch(ctx, replay, liveBatchPGRetain(t, m, replay), func(*Writer, ManagedLiveBlock) error { t.Fatal("exact retry reran handler"); return nil }); err != nil || retry.Applied {
					t.Fatalf("retry=%+v %v", retry, err)
				}
			})
		})
	}
}
