package store

import (
	"context"
	"fmt"
	"testing"

	"ponder.local/ponder-go/checkpoint"
)

// Public checkpoints can describe a retained pre-reorg projection. Recovery
// authenticates that projection through receipts, not a timestamp/height sort.
func TestManagedLiveRecoveryPublicProjection(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, mode := range []string{"cross_fork_timestamp", "global_reorg_height"} {
				t.Run(mode, func(t *testing.T) {
					ctx := context.Background()
					_, opts, open := liveRecoveryFixture(t, dialect)
					opts.EndBlocks = map[uint64]uint64{1: 6, 10: 6}
					s, err := open(opts)
					if err != nil {
						t.Fatal(err)
					}
					m := s.ManagedLive()
					batch := liveSegmentsUnitBatch(m)
					if mode == "cross_fork_timestamp" {
						batch.Blocks[1].Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(105, 1, 5, true))
					}
					a6 := liveSegmentsUnitBlock(1, 6, batch.Blocks[1].Head.Hash)
					if mode == "cross_fork_timestamp" {
						a6.Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(106, 1, 6, true))
					}
					batch.Blocks = append(batch.Blocks, a6)
					if _, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), nil); err != nil {
						t.Fatal(err)
					}
					finalize := func(head ManagedLiveHead) {
						t.Helper()
						c, err := m.Control(ctx)
						if err != nil {
							t.Fatal(err)
						}
						f := ManagedLiveFinalize{ID: "finality-" + mode, Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, ExpectedSafeOrdinal: c.SafeOrdinal, Finalized: map[uint64]ManagedLiveHead{1: head}}
						fa, err := m.RetainFinalize(ctx, f)
						if err != nil {
							t.Fatal(err)
						}
						if result, err := m.ApplyFinalize(ctx, f, fa); err != nil || len(result.Pruned) != 0 {
							t.Fatalf("finality crossed earlier unsafe B segment: %+v %v", result, err)
						}
					}
					if mode == "cross_fork_timestamp" {
						managedTestClose(t, s)
						opts.OpenID = "initial-recovery"
						s, err = open(opts)
						if err != nil {
							t.Fatal(err)
						}
						m = s.ManagedLive()
						c, err := m.Control(ctx)
						if err != nil {
							t.Fatal(err)
						}
						batch.Blocks[1].Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(200, 1, 5, true))
						batch.Blocks[1].Head.Hash = fmt.Sprintf("0x%064x", 987000)
						batch.Blocks[1].InputDigest = managedHash("fresh-projection-a5")
						batch.Blocks[3].Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(201, 1, 6, true))
						batch.Blocks[3].Head.Hash = fmt.Sprintf("0x%064x", 987001)
						batch.Blocks[3].Head.ParentHash = batch.Blocks[1].Head.Hash
						batch.Blocks[3].InputDigest = managedHash("fresh-projection-a6")
						partial := ManagedLiveBatch{ID: "partial-new-fork", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: batch.Blocks[:3]}
						if _, err := m.ApplyBatch(ctx, partial, liveBatchPGRetain(t, m, partial), nil); err != nil {
							t.Fatal(err)
						}
						finalize(batch.Blocks[1].Head)
					} else {
						finalize(batch.Blocks[3].Head)
						c, err := m.Control(ctx)
						if err != nil {
							t.Fatal(err)
						}
						r := ManagedLiveReorg{ID: "global-b-rewind", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: batch.Baselines[10]}
						ra, err := m.RetainReorg(ctx, r)
						if err != nil {
							t.Fatal(err)
						}
						if result, err := m.ApplyReorg(ctx, r, ra); err != nil || len(result.Removed) != 4 {
							t.Fatalf("empty global suffix rewind=%+v %v", result, err)
						}
						c, err = m.Control(ctx)
						if err != nil {
							t.Fatal(err)
						}
						partial := ManagedLiveBatch{ID: "partial-protected-branch", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{batch.Blocks[1]}}
						if _, err := m.ApplyBatch(ctx, partial, liveBatchPGRetain(t, m, partial), nil); err != nil {
							t.Fatal(err)
						}
					}
					public, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
					if err != nil {
						t.Fatal(err)
					}
					for _, state := range public {
						if state.ChainID == 1 {
							latest, le := checkpoint.Decode(state.LatestCheckpoint)
							finalized, fe := checkpoint.Decode(state.FinalizedCheckpoint)
							if le != nil || fe != nil {
								t.Fatal(le, fe)
							}
							if mode == "cross_fork_timestamp" && (latest.BlockNumber != 6 || finalized.BlockNumber != 5 || latest.BlockTimestamp >= finalized.BlockTimestamp) {
								t.Fatalf("timestamp counterexample not established: %+v", state)
							}
							if mode == "global_reorg_height" && (latest.BlockNumber != 5 || finalized.BlockNumber != 6) {
								t.Fatalf("height counterexample not established: %+v", state)
							}
						}
					}
					before, err := m.Control(ctx)
					if err != nil {
						t.Fatal(err)
					}
					opts.FinalizedHeads = cloneLiveHeads(before.Finality)
					managedTestClose(t, s)
					opts.OpenID = "recover-authenticated-stale-projection"
					s, err = open(opts)
					if err != nil {
						t.Fatalf("audited legitimate stale projection could not recover: %v", err)
					}
					m = s.ManagedLive()
					c, err := m.Control(ctx)
					if err != nil || c.Generation != before.Generation+1 || c.Cursors[1] != batch.Baselines[1] || c.Finality[1] != before.Finality[1] {
						t.Fatalf("stale projection recovery lost lineage: %+v %v", c, err)
					}
					actual, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
					if err != nil || managedHash(actual) != managedHash(public) {
						t.Fatalf("recovery changed public projection: %v %v", actual, err)
					}
					replay := ManagedLiveBatch{ID: "actual-full-replay", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: batch.Blocks}
					result, err := m.ApplyBatch(ctx, replay, liveBatchPGRetain(t, m, replay), nil)
					if err != nil || len(result.Segments) != 4 {
						t.Fatalf("actual full empty-segment replay=%+v %v", result, err)
					}
					c, err = m.Control(ctx)
					if err != nil || len(c.ReplayTargets) != 0 {
						t.Fatalf("replay incomplete=%+v %v", c, err)
					}
					actual, err = checkpointStates(ctx, s.businessBackend, s.BusinessStore())
					if err != nil {
						t.Fatal(err)
					}
					for _, state := range actual {
						if state.LatestCheckpoint != c.Cursors[state.ChainID].Checkpoint {
							t.Fatalf("published checkpoint != actual replay: %+v", state)
						}
					}
					if ready, err := m.Ready(ctx); err != nil || ready {
						t.Fatalf("projection replay invented runtime readiness: %v %v", ready, err)
					}
				})
			}
		})
	}
}
