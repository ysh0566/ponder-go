package historical

import (
	"context"
	"errors"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"testing"
	"time"
)

func TestRuntimeContinuousFinalityEviction(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"eviction_recovers", "rpc_fork_rejected"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				continuousRoot(t)
				s := newContinuousRPC(t, 5)
				s.mu.Lock()
				for _, chain := range s.canonical {
					for n, b := range chain {
						timestamp := uint64(1000)
						if n > 5 {
							timestamp += n - 5
						}
						b["timestamp"] = fmt.Sprintf("0x%x", timestamp)
					}
				}
				s.mu.Unlock()
				b := runtimeBuild(t, s.p, 6, 1, indexing.Handler(finiteIncrement))
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				var r *RuntimeRunner
				advanced := false
				finalizations := 0
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-finality-eviction-v1", OpenID: "eviction-1", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{HeaderCacheCapacity: 2, PollingInterval: time.Millisecond, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
					if e.Type == "finalize" {
						finalizations++
					}
					if e.Type != "block" || advanced {
						return nil
					}
					c, err := r.Control(ctx)
					if err != nil {
						return err
					}
					both := true
					for _, h := range c.Cursors {
						cp, _ := checkpoint.Decode(h.Checkpoint)
						both = both && cp.BlockNumber >= 5
					}
					if both {
						advanced = true
						s.mu.Lock()
						if kind == "rpc_fork_rejected" {
							wrong := finiteBlock(1, 2)
							wrong["timestamp"] = "0x3e8"
							wrong["hash"] = fmt.Sprintf("0x%064x", 88888882)
							s.canonical[1][2] = wrong
						}
						s.latest[1], s.latest[2] = 6, 6
						s.mu.Unlock()
					}
					return nil
				}}}
				r = runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if kind == "rpc_fork_rejected" {
					if !errors.Is(err, store.ErrManagedLiveBatchConflict) || out.Ready || out.Completed {
						t.Fatalf("RPC fork substituted evicted executed identity: %+v err=%v", out, err)
					}
					c, e := r.Control(context.Background())
					if e != nil {
						t.Fatal(e)
					}
					if h, ok := c.Finality[1]; ok {
						cp, _ := checkpoint.Decode(h.Checkpoint)
						if cp.BlockNumber != 0 {
							t.Fatalf("unproven raw fork promoted finality: %+v", c)
						}
					}
					return
				}
				if err != nil || !out.Ready || !out.Completed || !advanced || finalizations != 2 {
					t.Fatalf("eviction blocked real source finality jump: %+v advanced=%v finalizations=%d err=%v", out, advanced, finalizations, err)
				}
				c, err := r.Control(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				for _, head := range c.Finality {
					cp, _ := checkpoint.Decode(head.Checkpoint)
					if cp.BlockNumber != 5 {
						t.Fatalf("finalized burst differs: %+v", c)
					}
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				syncFixture := f
				syncFixture.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
				managedInspect(t, syncFixture, func(db store.Backend) {
					m, err := store.NewSyncMigrator(db, f.options.Owned.Storage.SyncNamespace)
					if err != nil {
						t.Fatal(err)
					}
					rows := managedRows(t, db, "SELECT count(*)::text AS n FROM "+schema.Quote(m.LatestSchema())+".blocks WHERE chain_id IN(1,2) AND number BETWEEN 1 AND 5")
					if len(rows) != 1 || fmt.Sprint(rows[0]["n"]) != "10" {
						t.Fatalf("evicted finalized raw data not persisted: %v", rows)
					}
				})
			})
		}
	}
}
