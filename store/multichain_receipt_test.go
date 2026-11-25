package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/interval"
	"github.com/ysh0566/ponder-go/schema"
)

// A restart can resume a block after another chain has already run. Rewinding
// the peer interval must retain the header proving any earlier callback in it.
func TestMultichainPartialPeerBlock(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			db, err := New(ctx, sourceBackend(t, ctx, dialect), model(false), fmt.Sprintf("ponder_go_partialpeer_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			peer := SyncBlock{ChainID: 2, Number: 1, Timestamp: 20, Hash: "peer", ParentHash: "genesis", Payload: []byte("{}")}
			if err := db.SaveBlock(ctx, peer); err != nil {
				t.Fatal(err)
			}
			applyPeer := func(id string, index uint64, value string) {
				t.Helper()
				cp := checkpoint.Block(20, 2, 1, false)
				cp.EventIndex = index
				if err := db.Apply(ctx, Event{ID: id, Checkpoint: cp}, func(w *Writer) error {
					return w.Write(ctx, "account", Row{"id": "peer", "balance": value}, Upsert)
				}); err != nil {
					t.Fatal(err)
				}
			}
			applyPeer("before", 1, "1")
			if err := db.Apply(ctx, event("fork", 1, 1), func(*Writer) error { return nil }); err != nil {
				t.Fatal(err)
			}
			applyPeer("after", 2, "2")
			if err := db.RecordBlockCompletion(ctx, peer); err != nil {
				t.Fatal(err)
			}
			if err := db.CompleteBlock(ctx, peer, "peer"); err != nil {
				t.Fatal(err)
			}
			if err := db.FinalizeIndexedBlock(ctx, peer); err != nil {
				t.Fatal(err)
			}
			if err := db.RevertAndRewind(ctx, checkpoint.Block(0, 1, 0, true)); err != nil {
				t.Fatal(err)
			}
			generation, err := db.PendingMultichainReplay(ctx)
			if err != nil || generation == "" {
				t.Fatalf("missing replay generation %q: %v", generation, err)
			}
			if err := db.FinishMultichainReplay(ctx, "stale"); !errors.Is(err, ErrIndexedBlockChanged) {
				t.Fatalf("stale replay acknowledgment: %v", err)
			}
			header, err := db.SyncedBlock(ctx, 2, 1)
			if err != nil || header == nil || header.Hash != peer.Hash || balance(t, db, "peer") != "1" {
				t.Fatalf("peer header=%v err=%v", header, err)
			}
			gaps, err := db.Missing(ctx, 2, "peer", interval.Interval{1, 1})
			if err != nil || len(gaps) != 1 || gaps[0] != (interval.Interval{1, 1}) {
				t.Fatalf("missing=%v err=%v", gaps, err)
			}
			applyPeer("before", 1, "99")
			if balance(t, db, "peer") != "1" {
				t.Fatal("retained callback executed twice")
			}
			applyPeer("after", 2, "3")
			if balance(t, db, "peer") != "3" {
				t.Fatal("removed callback did not replay")
			}
		})
	}
}
