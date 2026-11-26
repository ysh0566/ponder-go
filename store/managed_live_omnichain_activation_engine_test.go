package store

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/internal/testpglite"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/schema"
)

// Interpose only on the commit that seals the named serve receipt. Queries,
// statements, commit, and rollback all run against the actual database.
type activationCrashBackend struct {
	Backend
	beforeCommit func(Transaction)
}

func (b *activationCrashBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &activationCrashTx{Transaction: tx, beforeCommit: b.beforeCommit}, nil
}

type activationCrashTx struct {
	Transaction
	beforeCommit func(Transaction)
}

func (tx *activationCrashTx) Commit(ctx context.Context) error {
	tx.beforeCommit(tx.Transaction)
	return tx.Transaction.Commit(ctx)
}

// Read the business tables without opening a managed owner, which would start
// a new generation and reset readiness before we could inspect the crash.
func checkActivationSnapshot(t *testing.T, ctx context.Context, q Querier, s *Store, id string, before ManagedLiveControl, served bool) {
	t.Helper()
	r, err := q.Query(ctx, "SELECT payload::text AS control,"+
		"(SELECT value->>'is_ready' FROM "+s.internal("_ponder_go_app")+" WHERE key='app') AS ready,"+
		"(SELECT kind||':'||length(after_hash)::text FROM "+s.internal("_ponder_go_live_operations")+" WHERE id=$1) AS receipt,"+
		"to_regclass($2) IS NOT NULL AS indexed,"+
		"(SELECT string_agg(id||'='||balance::text,',' ORDER BY id) FROM "+s.table("account")+") AS accounts "+
		"FROM "+s.internal("_ponder_go_live_control")+" WHERE key='control'", id, s.internal("account_balance_idx"))
	if err != nil || len(r.Rows) != 1 {
		t.Fatalf("read activation snapshot: %v %v", r.Rows, err)
	}
	row := r.Rows[0]
	var control ManagedLiveControl
	if err := json.Unmarshal([]byte(fmt.Sprint(row["control"])), &control); err != nil {
		t.Fatal(err)
	}
	flag, last, count := "0", before.LastOperation, before.Activations
	var receipt any
	if served {
		receipt = "serve:64"
		flag, last, count = "1", id, count+1
	}
	if row["receipt"] != receipt || row["indexed"] != served || row["ready"] != flag || row["accounts"] != "1=4,10=4" ||
		control.Serving != served || control.LastOperation != last || control.Activations != count || control.Generation != before.Generation ||
		!maps.Equal(control.Cursors, before.Cursors) || !maps.Equal(control.LogicalSafe, before.LogicalSafe) {
		t.Fatalf("served=%v receipt=%v index=%v ready=%v accounts=%v control=%+v", served, row["receipt"], row["indexed"], row["ready"], row["accounts"], control)
	}
}

