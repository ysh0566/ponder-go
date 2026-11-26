package store

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/internal/testpglite"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/schema"
)

// The hooks observe successful statements and pending commits on a real transaction.
type activationCrashBackend struct {
	Backend
	beforeCommit func(Transaction)
	afterExec    func(Transaction, string)
}

func (b *activationCrashBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &activationCrashTx{Transaction: tx, backend: b}, nil
}

type activationCrashTx struct {
	Transaction
	backend *activationCrashBackend
}

func (tx *activationCrashTx) Exec(ctx context.Context, query string, args ...any) error {
	if err := tx.Transaction.Exec(ctx, query, args...); err != nil {
		return err
	}
	if tx.backend.afterExec != nil {
		tx.backend.afterExec(tx.Transaction, query)
	}
	return nil
}

func (tx *activationCrashTx) Commit(ctx context.Context) error {
	if tx.backend.beforeCommit != nil {
		tx.backend.beforeCommit(tx.Transaction)
	}
	return tx.Transaction.Commit(ctx)
}

// Read the business tables without opening a managed owner, which would start
// a new generation and reset readiness before we could inspect the crash.
func checkActivationSnapshot(t *testing.T, ctx context.Context, q Querier, s *Store, id string, before ManagedLiveControl, served bool) ManagedLiveControl {
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
		!maps.Equal(control.Cursors, before.Cursors) || !maps.Equal(control.LogicalCursors, before.LogicalCursors) || !maps.Equal(control.LogicalSafe, before.LogicalSafe) {
		t.Fatalf("served=%v receipt=%v index=%v ready=%v accounts=%v control=%+v", served, row["receipt"], row["indexed"], row["ready"], row["accounts"], control)
	}
	return control
}

type activationInterruption int

const (
	beforeBlockCommit activationInterruption = iota
	afterFirstLogPage
	beforeLogCommit
)

func TestOmnichainActivationEngineInterruption(t *testing.T) {
	testOmnichainActivationCrash(t, beforeBlockCommit)
}

func TestOmnichainLogOnlyActivationEngineInterruption(t *testing.T) {
	t.Run("first-page", func(t *testing.T) { testOmnichainActivationCrash(t, afterFirstLogPage) })
	t.Run("before-commit", func(t *testing.T) { testOmnichainActivationCrash(t, beforeLogCommit) })
}

