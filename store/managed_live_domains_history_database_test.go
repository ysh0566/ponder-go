package store

import (
	"context"
	"testing"
)

func TestManagedLiveDomainsHistoricalAck(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := liveDomainsFixture(t, dialect)
			d := open(opts)
			batch, request := liveDomainsSeed(t, d)
			liveDomainsCacheSeed(t, d)
			fault, restore := liveDomainsFault(d, "business", "exec_abort")
			_, err := d.CoordinateReorg(ctx, request)
			restore()
			if err == nil || !fault.fired {
				t.Fatal("pending not staged")
			}
			if err = d.Close(ctx); err != nil {
				t.Fatal(err)
			}
			opts.OpenID = "covered-then-reorg"
			d = open(opts)
			covered, err := d.CoordinateReorg(ctx, request)
			if err != nil || covered.Outcome != "covered-by-recovery" {
				t.Fatalf("covered=%+v %v", covered, err)
			}
			c, err := d.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			batch.ID = "real-replay-before-later-reorg"
			batch.Generation = c.Generation
			batch.ExpectedLastOperation = c.LastOperation
			batch.ExpectedTail = c.Tail
			batch.Baselines = nil
			acquired, err := d.RetainBatch(ctx, batch)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			var saved *Writer
			if _, err = d.ApplyBatch(ctx, batch, acquired, liveBatchPGSharedHandler(t, &calls, &saved)); err != nil {
				t.Fatal(err)
			}
			c, err = d.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			later := request
			later.Reorg.ID = "later-reorg-prunes-old-proof"
			later.Reorg.Generation = c.Generation
			later.Reorg.ExpectedLastOperation = c.LastOperation
			later.Reorg.ExpectedTail = c.Tail
			if _, err = d.CoordinateReorg(ctx, later); err != nil {
				t.Fatal(err)
			}
			// Explicitly prove the physical old unsafe proof is gone. The historical ack
			// must remain valid through the immutable receipt prefix, not this table.
			durable, _, err := d.business(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, h := range durable.Proofs {
				if h == request.Orphaned[0] || h == request.Orphaned[1] {
					t.Fatal("counterexample did not prune old proofs")
				}
			}
			if _, err = d.Control(ctx); err != nil {
				t.Fatalf("later valid reorg invalidated older covered ack: %v", err)
			}
			if again, err := d.CoordinateReorg(ctx, request); err != nil || managedHash(again) != managedHash(covered) {
				t.Fatalf("old covered retry=%+v %v", again, err)
			}
			if err = d.Close(ctx); err != nil {
				t.Fatal(err)
			}
			opts.OpenID = "covered-after-second-reopen"
			d = open(opts)
			c, err = d.Control(ctx)
			if err != nil || c.Generation != 3 {
				t.Fatalf("second reopen=%+v %v", c, err)
			}
		})
	}
}
