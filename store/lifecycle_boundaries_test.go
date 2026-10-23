package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

type migrationFaultBackend struct {
	Backend
	stage     string
	terminate func()
}
type migrationFaultTx struct {
	Transaction
	fault *migrationFaultBackend
}

func (b *migrationFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &migrationFaultTx{Transaction: tx, fault: b}, nil
}

var errMigrationFault = errors.New("injected migration persistence failure")

func (tx *migrationFaultTx) trip() error {
	if tx.fault.terminate != nil {
		tx.fault.terminate()
	}
	return errMigrationFault
}
func (tx *migrationFaultTx) Exec(ctx context.Context, sql string, args ...any) error {
	if e := tx.Transaction.Exec(ctx, sql, args...); e != nil {
		return e
	}
	if tx.fault.stage == "slow_ddl" && strings.HasPrefix(sql, "CREATE OR REPLACE FUNCTION ") && strings.Contains(sql, `."live_query_notify"`) {
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if tx.fault.stage == "during_undo" && strings.HasPrefix(sql, "DELETE FROM ") && strings.Contains(sql, `."account" WHERE`) {
		return tx.trip()
	}
	if tx.fault.stage == "before_commit" && strings.HasPrefix(sql, "INSERT INTO ") && strings.Contains(sql, `."_ponder_go_app"`) {
		return tx.trip()
	}
	return nil
}
func (tx *migrationFaultTx) Commit(ctx context.Context) error {
	if e := tx.Transaction.Commit(ctx); e != nil {
		return e
	}
	if tx.fault.stage == "after_commit" {
		return tx.trip()
	}
	return nil
}

func newTestLifecycle(t *testing.T, b Backend, ns string, opts LifecycleOptions) *Lifecycle {
	t.Helper()
	model := sourceActionsModel()
	model.Tables[0].Indexes = []schema.Index{{Name: "account_balance_index", Columns: []string{"balance"}}}
	d, e := NewLifecycle(b, model, ns, schema.Multichain, opts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
func lifecycleMigration() Migration {
	return Migration{BuildID: "abc", Chains: []MigrationChain{{ID: 1, Name: "mainnet"}}, FinalizedBlocks: []FinalizedBlock{{Number: 10, Timestamp: 10, Hash: "0x", ParentHash: "0x"}}}
}
func seedLifecycleRecovery(t *testing.T, d *Lifecycle) {
	t.Helper()
	ctx := context.Background()
	if _, e := d.Migrate(ctx, Migration{BuildID: "abc"}); e != nil {
		t.Fatal(e)
	}
	a := d.Actions()
	zero := checkpoint.MustEncode(actionCheckpoint(0))
	if e := a.InitializeCheckpoints(ctx, []CheckpointState{{ChainID: 1, ChainName: "mainnet", LatestCheckpoint: zero, FinalizedCheckpoint: zero, SafeCheckpoint: zero}}); e != nil {
		t.Fatal(e)
	}
	if e := a.CreateTriggers(ctx); e != nil {
		t.Fatal(e)
	}
	if e := a.CreateIndexes(ctx); e != nil {
		t.Fatal(e)
	}
	if e := a.CreateLiveQueryTriggers(ctx, []string{"account"}); e != nil {
		t.Fatal(e)
	}
	if e := a.RawBatch(ctx, 1, func(w *Writer) error {
		if e := w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert); e != nil {
			return e
		}
		if e := w.CommitBlock(ctx, "account", actionCheckpoint(1)); e != nil {
			return e
		}
		return w.AdvanceLatest(ctx, actionCheckpoint(1))
	}); e != nil {
		t.Fatal(e)
	}
	if e := a.Finalize(ctx, actionCheckpoint(1)); e != nil {
		t.Fatal(e)
	}
	if e := a.RawBatch(ctx, 1, func(w *Writer) error {
		if e := w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "99"}, Update); e != nil {
			return e
		}
		if e := w.CommitBlock(ctx, "account", actionCheckpoint(2)); e != nil {
			return e
		}
		return w.AdvanceLatest(ctx, actionCheckpoint(2))
	}); e != nil {
		t.Fatal(e)
	}
	// Include a durable MAX record as well as an assigned live record.
	if e := a.RawBatch(ctx, 1, func(w *Writer) error {
		return w.Write(ctx, "account", Row{"address": "0x0000000000000000000000000000000000000001", "balance": nil}, Insert)
	}); e != nil {
		t.Fatal(e)
	}
	if e := d.MarkReady(ctx); e != nil {
		t.Fatal(e)
	}
	if e := d.Close(); e != nil {
		t.Fatal(e)
	}
}

type lifecycleSnapshot struct{ Data, Journal, Checkpoints, Metadata, Indexes, Triggers Result }

