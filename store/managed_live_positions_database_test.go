package store

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"testing"
)

func sourceNativeBootstrap(t *testing.T, d *ManagedLiveDomains) ManagedLiveBatch {
	t.Helper()
	ctx := context.Background()
	c, e := d.Control(ctx)
	if e != nil {
		t.Fatal(e)
	}
	b := ManagedLiveBatch{ID: "source-bootstrap", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, Baselines: cloneLiveHeads(d.live().opts.FinalizedHeads), InitialCheckpoints: map[uint64]string{}}
	for id, h := range b.Baselines {
		cp, _ := checkpoint.Decode(h.Checkpoint)
		b.InitialCheckpoints[id] = h.Checkpoint
		if d.live().opts.runtimeStarts[id] <= cp.BlockNumber {
			b.InitialCheckpoints[id] = checkpoint.MustEncode(checkpoint.Block(cp.BlockTimestamp, id, cp.BlockNumber, false))
		}
	}
	sourceNativeBatch(t, d, b, nil)
	return b
}
func sourceNativeBatch(t *testing.T, d *ManagedLiveDomains, b ManagedLiveBatch, fn func(*Writer, ManagedLiveBlock) error) {
	t.Helper()
	ctx := context.Background()
	io, scope, e := d.NewRuntimeScope(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer scope.Seal(ctx)
	a, e := d.RetainBatch(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	_, e = d.ApplyRuntimeBatch(io, b, a, func(_ context.Context, w *Writer, block ManagedLiveBlock) error {
		if fn != nil {
			return fn(w, block)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = scope.Seal(ctx); e != nil {
		t.Fatal(e)
	}
}
func sourceNativeFinalize(t *testing.T, d *ManagedLiveDomains, id string, heads map[uint64]ManagedLiveHead) error {
	t.Helper()
	ctx := context.Background()
	c, e := d.Control(ctx)
	if e != nil {
		return e
	}
	q := ManagedLiveFinalize{ID: id, Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, ExpectedSafeOrdinal: c.SafeOrdinal, Finalized: heads}
	a, e := d.RetainFinalize(ctx, q)
	if e != nil {
		return e
	}
	_, e = d.ApplyFinalize(ctx, q, a)
	return e
}
func TestManagedLiveSourceIdentity(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, setup := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/setup_%t", backend, setup), func(t *testing.T) {
				ctx := context.Background()
				o, opts, open := continuousStoreFixture(t, backend)
				opts.SourceCheckpoints = true
				opts.Setup = setup
				d := open(opts)
				if e := d.Close(ctx); e != nil {
					t.Fatal(e)
				}
				other := opts
				other.OpenID = "source-forbidden-setup-change"
				other.Setup = !setup
				var wrong *ManagedLiveDomains
				var e error
				if backend == "postgres" {
					wrong, e = OpenManagedLiveRuntimePostgres(ctx, o, other)
				} else {
					wrong, e = OpenManagedLiveRuntimePGlite(ctx, o, other)
				}
				if wrong != nil {
					_ = wrong.Close(ctx)
				}
				if e == nil {
					t.Fatal("v9 adopted namespace across setup policy")
				}
			})
		}
	}
}
func TestManagedLiveSourceFinalizeRequiresExecution(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := continuousStoreFixture(t, backend)
			opts.SourceCheckpoints = true
			for id, h := range opts.FinalizedHeads {
				cp, _ := checkpoint.Decode(h.Checkpoint)
				opts.StartBlocks[id] = cp.BlockNumber
			}
			d := open(opts)
			base := sourceNativeBootstrap(t, d)
			if e := sourceNativeFinalize(t, d, "source-premature-finalize", map[uint64]ManagedLiveHead{1: base.Baselines[1]}); e == nil {
				t.Fatal("header-only bootstrap certified unexecuted Down as Up")
			}
			c, e := d.Control(ctx)
			if e != nil {
				t.Fatal(e)
			}
			block := ManagedLiveBlock{Head: base.Baselines[1], InputDigest: managedHash("actual-first-block")}
			b := ManagedLiveBatch{ID: "source-execute-first", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{block}}
			sourceNativeBatch(t, d, b, nil)
			if e := sourceNativeFinalize(t, d, "source-executed-finalize", map[uint64]ManagedLiveHead{1: block.Head}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestManagedLiveSourceReorgRestoresDown(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := continuousStoreFixture(t, backend)
			opts.SourceCheckpoints = true
			cp, _ := checkpoint.Decode(opts.FinalizedHeads[10].Checkpoint)
			opts.StartBlocks[10] = cp.BlockNumber
			d := open(opts)
			base := sourceNativeBootstrap(t, d)
			c, e := d.Control(ctx)
			if e != nil {
				t.Fatal(e)
			}
			aBlock := liveSegmentsUnitBlock(1, 5, base.Baselines[1].Hash)
			bBlock := ManagedLiveBlock{Head: base.Baselines[10], InputDigest: managedHash("chain10-first")}
			batch := ManagedLiveBatch{ID: "source-global-work", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{aBlock, bBlock}}
			fn := func(w *Writer, block ManagedLiveBlock) error {
				at, _ := checkpoint.Decode(block.Head.Checkpoint)
				row, e := w.Find(ctx, "account", Row{"id": "shared"})
				if e != nil {
					return e
				}
				value := fmt.Sprint(at.ChainID)
				if row != nil {
					value = fmt.Sprint(row["balance"]) + value
					_, e = w.Indexing().Update(ctx, "account", Row{"id": "shared"}, Row{"balance": value})
				} else {
					_, e = w.Indexing().InsertOne(ctx, "account", Row{"id": "shared", "balance": value}, Conflict{})
				}
				return e
			}
			sourceNativeBatch(t, d, batch, fn)
			if e := sourceNativeFinalize(t, d, "source-only-other-finality", map[uint64]ManagedLiveHead{10: bBlock.Head}); e != nil {
				t.Fatal(e)
			}
			c, e = d.Control(ctx)
			if e != nil || c.SafeOrdinal != 0 {
				t.Fatalf("global unsafe prefix unexpectedly pruned %+v %v", c, e)
			}
			request := ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{ID: "source-global-reorg", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: base.Baselines[1]}, Orphaned: []ManagedLiveHead{aBlock.Head}}
			result, e := d.CoordinateReorg(ctx, request)
			if e != nil || result.Reorg == nil || len(result.Reorg.Removed) != 2 {
				t.Fatalf("global down reorg=%+v %v", result, e)
			}
			c, e = d.Control(ctx)
			if e != nil || c.LogicalCursors[10] != base.InitialCheckpoints[10] || !liveDown(c.LogicalCursors[10]) {
				t.Fatalf("down projection lost %+v %v", c, e)
			}
			rows, e := d.storage.businessBackend.Query(ctx, "SELECT checkpoint FROM "+d.storage.BusinessStore().internal("_ponder_go_runtime_cursor")+" WHERE chain_id=10")
			if e != nil || len(rows.Rows) != 1 || rows.Rows[0]["checkpoint"] != c.LogicalCursors[10] {
				t.Fatalf("actual SQL cursor differs %+v %v", rows, e)
			}
			liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "missing")
		})
	}
}

