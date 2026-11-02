package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestManagedLiveRecoveryFinality(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := liveRecoveryFixture(t, dialect)
			s, err := open(opts)
			if err != nil {
				t.Fatal(err)
			}
			m := s.ManagedLive()
			batch := liveSegmentsUnitBatch(m)
			calls := 0
			var saved *Writer
			if _, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
				t.Fatal(err)
			}
			for _, heads := range []map[uint64]ManagedLiveHead{{1: batch.Blocks[1].Head}, {10: batch.Blocks[0].Head}} {
				c, err := m.Control(ctx)
				if err != nil {
					t.Fatal(err)
				}
				r := ManagedLiveFinalize{ID: fmt.Sprintf("safe-prefix-%d", c.Finalizations), Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, ExpectedSafeOrdinal: c.SafeOrdinal, Finalized: heads}
				a, err := m.RetainFinalize(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.ApplyFinalize(ctx, r, a); err != nil {
					t.Fatal(err)
				}
			}
			c, err := m.Control(ctx)
			if err != nil || c.SafeOrdinal != 2 {
				t.Fatalf("safe prefix=%+v %v", c, err)
			}
			a6 := liveSegmentsUnitBlock(1, 6, batch.Blocks[1].Head.Hash)
			extra := ManagedLiveBatch{ID: "unsafe-a6", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{a6}}
			if _, err := m.ApplyBatch(ctx, extra, liveBatchPGRetain(t, m, extra), func(w *Writer, _ ManagedLiveBlock) error {
				return w.Write(ctx, "account", Row{"id": "shared", "balance": "30"}, Update)
			}); err != nil {
				t.Fatal(err)
			}
			public, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
			if err != nil {
				t.Fatal(err)
			}
			c, err = m.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			opts.FinalizedHeads = cloneLiveHeads(c.Finality)
			managedTestClose(t, s)
			for generation := uint64(2); generation <= 3; generation++ {
				opts.OpenID = fmt.Sprintf("recover-safe-%d", generation)
				s, err = open(opts)
				if err != nil {
					t.Fatal(err)
				}
				m = s.ManagedLive()
				c, err = m.Control(ctx)
				if err != nil || c.Generation != generation || c.Tail != 2 || c.Next != 5 || c.SafeOrdinal != 2 || len(c.ReplayTargets) != 2 || c.Cursors[1] != batch.Blocks[1].Head || c.Cursors[10] != batch.Blocks[0].Head {
					t.Fatalf("safe recovery=%+v %v", c, err)
				}
				liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "20")
				actual, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
				if err != nil || managedHash(actual) != managedHash(public) {
					t.Fatalf("reopen lowered public head/finality: %v %v", actual, err)
				}
				if generation == 2 {
					managedTestClose(t, s)
				}
			}
			partial := ManagedLiveBatch{ID: "replay-empty-b6", Generation: 3, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{batch.Blocks[2]}}
			result, err := m.ApplyBatch(ctx, partial, liveBatchPGRetain(t, m, partial), nil)
			if err != nil || len(result.Segments) != 1 || result.Segments[0].Ordinal != 5 || result.Segments[0].Journal.Count != 0 {
				t.Fatalf("empty replay=%+v %v", result, err)
			}
			actual, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
			if err != nil || managedHash(actual) != managedHash(public) {
				t.Fatalf("partial replay regressed public: %v %v", actual, err)
			}
			c, err = m.Control(ctx)
			if err != nil || len(c.ReplayTargets) != 2 {
				t.Fatalf("incomplete replay lost target=%+v %v", c, err)
			}
			replay := ManagedLiveBatch{ID: "replay-a6", Generation: 3, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{a6}}
			acquired := liveBatchPGRetain(t, m, replay)
			before, err := m.snapshotHash(ctx, s.businessBackend, c.LastOperation)
			if err != nil {
				t.Fatal(err)
			}
			abort := errors.New("abort after actual replay write")
			if _, err := m.ApplyBatch(ctx, replay, acquired, func(w *Writer, _ ManagedLiveBlock) error {
				if err := w.Write(ctx, "account", Row{"id": "shared", "balance": "30"}, Update); err != nil {
					return err
				}
				return abort
			}); !errors.Is(err, abort) {
				t.Fatalf("replay fault=%v", err)
			}
			after, err := m.snapshotHash(ctx, s.businessBackend, c.LastOperation)
			if err != nil || before != after {
				t.Fatalf("failed replay changed exact state: %v", err)
			}
			if _, err := m.ApplyBatch(ctx, replay, acquired, func(w *Writer, _ ManagedLiveBlock) error {
				return w.Write(ctx, "account", Row{"id": "shared", "balance": "30"}, Update)
			}); err != nil {
				t.Fatal(err)
			}
			c, err = m.Control(ctx)
			if err != nil || c.Next != 7 || c.Tail != 6 || len(c.ReplayTargets) != 0 || c.Recoveries != 2 {
				t.Fatalf("complete replay=%+v %v", c, err)
			}
			liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "30")
			if ready, err := m.Ready(ctx); err != nil || ready {
				t.Fatalf("storage replay invented runtime readiness=%v %v", ready, err)
			}
		})
	}
}