func snapshotLifecycle(t *testing.T, d *Lifecycle) lifecycleSnapshot {
	t.Helper()
	ctx := context.Background()
	query := func(sql string, args ...any) Result {
		r, e := d.s.Backend.Query(ctx, sql, args...)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	return lifecycleSnapshot{
		Data:        query("SELECT address,balance::text AS balance FROM " + d.s.table("account") + " ORDER BY address"),
		Journal:     query("SELECT * FROM " + d.s.internal("_ponder_go_journal") + " ORDER BY operation_id"),
		Checkpoints: query("SELECT * FROM " + d.s.internal("_ponder_go_progress")),
		Metadata:    query("SELECT value::text AS value,lease_token FROM " + d.s.internal("_ponder_go_app")),
		Indexes:     query("SELECT indexname FROM pg_indexes WHERE schemaname=$1 AND tablename='account' ORDER BY indexname", d.s.Namespace),
		Triggers:    query("SELECT tgname FROM pg_trigger WHERE tgrelid=$1::regclass AND NOT tgisinternal ORDER BY tgname", d.s.table("account")),
	}
}
func TestLifecycleBoundaries(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"lease takeover fences all writers", "failed recovery restores data and schema", "failed first migration leaves no schemas", "finalized block regression and missing chain", "canceled waiter preserves active owner", "slow startup publishes a fresh lease"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					b := sourceBackend(t, ctx, dialect)
					ns := fmt.Sprintf("ponder_go_life_guard_%x", time.Now().UnixNano())
					opts := LifecycleOptions{}
					if name == "lease takeover fences all writers" {
						opts = LifecycleOptions{HeartbeatInterval: 5 * time.Second, HeartbeatTimeout: 200 * time.Millisecond}
					}
					d := newTestLifecycle(t, b, ns, opts)
					switch name {
					case "slow startup publishes a fresh lease":
						slow := newTestLifecycle(t, &migrationFaultBackend{Backend: b, stage: "slow_ddl"}, ns, LifecycleOptions{HeartbeatInterval: 25 * time.Millisecond, HeartbeatTimeout: 100 * time.Millisecond})
						if _, e := slow.Migrate(ctx, Migration{BuildID: "abc"}); e != nil {
							t.Fatal(e)
						}
						if e := slow.Actions().CreateTriggers(ctx); e != nil {
							t.Fatalf("startup returned expired lease: %v", e)
						}
					case "lease takeover fences all writers":
						if _, e := d.Migrate(ctx, Migration{BuildID: "abc"}); e != nil {
							t.Fatal(e)
						}
						second := newTestLifecycle(t, b, ns, LifecycleOptions{HeartbeatInterval: 25 * time.Millisecond, HeartbeatTimeout: opts.HeartbeatTimeout})
						// Acquisition compares the new owner's configured timeout, as the
						// source does, so both contenders use the same expiration window.
						second.opts.HeartbeatTimeout = opts.HeartbeatTimeout
						second.s.leaseTimeout = opts.HeartbeatTimeout.Milliseconds()
						if _, e := second.Migrate(ctx, Migration{BuildID: "abc"}); e != nil {
							t.Fatal(e)
						}
						entered := false
						checks := []func() error{
							func() error { return d.Actions().RawBatch(ctx, 1, func(*Writer) error { entered = true; return nil }) },
							func() error { return d.Actions().CreateTriggers(ctx) }, func() error { return d.Actions().DropTriggers(ctx) },
							func() error { return d.Actions().CreateIndexes(ctx) }, func() error { return d.Actions().CreateViews(ctx) },
							func() error { return d.Actions().CommitBlock(ctx, "account", actionCheckpoint(1)) }, func() error { return d.Actions().AdvanceLatest(ctx, actionCheckpoint(1)) },
							func() error { return d.Actions().Finalize(ctx, actionCheckpoint(1)) }, func() error { return d.Actions().Revert(ctx, actionCheckpoint(1)) }, func() error { return d.Actions().CrashRecovery(ctx) },
							func() error { return d.MarkReady(ctx) }, func() error { return d.Heartbeat(ctx) },
							func() error {
								return d.s.SaveBlock(ctx, SyncBlock{ChainID: 1, Number: 1, Payload: json.RawMessage(`{}`)})
							},
							func() error {
								return d.s.CompleteBlock(ctx, SyncBlock{ChainID: 1, Number: 1, Payload: json.RawMessage(`{}`)}, "test")
							},
							func() error { return d.s.RecordFactoryChildren(ctx, "factory", 1, 1, "0x", nil) }, func() error { return d.s.RewindSync(ctx, 1, 0) },
							func() error { return d.s.CachePut(ctx, "rpc", json.RawMessage(`{}`)) },
						}
						for i, check := range checks {
							if e := check(); !errors.Is(e, ErrLeaseLost) {
								t.Fatalf("old owner writer %d was not fenced: %v", i, e)
							}
						}
						if entered {
							t.Fatal("stale handler was invoked")
						}
						if e := d.Close(); e != nil {
							t.Fatal(e)
						}
						metadata, e := second.Metadata(ctx)
						if e != nil || metadata[0].Value.IsLocked != 1 {
							t.Fatalf("old close unlocked new owner: %#v %v", metadata, e)
						}
						if _, e := NewActions(ctx, b, d.s.Schema, ns, schema.Multichain); e == nil {
							t.Fatal("standalone API adopted managed schema")
						}
					case "failed recovery restores data and schema":
						seedLifecycleRecovery(t, d)
						before := snapshotLifecycle(t, d)
						for _, stage := range []string{"during_undo", "before_commit"} {
							failed := newTestLifecycle(t, &migrationFaultBackend{Backend: b, stage: stage}, ns, opts)
							if _, e := failed.Migrate(ctx, lifecycleMigration()); !errors.Is(e, errMigrationFault) {
								t.Fatalf("missing injected failure at %s: %v", stage, e)
							}
							if after := snapshotLifecycle(t, d); !reflect.DeepEqual(before, after) {
								t.Fatalf("failure at %s changed durable state: before %+v after %+v", stage, before, after)
							}
						}
						second := newTestLifecycle(t, b, ns, opts)
						recovery, e := second.Migrate(ctx, lifecycleMigration())
						if e != nil {
							t.Fatal(e)
						}
						if !reflect.DeepEqual(recovery, []RecoveryCheckpoint{{ChainID: 1, Checkpoint: checkpoint.MustEncode(actionCheckpoint(1))}}) {
							t.Fatalf("safe recovery checkpoint: %#v", recovery)
						}
						after := snapshotLifecycle(t, second)
						if len(after.Data.Rows) != 1 || after.Data.Rows[0]["balance"] != "10" || len(after.Journal.Rows) != 0 || len(after.Indexes.Rows) != 1 || len(after.Triggers.Rows) != 2 {
							t.Fatalf("recovery result: %+v", after)
						}
						if !reflect.DeepEqual(after.Checkpoints, before.Checkpoints) {
							t.Fatal("migration reset latest metadata before runtime")
						}
						if e := second.Actions().RawBatch(ctx, 1, func(w *Writer) error {
							return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "77"}, Update)
						}); e == nil {
							t.Fatal("recovered capture-free schema allowed business write")
						}
						if e := second.Actions().CreateTriggers(ctx); e != nil {
							t.Fatal(e)
						}
						if e := second.Actions().AdvanceLatest(ctx, actionCheckpoint(1)); e != nil {
							t.Fatal(e)
						}
						if e := second.Actions().RawBatch(ctx, 1, func(w *Writer) error {
							if e := w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "77"}, Update); e != nil {
								return e
							}
							return w.CommitBlock(ctx, "account", actionCheckpoint(2))
						}); e != nil {
							t.Fatal(e)
						}
					case "failed first migration leaves no schemas":
						failed := newTestLifecycle(t, &migrationFaultBackend{Backend: b, stage: "before_commit"}, ns, opts)
						if _, e := failed.Migrate(ctx, Migration{BuildID: "abc"}); !errors.Is(e, errMigrationFault) {
							t.Fatal(e)
						}
						r, e := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1 OR nspname=$2", ns, ns+"_sync")
						if e != nil || len(r.Rows) != 0 {
							t.Fatalf("partial startup schema leaked: %#v %v", r, e)
						}
						if _, e := d.Migrate(ctx, Migration{BuildID: "abc"}); e != nil {
							t.Fatal(e)
						}
					case "finalized block regression and missing chain":
						seedLifecycleRecovery(t, d)
						final := checkpoint.MustEncode(checkpoint.Checkpoint{ChainID: 1, BlockTimestamp: 11, BlockNumber: 1})
						if e := b.Exec(ctx, "UPDATE "+d.s.internal("_ponder_go_finalized")+" SET checkpoint=$1", final); e != nil {
							t.Fatal(e)
						}
						before := snapshotLifecycle(t, d)
						for _, request := range []Migration{lifecycleMigration(), {BuildID: "abc"}} {
							second := newTestLifecycle(t, b, ns, opts)
							if _, e := second.Migrate(ctx, request); e == nil {
								t.Fatal("invalid finalized recovery accepted")
							}
							if !reflect.DeepEqual(before, snapshotLifecycle(t, d)) {
								t.Fatal("failed precondition mutated state")
							}
						}
					case "canceled waiter preserves active owner":
						if _, e := d.Migrate(ctx, Migration{BuildID: "abc"}); e != nil {
							t.Fatal(e)
						}
						before := snapshotLifecycle(t, d)
						second := newTestLifecycle(t, b, ns, opts)
						deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
						defer cancel()
						if _, e := second.Migrate(deadline, Migration{BuildID: "abc"}); !errors.Is(e, context.DeadlineExceeded) {
							t.Fatalf("wait cancellation: %v", e)
						}
						if !reflect.DeepEqual(before, snapshotLifecycle(t, d)) {
							t.Fatal("canceled waiter changed active owner")
						}
					}
				})
			}
		})
	}
}
