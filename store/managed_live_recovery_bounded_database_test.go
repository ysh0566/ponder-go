package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
)

func TestManagedLiveRecoveryBoundedFork(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, end := range []uint64{5, 6} {
				t.Run(fmt.Sprintf("end_%d", end), func(t *testing.T) {
					ctx := context.Background()
					_, opts, open := liveRecoveryFixture(t, dialect)
					opts.EndBlocks = map[uint64]uint64{1: end, 10: 6}
					s, err := open(opts)
					if err != nil {
						t.Fatal(err)
					}
					m := s.ManagedLive()
					batch := liveSegmentsUnitBatch(m)
					batch.Blocks[1].Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(105, 1, 5, true))
					calls := 0
					var saved *Writer
					if _, err := m.ApplyBatch(ctx, batch, liveBatchPGRetain(t, m, batch), liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
						t.Fatal(err)
					}
					if end == 6 {
						c, err := m.Control(ctx)
						if err != nil {
							t.Fatal(err)
						}
						a6 := liveSegmentsUnitBlock(1, 6, batch.Blocks[1].Head.Hash)
						a6.Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(106, 1, 6, true))
						extra := ManagedLiveBatch{ID: "old-bounded-a6", Generation: 1, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{a6}}
						if _, err := m.ApplyBatch(ctx, extra, liveBatchPGRetain(t, m, extra), func(w *Writer, _ ManagedLiveBlock) error {
							return w.Write(ctx, "account", Row{"id": "shared", "balance": "30"}, Update)
						}); err != nil {
							t.Fatal(err)
						}
					}
					public, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
					if err != nil {
						t.Fatal(err)
					}
					managedTestClose(t, s)
					opts.OpenID = "bounded-fork-recovery"
					s, err = open(opts)
					if err != nil {
						t.Fatal(err)
					}
					m = s.ManagedLive()
					c, err := m.Control(ctx)
					if err != nil {
						t.Fatal(err)
					}
					actual, err := checkpointStates(ctx, s.businessBackend, s.BusinessStore())
					if err != nil || managedHash(actual) != managedHash(public) {
						t.Fatalf("recovery changed public head before replay: %v %v", actual, err)
					}
					blocks := append([]ManagedLiveBlock(nil), batch.Blocks...)
					stamp := uint64(104)
					if end == 6 {
						stamp = 200
					}
					blocks[1].Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(stamp, 1, 5, true))
					blocks[1].Head.Hash = fmt.Sprintf("0x%064x", end*9999)
					blocks[1].InputDigest = managedHash("fresh-bounded-fork")
					replay := ManagedLiveBatch{ID: "bounded-new-fork-group", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: blocks}
					calls = 0
					if _, err := m.ApplyBatch(ctx, replay, liveBatchPGRetain(t, m, replay), liveBatchPGSharedHandler(t, &calls, &saved)); err != nil || calls != 3 {
						t.Fatalf("valid acquired fork replay failed: calls=%d %v", calls, err)
					}
					c, err = m.Control(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if end == 5 {
						if len(c.ReplayTargets) != 0 {
							t.Fatalf("completed bounded end still has unreachable old timestamp target: %+v", c.ReplayTargets)
						}
					} else {
						if len(c.ReplayTargets) != 2 {
							t.Fatal("lower height with higher timestamp prematurely completed replay")
						}
						actual, err = checkpointStates(ctx, s.businessBackend, s.BusinessStore())
						if err != nil || managedHash(actual) != managedHash(public) {
							t.Fatalf("partial replay replaced unreplayed public height: %v %v", actual, err)
						}
						a6 := liveSegmentsUnitBlock(1, 6, blocks[1].Head.Hash)
						a6.Head.Checkpoint = checkpoint.MustEncode(checkpoint.Block(201, 1, 6, true))
						a6.Head.Hash = fmt.Sprintf("0x%064x", 666666)
						a6.InputDigest = managedHash("fresh-bounded-a6")
						next := ManagedLiveBatch{ID: "bounded-actual-end", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{a6}}
						if _, err := m.ApplyBatch(ctx, next, liveBatchPGRetain(t, m, next), func(w *Writer, _ ManagedLiveBlock) error {
							return w.Write(ctx, "account", Row{"id": "shared", "balance": "30"}, Update)
						}); err != nil {
							t.Fatal(err)
						}
						c, err = m.Control(ctx)
						if err != nil || len(c.ReplayTargets) != 0 {
							t.Fatalf("actual bounded end failed to complete: %+v %v", c, err)
						}
					}
					actual, err = checkpointStates(ctx, s.businessBackend, s.BusinessStore())
					if err != nil {
						t.Fatal(err)
					}
					for _, state := range actual {
						if state.LatestCheckpoint != c.Cursors[state.ChainID].Checkpoint {
							t.Fatalf("committed replay retained orphan public checkpoint: %+v private=%+v", state, c.Cursors[state.ChainID])
						}
					}
					if ready, err := m.Ready(ctx); err != nil || ready {
						t.Fatalf("bounded replay invented runtime readiness: %v %v", ready, err)
					}
				})
			}
		})
	}
}
