package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

func initialState(chain, block uint64, name string) CheckpointState {
	cp := checkpoint.MustEncode(checkpoint.Checkpoint{ChainID: chain, BlockNumber: block})
	return CheckpointState{ChainID: chain, ChainName: name, LatestCheckpoint: cp, FinalizedCheckpoint: cp, SafeCheckpoint: cp}
}

func TestHistoricalSafeSealing(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, order := range []schema.Ordering{schema.Multichain, schema.Isolated} {
			t.Run(dialect+"/"+string(order), func(t *testing.T) {
				ctx := context.Background()
				model := sourceActionsModel()
				if order == schema.Isolated {
					model.Tables[0].Columns[0].Primary = false
					model.Tables[0].Columns = append([]schema.Column{{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}}, model.Tables[0].Columns...)
					model.Tables[0].PrimaryKey = []string{"chain_id", "address"}
				}
				a := sourceActions(t, dialect, model, order)
				for _, op := range []struct {
					chain, block uint64
					address      string
					balance      string
				}{{1, 9, sourceZeroAddress, "10"}, {0, 0, "0x0000000000000000000000000000000000000001", "20"}, {1, 9, "0x0000000000000000000000000000000000000002", "30"}} {
					row := Row{"address": op.address, "balance": op.balance}
					if order == schema.Isolated {
						row["chain_id"] = int(op.chain)
					}
					if e := a.HistoricalBatch(ctx, op.chain, func(w *Writer) error { return w.Write(ctx, "account", row, Insert) }); e != nil {
						t.Fatal(e)
					}
					if e := a.CommitBlock(ctx, "account", checkpoint.Checkpoint{ChainID: op.chain, BlockNumber: op.block}); e != nil {
						t.Fatal(e)
					}
				}
				if e := a.InitializeCheckpoints(ctx, []CheckpointState{initialState(1, 10, "mainnet")}); order == schema.Multichain {
					if e == nil {
						t.Fatal("safe claim crossed an unknown chain's global suffix")
					}
					states, e := a.Checkpoints(ctx)
					if e != nil || len(states) != 0 {
						t.Fatalf("failed safe initialization published metadata: %#v %v", states, e)
					}
					journal, e := a.Journal(ctx, "account")
					if e != nil || len(journal) != 3 {
						t.Fatalf("failed initialization discarded journal: %#v %v", journal, e)
					}
					if e := a.InitializeCheckpoints(ctx, []CheckpointState{initialState(1, 10, "mainnet"), initialState(0, 0, "zero")}); e != nil {
						t.Fatal(e)
					}
				} else {
					if e != nil {
						t.Fatal(e)
					}
					journal, e := a.Journal(ctx, "account")
					if e != nil || len(journal) != 1 || journal[0]["address"] != "0x0000000000000000000000000000000000000001" {
						t.Fatalf("isolated safe prefix affected another chain: %#v %v", journal, e)
					}
					if e := a.InitializeCheckpoints(ctx, []CheckpointState{initialState(0, 0, "zero")}); e != nil {
						t.Fatal(e)
					}
				}
				journal, e := a.Journal(ctx, "account")
				if e != nil || len(journal) != 0 {
					t.Fatalf("confirmed historical journal retained: %#v %v", journal, e)
				}
				if e := a.CrashRecovery(ctx); e != nil {
					t.Fatal(e)
				}
				rows, e := a.Select(ctx, "account")
				if e != nil || len(rows) != 3 {
					t.Fatalf("confirmed baseline was undone: %#v %v", rows, e)
				}
			})
		}
	}
}