func TestManagedLiveSourceBootstrapAdmission(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := continuousStoreFixture(t, backend)
			opts.SourceCheckpoints = true
			d := open(opts)
			c, e := d.Control(ctx)
			if e != nil {
				t.Fatal(e)
			}
			b := ManagedLiveBatch{ID: "forbidden-nonempty-bootstrap", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, Baselines: cloneLiveHeads(opts.FinalizedHeads), InitialCheckpoints: map[uint64]string{}}
			for id, h := range b.Baselines {
				b.InitialCheckpoints[id] = h.Checkpoint
			}
			b.Blocks = []ManagedLiveBlock{liveSegmentsUnitBlock(1, 5, b.Baselines[1].Hash)}
			io, scope, e := d.NewRuntimeScope(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer scope.Seal(ctx)
			a, e := d.RetainBatch(ctx, b)
			if e != nil {
				t.Fatal(e)
			}
			called := 0
			_, e = d.ApplyRuntimeBatch(io, b, a, func(context.Context, *Writer, ManagedLiveBlock) error { called++; return nil })
			if e == nil || called != 0 {
				t.Fatalf("nonempty initial batch entered callback called=%d err=%v", called, e)
			}
			if e = scope.Seal(ctx); e != nil {
				t.Fatal(e)
			}
			c, e = d.Control(ctx)
			if e != nil || c.Batches != 0 {
				t.Fatalf("invalid first batch persisted %+v %v", c, e)
			}
		})
	}
}

func TestManagedLiveSourceRetainedExecutedFinality(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, target := range []string{"anchor", "higher"} {
			t.Run(backend+"/"+target, func(t *testing.T) {
				ctx := context.Background()
				_, opts, open := continuousStoreFixture(t, backend)
				opts.SourceCheckpoints = true
				for id, h := range opts.FinalizedHeads {
					cp, _ := checkpoint.Decode(h.Checkpoint)
					opts.StartBlocks[id] = cp.BlockNumber
				}
				d := open(opts)
				base := sourceNativeBootstrap(t, d)
				c, e := d.Control(ctx)
				if e != nil {
					t.Fatal(e)
				}
				a0 := ManagedLiveBlock{Head: base.Baselines[1], InputDigest: managedHash("A4-executed")}
				a1 := liveSegmentsUnitBlock(1, 5, a0.Head.Hash)
				b0 := ManagedLiveBlock{Head: base.Baselines[10], InputDigest: managedHash("B4-executed")}
				b1 := liveSegmentsUnitBlock(10, 5, b0.Head.Hash)
				sourceNativeBatch(t, d, ManagedLiveBatch{ID: "retained-execution", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, Blocks: []ManagedLiveBlock{a0, a1, b0, b1}}, nil)
				c, e = d.Control(ctx)
				if e != nil {
					t.Fatal(e)
				}
				_, e = d.CoordinateReorg(ctx, ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{ID: "rewind-executed-B", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: a0.Head}, Orphaned: []ManagedLiveHead{a1.Head}})
				if e != nil {
					t.Fatal(e)
				}
				c, e = d.Control(ctx)
				if e != nil || !liveDown(c.LogicalCursors[10]) || !liveDown(c.LogicalFinality[10]) {
					t.Fatalf("expected private Down with prior Down finality %+v %v", c, e)
				}
				want := b0.Head
				if target == "higher" {
					want = b1.Head
				}
				// Independent fixtures isolate Down4 -> Up4 and Down4 -> Up5.
				if e = sourceNativeFinalize(t, d, "retained-B-up", map[uint64]ManagedLiveHead{10: want}); e != nil {
					t.Fatalf("retained executed %s rejected %v", target, e)
				}
				c, e = d.Control(ctx)
				if e != nil || !liveDown(c.LogicalCursors[10]) || c.LogicalFinality[10] != want.Checkpoint {
					t.Fatalf("finalize changed private replay cursor %+v %v", c, e)
				}
			})
		}
	}
}