func TestOmnichainActivationEngineInterruption(t *testing.T) {
	for _, target := range []string{"postgres", "pglite-business", "pglite-sync"} {
		t.Run(target, func(t *testing.T) {
			ctx := t.Context()
			o, opts := liveTestOptions()
			o.Ordering, o.Schema = schema.Omnichain, model(false)
			o.Schema.Tables[0].Indexes = []schema.Index{{Name: "account_balance_idx", Columns: []string{"balance"}}}
			root := t.TempDir()
			o.BusinessDirectory, o.SyncDirectory = filepath.Join(root, "business"), filepath.Join(root, "sync")
			opts.EndBlocks = map[uint64]uint64{1: 4, 10: 4}
			heads := cloneLiveHeads(opts.FinalizedHeads)
			for id, h := range heads {
				opts.FinalizedHeads[id] = liveSegmentsUnitBlock(id, 5, h.Hash).Head
			}
			runtime := ManagedLiveRuntimeOptions{ManagedLiveOptions: opts, StartBlocks: opts.EndBlocks, SourceCheckpoints: true}
			dialect, open := "pglite", OpenManagedLiveRuntimePGlite
			var pg *testpostgres.Server
			var kill func() error
			if target == "postgres" {
				pg = testpostgres.Start(t)
				o.PostgresURL = pg.URL()
				dialect, open = "postgres", OpenManagedLiveRuntimePostgres
				kill = func() error { pg.Kill(t); return nil }
			} else {
				dir := o.BusinessDirectory
				if target == "pglite-sync" {
					dir = o.SyncDirectory
				}
				kill = testpglite.Track(t, dir)
			}
			d, err := open(ctx, o, runtime)
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			defer func() {
				if !closed {
					_ = d.Close(context.Background())
				}
			}()
			before := seedOmnichainActivation(t, ctx, d, heads)
			const operation = "activate-before-engine-stop"
			st := d.live().owner.s
			original := st.Backend
			checkActivationSnapshot(t, ctx, original, st, operation, before, false)
			hit := 0
			st.Backend = &activationCrashBackend{Backend: original, beforeCommit: func(tx Transaction) {
				r, err := tx.Query(ctx, "SELECT id FROM "+st.internal("_ponder_go_live_operations")+" WHERE id=$1", operation)
				if err != nil {
					t.Fatal(err)
				}
				if len(r.Rows) == 0 {
					return
				}
				checkActivationSnapshot(t, ctx, tx, st, operation, before, true)
				if err := kill(); err != nil {
					t.Fatal(err)
				}
				hit++
			}}
			activationErr := d.ActivateRuntime(ctx, operation, before.Cursors)
			st.Backend = original
			committed := target == "pglite-sync"
			if hit != 1 || (activationErr == nil) != committed {
				t.Fatalf("engine interruptions=%d, activation=%v", hit, activationErr)
			}
			if ready, err := d.Ready(ctx); ready {
				t.Fatalf("exposed readiness with an engine offline: %v", err)
			}
			if committed {
				checkActivationSnapshot(t, ctx, original, st, operation, before, true)
			}
			_ = d.Close(ctx) // The interrupted engine may return a shutdown error.
			closed = true
			if pg != nil {
				pg.Restart(t)
				o.PostgresURL = pg.URL()
			}
			raw := liveDomainsRawOpen(t, o, dialect, true)
			if committed {
				// Closing the healthy business owner may clear readiness. Its
				// committed activation receipt must still be present on disk.
				r, err := raw.Query(ctx, "SELECT kind,after_hash FROM "+st.internal("_ponder_go_live_operations")+" WHERE id=$1", operation)
				if err != nil || len(r.Rows) != 1 || r.Rows[0]["kind"] != "serve" || !liveDigest(fmt.Sprint(r.Rows[0]["after_hash"])) {
					t.Fatalf("cold activation receipt=%v err=%v", r.Rows, err)
				}
			} else {
				checkActivationSnapshot(t, ctx, raw, st, operation, before, false)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			runtime.OpenID = "reopen-after-engine-stop"
			next, err := open(ctx, o, runtime)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := next.Close(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			c, err := next.Control(ctx)
			if err != nil || c.Generation != before.Generation+1 || !maps.Equal(c.Cursors, before.Cursors) ||
				!maps.Equal(c.LogicalSafe, before.LogicalSafe) || c.Batches != before.Batches || c.Serving {
				t.Fatalf("reopened control=%+v err=%v", c, err)
			}
			if ready, err := next.Ready(ctx); ready || err != nil {
				t.Fatalf("reopened readiness=%v err=%v", ready, err)
			}
			if err := next.ActivateRuntime(ctx, "activate-reopened", c.Cursors); err != nil {
				t.Fatal(err)
			}
			if ready, err := next.Ready(ctx); !ready || err != nil {
				t.Fatalf("recovered readiness=%v err=%v", ready, err)
			}
			s := next.live().owner.s
			checkActivationSnapshot(t, ctx, s.Backend, s, "activate-reopened", c, true)
		})
	}
}

func seedOmnichainActivation(t *testing.T, ctx context.Context, d *ManagedLiveDomains, heads map[uint64]ManagedLiveHead) ManagedLiveControl {
	t.Helper()
	run, scope, err := d.NewRuntimeScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { scope.Cancel(); _ = scope.Seal(context.Background()) }()
	baseline := ManagedLiveBatch{ID: "baseline", Baselines: heads, InitialCheckpoints: map[uint64]string{}}
	events := ManagedLiveBatch{ID: "source-blocks"}
	for _, id := range []uint64{1, 10} {
		baseline.InitialCheckpoints[id] = checkpoint.MustEncode(checkpoint.Block(100, id, 4, false))
		cp := checkpoint.Block(100, id, 4, true)
		cp.EventType, cp.EventIndex = checkpoint.Blocks, 0
		events.Blocks = append(events.Blocks, ManagedLiveBlock{Head: heads[id], InputDigest: managedHash(cp), SourceCheckpoint: checkpoint.MustEncode(cp)})
	}
	writes := 0
	for _, b := range []ManagedLiveBatch{baseline, events} {
		c, err := d.Control(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b.Generation, b.ExpectedLastOperation, b.ExpectedTail = c.Generation, c.LastOperation, c.Tail
		acquired, err := d.RetainBatch(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.ApplyRuntimeBatch(run, b, acquired, func(ctx context.Context, w *Writer, block ManagedLiveBlock) error {
			cp, err := checkpoint.Decode(block.SourceCheckpoint)
			if err != nil {
				return err
			}
			writes++
			return w.Write(ctx, "account", Row{"id": fmt.Sprint(cp.ChainID), "balance": fmt.Sprint(cp.BlockNumber)}, Insert)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := scope.Seal(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := d.Control(ctx)
	if err != nil || writes != 2 || !maps.Equal(c.Cursors, heads) || len(c.LogicalSafe) != 2 {
		t.Fatalf("seeded writes=%d control=%+v err=%v", writes, c, err)
	}
	return c
}