func testOmnichainActivationCrash(t *testing.T, point activationInterruption) {
	t.Helper()
	logOnly := point != beforeBlockCommit
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
			var get ManagedLiveHeaderReader
			if logOnly {
				get = configureActivationLogRange(&runtime, heads)
			}
			activate := func(d *ManagedLiveDomains, id string, c ManagedLiveControl) error {
				if logOnly {
					return d.ActivatePagedLogOnlyRuntime(ctx, id, c.Cursors, get)
				}
				return d.ActivateRuntime(ctx, id, c.Cursors)
			}
			checkSnapshot := func(q Querier, s *Store, id string, prior ManagedLiveControl, served bool) {
				t.Helper()
				c := checkActivationSnapshot(t, ctx, q, s, id, prior, served)
				if logOnly {
					checkActivationLogPages(t, ctx, q, s, id, served, get)
					checkActivationScanHeads(t, c, served, get)
				}
			}
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
			before := seedOmnichainActivation(t, ctx, d, heads, get)
			const operation = "activate-before-engine-stop"
			st := d.live().owner.s
			original := st.Backend
			checkSnapshot(original, st, operation, before, false)
			hit := 0
			interrupt := func() {
				t.Helper()
				if err := kill(); err != nil {
					t.Fatal(err)
				}
				hit++
			}
			hooks := &activationCrashBackend{Backend: original}
			if point == afterFirstLogPage {
				hooks.afterExec = func(tx Transaction, query string) {
					if hit != 0 || !strings.HasPrefix(query, "INSERT INTO "+st.internal("_ponder_go_live_lineage_pages")+" ") {
						return
					}
					r, err := tx.Query(ctx, "SELECT first_block::text AS height,jsonb_array_length(headers)::text AS size FROM "+st.internal("_ponder_go_live_lineage_pages")+" WHERE operation_id=$1", operation)
					if err != nil || len(r.Rows) != 1 || r.Rows[0]["height"] != "4" || r.Rows[0]["size"] != fmt.Sprint(lineagePageSize) {
						t.Fatalf("pending first scan page: %v %v", r.Rows, err)
					}
					checkActivationSnapshot(t, ctx, tx, st, operation, before, false)
					interrupt()
				}
			} else {
				hooks.beforeCommit = func(tx Transaction) {
					r, err := tx.Query(ctx, "SELECT id FROM "+st.internal("_ponder_go_live_operations")+" WHERE id=$1", operation)
					if err != nil {
						t.Fatal(err)
					}
					if len(r.Rows) == 0 {
						return
					}
					checkSnapshot(tx, st, operation, before, true)
					interrupt()
				}
			}
			st.Backend = hooks
			activationErr := activate(d, operation, before)
			st.Backend = original
			committed := target == "pglite-sync"
			if hit != 1 || (activationErr == nil) != committed {
				t.Fatalf("engine interruptions=%d, activation=%v", hit, activationErr)
			}
			if ready, err := d.Ready(ctx); ready {
				t.Fatalf("exposed readiness with an engine offline: %v", err)
			}
			if committed {
				checkSnapshot(original, st, operation, before, true)
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
				if logOnly {
					checkActivationLogPages(t, ctx, raw, st, operation, true, get)
				}
			} else {
				checkSnapshot(raw, st, operation, before, false)
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
				!maps.Equal(c.LogicalCursors, before.LogicalCursors) || !maps.Equal(c.LogicalSafe, before.LogicalSafe) || c.Batches != before.Batches || c.Serving {
				t.Fatalf("reopened control=%+v err=%v", c, err)
			}
			if logOnly {
				checkActivationScanHeads(t, c, committed, get)
			}
			if ready, err := next.Ready(ctx); ready || err != nil {
				t.Fatalf("reopened readiness=%v err=%v", ready, err)
			}
			if err := activate(next, "activate-reopened", c); err != nil {
				t.Fatal(err)
			}
			if ready, err := next.Ready(ctx); !ready || err != nil {
				t.Fatalf("recovered readiness=%v err=%v", ready, err)
			}
			s := next.live().owner.s
			checkSnapshot(s.Backend, s, "activate-reopened", c, true)
		})
	}
}

func seedOmnichainActivation(t *testing.T, ctx context.Context, d *ManagedLiveDomains, heads map[uint64]ManagedLiveHead, get ManagedLiveHeaderReader) ManagedLiveControl {
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
		block := ManagedLiveBlock{Head: heads[id]}
		if get != nil {
			cp.TransactionIndex, cp.EventType = 0, checkpoint.Logs
			block.LogEventCount = 1
			block.LogFirstEvent = checkpoint.MustEncode(cp)
			block.LogLastEvent = block.LogFirstEvent
			err := d.WithRuntimeSync(run, func(ctx context.Context, source *SourceSyncStore) error {
				f := d.live().opts.runtimeLogFilters[id][0]
				return source.InsertIntervals(ctx, id, []SourceFilterInterval{{Filter: f, Interval: SourceInterval{4, activationLogEnd}}}, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		block.SourceCheckpoint, block.InputDigest = checkpoint.MustEncode(cp), managedHash(cp)
		events.Blocks = append(events.Blocks, block)
	}
	writes := 0
	for _, b := range []ManagedLiveBatch{baseline, events} {
		c, err := d.Control(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b.Generation, b.ExpectedLastOperation, b.ExpectedTail = c.Generation, c.LastOperation, c.Tail
		var acquired *ManagedLiveAcquisition
		if get != nil && len(b.Blocks) > 0 {
			acquired, err = d.RetainPagedLogOnlyBatch(ctx, b, get)
		} else {
			acquired, err = d.RetainBatch(ctx, b)
		}
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
	for _, block := range events.Blocks {
		cp, err := checkpoint.Decode(block.SourceCheckpoint)
		if err != nil || c.LogicalCursors[cp.ChainID] != block.SourceCheckpoint || c.LogicalSafe[cp.ChainID] != block.SourceCheckpoint {
			t.Fatalf("seeded source checkpoint %s: cursors=%v safe=%v err=%v", block.SourceCheckpoint, c.LogicalCursors, c.LogicalSafe, err)
		}
	}
	return c
}
