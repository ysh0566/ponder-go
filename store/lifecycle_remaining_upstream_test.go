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

func camelSourceModel() schema.Schema {
	m := schema.Schema{Tables: []schema.Table{{Name: "accountCc", Columns: []schema.Column{{Name: "addressCc", Property: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint"}}}}}
	fields := []schema.ViewField{}
	for _, column := range m.Tables[0].Columns {
		c := column
		fields = append(fields, schema.ViewField{Name: c.Name, Column: &c})
	}
	m.Views = []schema.View{{Name: "accountViewCc", Fields: fields, Query: `SELECT * FROM "accountCc"`}}
	return m
}
func TestUpstreamRemainingLifecycle(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"migrate() with crash recovery reverts rows", "camelCase"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					b := sourceBackend(t, ctx, dialect)
					ns := fmt.Sprintf("ponder_go_case_%x", time.Now().UnixNano())
					model := sourceActionsModel()
					opts := LifecycleOptions{}
					if name == "camelCase" {
						model = camelSourceModel()
						opts.ViewsSchema = ns + "_viewCc"
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
					if name == "migrate() with crash recovery reverts rows" {
						if e = d.MarkReady(ctx); e != nil {
							t.Fatal(e)
						}
						// Source writes this historical row before live capture. Native
						// HistoricalBatch captures it too, until safe metadata seals it.
						if e = a.HistoricalBatch(ctx, 1, func(w *Writer) error {
							return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert)
						}); e != nil {
							t.Fatal(e)
						}
						if e = a.CommitBlock(ctx, "account", actionCheckpoint(9)); e != nil {
							t.Fatal(e)
						}
						if e = a.CreateTriggers(ctx); e != nil {
							t.Fatal(e)
						}
						if e = a.RawBatch(ctx, 1, func(w *Writer) error {
							return w.Write(ctx, "account", Row{"address": "0x0000000000000000000000000000000000000001", "balance": nil}, Insert)
						}); e != nil {
							t.Fatal(e)
						}
						if e = a.CommitBlock(ctx, "account", actionCheckpoint(11)); e != nil {
							t.Fatal(e)
						}
						cp := checkpoint.MustEncode(actionCheckpoint(10))
						if e = a.InitializeCheckpoints(ctx, []CheckpointState{{ChainID: 1, ChainName: "mainnet", LatestCheckpoint: cp, FinalizedCheckpoint: cp, SafeCheckpoint: cp}}); e != nil {
							t.Fatal(e)
						}
						if e = d.Close(); e != nil {
							t.Fatal(e)
						}
						second, e := NewLifecycle(b, model, ns, schema.Multichain, opts)
						if e != nil {
							t.Fatal(e)
						}
						defer second.Close()
						got, e := second.Migrate(ctx, Migration{BuildID: "abc", Chains: []MigrationChain{{ID: 1, Name: "mainnet"}}, FinalizedBlocks: []FinalizedBlock{{Timestamp: 1, Number: 10, Hash: "0x", ParentHash: "0x"}}})
						if e != nil {
							t.Fatal(e)
						}
						want := []RecoveryCheckpoint{{ChainID: 1, Checkpoint: "000000000000000000000000010000000000000010000000000000000000000000000000000"}}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("source full checkpoint snapshot: %#v want %#v", got, want)
						}
						rows, e := second.Actions().Select(ctx, "account")
						if e != nil {
							t.Fatal(e)
						}
						if len(rows) != 1 {
							t.Fatalf("source rows length=1: %#v", rows)
						}
						if rows[0]["address"] != sourceZeroAddress {
							t.Fatalf("source baseline address: %#v", rows[0])
						}
						metadata, e := second.Metadata(ctx)
						if e != nil || len(metadata) != 1 {
							t.Fatalf("source metadata length=1: %#v %v", metadata, e)
						}
						return
					}
					for _, relation := range []string{"accountCc", "_ponder_go_journal", "_ponder_go_app"} {
						r, e := b.Query(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema=$1 AND table_type='BASE TABLE' AND table_name=$2", ns, relation)
						if e != nil || len(r.Rows) != 1 {
							t.Fatalf("source camelCase table containment (%s): %#v %v", relation, r, e)
						}
					}
					metadata, e := d.Metadata(ctx)
					if e != nil || len(metadata) != 1 {
						t.Fatalf("source metadata length=1: %#v %v", metadata, e)
					}
					if e = a.CreateTriggers(ctx); e != nil {
						t.Fatal(e)
					}
					if e = a.CreateIndexes(ctx); e != nil {
						t.Fatal(e)
					}
					if e = a.CreateViews(ctx); e != nil {
						t.Fatal(e)
					}
					if e = a.CommitBlock(ctx, "accountCc", actionCheckpoint(10)); e != nil {
						t.Fatal(e)
					}
					if e = a.DropTriggers(ctx); e != nil {
						t.Fatal(e)
					}
					if e = a.CreateLiveQueryProcedures(ctx); e != nil {
						t.Fatal(e)
					}
					if e = a.CreateLiveQueryTriggers(ctx, []string{"accountCc"}); e != nil {
						t.Fatal(e)
					}
					if e = a.DropLiveQueryTriggers(ctx, []string{"accountCc"}); e != nil {
						t.Fatal(e)
					}
					if e = a.Revert(ctx, actionCheckpoint(10)); e != nil {
						t.Fatal(e)
					}
					if e = a.Finalize(ctx, actionCheckpoint(10)); e != nil {
						t.Fatal(e)
					}
					if e = a.CrashRecovery(ctx); e != nil {
						t.Fatal(e)
					}
				})
			}
		})
	}
}