func TestViewsSchemaBoundaries(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"alias reads and independent notifications", "unowned alias rollback and configuration guard"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					b := sourceBackend(t, ctx, dialect)
					ns := fmt.Sprintf("ponder_go_alias_%x", time.Now().UnixNano())
					alias := ns + "_viewCc"
					opts := LifecycleOptions{ViewsSchema: alias}
					model := camelSourceModel()
					if name == "unowned alias rollback and configuration guard" {
						if _, e := NewLifecycle(b, model, ns, schema.Multichain, LifecycleOptions{ViewsSchema: "public"}); e == nil {
							t.Fatal("unrelated schema accepted as views namespace")
						}
						if e := b.Exec(ctx, "CREATE SCHEMA "+schema.Quote(alias)); e != nil {
							t.Fatal(e)
						}
						if e := b.Exec(ctx, "CREATE VIEW "+schema.Quote(alias)+".unrelated AS SELECT 42 AS value"); e != nil {
							t.Fatal(e)
						}
						failed, e := NewLifecycle(b, model, ns, schema.Multichain, opts)
						if e != nil {
							t.Fatal(e)
						}
						if _, e = failed.Migrate(ctx, Migration{BuildID: "abc"}); e == nil {
							t.Fatal("unowned alias adopted")
						}
						r, e := b.Query(ctx, "SELECT value FROM "+schema.Quote(alias)+".unrelated")
						if e != nil || fmt.Sprint(r.Rows[0]["value"]) != "42" {
							t.Fatal("unowned alias changed", r, e)
						}
						r, e = b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1 OR nspname=$2", ns, ns+"_sync")
						if e != nil || len(r.Rows) != 0 {
							t.Fatalf("failed alias startup leaked indexing schema: %#v %v", r, e)
						}
						if e = b.Exec(ctx, "DROP VIEW "+schema.Quote(alias)+".unrelated"); e != nil {
							t.Fatal(e)
						}
						if e = b.Exec(ctx, "DROP SCHEMA "+schema.Quote(alias)); e != nil {
							t.Fatal(e)
						}
					}
					d, e := NewLifecycle(b, model, ns, schema.Multichain, opts)
					if e != nil {
						t.Fatal(e)
					}
					defer d.Close()
					if _, e = d.Migrate(ctx, Migration{BuildID: "abc"}); e != nil {
						t.Fatal(e)
					}
					a := d.Actions()
					if name == "unowned alias rollback and configuration guard" {
						if e = d.Close(); e != nil {
							t.Fatal(e)
						}
						other, e := NewLifecycle(b, model, ns, schema.Multichain, LifecycleOptions{ViewsSchema: ns + "_other"})
						if e != nil {
							t.Fatal(e)
						}
						if _, e = other.Migrate(ctx, Migration{BuildID: "abc"}); e == nil {
							t.Fatal("changed alias configuration reused an old build")
						}
						return
					}
					if e = a.CreateTriggers(ctx); e != nil {
						t.Fatal(e)
					}
					if e = a.CreateLiveQueryTriggers(ctx, []string{"accountCc"}); e != nil {
						t.Fatal(e)
					}
					if e = a.InitializeCheckpoints(ctx, []CheckpointState{initialState(1, 0, "mainnet")}); e != nil {
						t.Fatal(e)
					}
					base, e := Listen(ctx, b, a.LiveQueryChannel())
					if e != nil {
						t.Fatal(e)
					}
					defer base.Close()
					view, e := Listen(ctx, b, a.ViewsLiveQueryChannel())
					if e != nil {
						t.Fatal(e)
					}
					defer view.Close()
					write := func(block uint64, value string) error {
						return a.RawBatch(ctx, 1, func(w *Writer) error {
							if _, e := w.SQL(ctx, `INSERT INTO "accountCc" ("addressCc",balance) VALUES($1,$2) ON CONFLICT("addressCc") DO UPDATE SET balance=excluded.balance`, sourceZeroAddress, value); e != nil {
								return e
							}
							if e := w.CommitBlock(ctx, "accountCc", actionCheckpoint(block)); e != nil {
								return e
							}
							return w.AdvanceLatest(ctx, actionCheckpoint(block))
						})
					}
					if e = write(1, "10"); e != nil {
						t.Fatal(e)
					}
					waitNotification(t, base, a.LiveQueryChannel(), `["accountCc"]`)
					waitNotification(t, view, a.ViewsLiveQueryChannel(), `["accountCc"]`)
					r, e := b.Query(ctx, "SELECT \"addressCc\",balance::text AS balance FROM "+schema.Quote(alias)+`."accountViewCc"`)
					if e != nil || !reflect.DeepEqual(r.Rows, []map[string]any{{"addressCc": sourceZeroAddress, "balance": "10"}}) {
						t.Fatalf("alias camelCase row: %#v %v", r, e)
					}
					r, e = b.Query(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name='_ponder_meta' ORDER BY ordinal_position", alias)
					if e != nil || len(r.Rows) != 2 || r.Rows[0]["column_name"] != "key" || r.Rows[1]["column_name"] != "value" {
						t.Fatalf("alias exposed internal fencing token: %#v %v", r, e)
					}
					if e = a.DropLiveQueryTriggers(ctx, []string{"accountCc"}); e != nil {
						t.Fatal(e)
					}
					if e = write(2, "20"); e != nil {
						t.Fatal(e)
					}
					expectNoNotification(t, base)
					waitNotification(t, view, a.ViewsLiveQueryChannel(), `[]`)
					if e = d.Close(); e != nil {
						t.Fatal(e)
					}
					reopened, e := NewLifecycle(b, model, ns, schema.Multichain, opts)
					if e != nil {
						t.Fatal(e)
					}
					defer reopened.Close()
					if _, e = reopened.Migrate(ctx, lifecycleMigration()); e != nil {
						t.Fatal(e)
					}
					states, e := reopened.Actions().Checkpoints(ctx)
					if e != nil || states[0].LatestCheckpoint != checkpoint.MustEncode(actionCheckpoint(2)) {
						t.Fatal("alias recovery changed latest", states, e)
					}
				})
			}
		})
	}
}