func TestManagedLiveRecoveryFormatIsolation(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, initialV2 := range []bool{false, true} {
				t.Run(fmt.Sprintf("initial_v2_%v", initialV2), func(t *testing.T) {
					o, opts, oldOpen := liveDBFixture(t, dialect)
					newOpen := func(in ManagedLiveOptions) (*IndependentStorage, error) {
						if dialect == "postgres" {
							return OpenManagedLiveRecoveryIndependentPostgres(context.Background(), o, in)
						}
						return OpenManagedLiveRecoveryIndependentPGlite(context.Background(), o, in)
					}
					first, second := oldOpen, newOpen
					if initialV2 {
						first, second = newOpen, oldOpen
					}
					s, err := first(opts)
					if err != nil {
						t.Fatal(err)
					}
					c, err := s.ManagedLive().Control(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					managedTestClose(t, s)
					opts.OpenID = "incompatible-format"
					if wrong, err := second(opts); err == nil {
						managedTestClose(t, wrong)
						t.Fatal("silently adopted incompatible namespace")
					}
					opts.OpenID = "same-format-reopen"
					s, err = first(opts)
					if err != nil {
						t.Fatal(err)
					}
					defer managedTestClose(t, s)
					got, err := s.ManagedLive().Control(context.Background())
					if err != nil || got.Generation != 2 || got.Version != c.Version {
						t.Fatalf("rejected cross-format open mutated original=%+v %v", got, err)
					}
				})
			}
		})
	}
}

func TestManagedLiveRecoveryRetainedFinality(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := liveRecoveryFixture(t, dialect)
			s, err := open(opts)
			if err != nil {
				t.Fatal(err)
			}
			m := s.ManagedLive()
			batch := liveSegmentsUnitBatch(m)
			calls := 0
			var saved *Writer
			if _, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
				t.Fatal(err)
			}
			c, err := m.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			finalize := ManagedLiveFinalize{ID: "finality-above-global-safe", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Finalized: map[uint64]ManagedLiveHead{1: batch.Blocks[1].Head}}
			acquired, err := m.RetainFinalize(ctx, finalize)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := m.ApplyFinalize(ctx, finalize, acquired); err != nil || len(result.Pruned) != 0 {
				t.Fatalf("unsafe earlier chain was skipped: %+v %v", result, err)
			}
			c, err = m.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			public, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
			if err != nil {
				t.Fatal(err)
			}
			opts.FinalizedHeads = cloneLiveHeads(c.Finality)
			managedTestClose(t, s)
			opts.OpenID = "recover-with-retained-finality"
			s, err = open(opts)
			if err != nil {
				t.Fatal(err)
			}
			m = s.ManagedLive()
			c, err = m.Control(ctx)
			if err != nil || c.Generation != 2 || c.SafeOrdinal != 0 || c.Tail != 0 || c.Cursors[1] != batch.Baselines[1] || c.Finality[1] != batch.Blocks[1].Head {
				t.Fatalf("recovery lost higher finality: %+v %v", c, err)
			}
			actual, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
			if err != nil || managedHash(actual) != managedHash(public) {
				t.Fatalf("higher public finality changed: %v %v", actual, err)
			}
			liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "missing")
			fork := append([]ManagedLiveBlock(nil), batch.Blocks...)
			fork[1].Head.Hash = fmt.Sprintf("0x%064x", 999999)
			bad := ManagedLiveBatch{ID: "competing-finalized-replay", Generation: 2, ExpectedLastOperation: c.LastOperation, ExpectedTail: 0, Blocks: fork}
			before, err := m.snapshotHash(ctx, s.businessBackend, c.LastOperation)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.ApplyBatch(ctx, bad, liveBatchPGRetain(t, m, bad), func(*Writer, ManagedLiveBlock) error { t.Fatal("conflicting finalized replay ran handler"); return nil }); err == nil {
				t.Fatal("accepted competing fork below retained finality")
			}
			after, err := m.snapshotHash(ctx, s.businessBackend, c.LastOperation)
			if err != nil || before != after {
				t.Fatalf("rejected finalized replay changed state: %v", err)
			}
			good := bad
			good.ID = "same-finalized-branch-replay"
			good.Blocks = batch.Blocks
			calls = 0
			if _, err := m.ApplyBatch(ctx, good, liveBatchPGRetain(t, m, good), liveBatchPGSharedHandler(t, &calls, &saved)); err != nil || calls != 3 {
				t.Fatalf("same-finality replay failed: calls=%d %v", calls, err)
			}
			if _, err := m.Control(ctx); err != nil {
				t.Fatal(err)
			}
			liveBatchPGBalance(t, s.businessBackend, s.BusinessStore(), "20")
		})
	}
}
