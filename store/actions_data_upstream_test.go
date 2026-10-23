package store

import (
	"context"
	"math/big"
	"reflect"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

// These replace the earlier atomic-Apply source mappings; they do not add a
// second credit for the same source records. Every raw write/commit is durable.
func TestUpstreamStagedDataActions(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"finalize()", "revert()", "revert() with composite primary key"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					model := sourceActionsModel()
					if name == "revert() with composite primary key" {
						model.Tables[0] = schema.Table{Name: "Test", Columns: []schema.Column{{Name: "A", Property: "a", Type: "integer", NotNull: true}, {Name: "B", Property: "b", Type: "integer", NotNull: true}, {Name: "C", Property: "c", Type: "integer"}}, PrimaryKey: []string{"A", "B"}}
					}
					a := sourceActions(t, dialect, model, schema.Multichain)
					if name == "finalize()" {
						initial := checkpoint.MustEncode(actionCheckpoint(0))
						if e := a.InitializeCheckpoints(ctx, []CheckpointState{{ChainName: "mainnet", ChainID: 1, SafeCheckpoint: initial, FinalizedCheckpoint: initial, LatestCheckpoint: initial}}); e != nil {
							t.Fatal(e)
						}
					}
					if e := a.CreateTriggers(ctx); e != nil {
						t.Fatal(e)
					}
					write := func(query string, args ...any) {
						t.Helper()
						if e := a.RawBatch(ctx, 1, func(w *Writer) error { _, e := w.SQL(ctx, query, args...); return e }); e != nil {
							t.Fatal(e)
						}
					}
					commit := func(table string, block uint64) {
						t.Helper()
						if e := a.CommitBlock(ctx, table, actionCheckpoint(block)); e != nil {
							t.Fatal(e)
						}
					}
					if name == "revert() with composite primary key" {
						write(`INSERT INTO "Test" ("A","B") VALUES ($1,$2)`, 1, 1)
						commit("Test", 11)
						write(`UPDATE "Test" SET "C"=$1 WHERE "A"=$2 AND "B"=$3`, 1, 1, 1)
						commit("Test", 12)
						if e := a.Revert(ctx, actionCheckpoint(11)); e != nil {
							t.Fatal(e)
						}
						rows, e := a.Select(ctx, "Test")
						if e != nil {
							t.Fatal(e)
						}
						if len(rows) != 1 {
							t.Fatal("source length", rows)
						}
						if !reflect.DeepEqual(rows[0], Row{"a": int64(1), "b": int64(1), "c": nil}) {
							t.Fatal("source composite strict row", rows[0])
						}
						return
					}
					write(`INSERT INTO account (address,balance) VALUES ($1,$2)`, sourceZeroAddress, "10")
					commit("account", 9)
					write(`UPDATE account SET balance=$1 WHERE address=$2`, "88", sourceZeroAddress)
					write(`INSERT INTO account (address) VALUES ($1)`, "0x0000000000000000000000000000000000000001")
					if name == "finalize()" {
						commit("account", 11)
						if e := a.Finalize(ctx, actionCheckpoint(10)); e != nil {
							t.Fatal(e)
						}
						rows, e := a.Journal(ctx, "account")
						if e != nil {
							t.Fatal(e)
						}
						if len(rows) != 2 {
							t.Fatal("source remaining journal length", rows)
						}
						return
					}
					commit("account", 10)
					write(`DELETE FROM account WHERE address=$1`, sourceZeroAddress)
					commit("account", 11)
					if e := a.Revert(ctx, actionCheckpoint(9)); e != nil {
						t.Fatal(e)
					}
					rows, e := a.Select(ctx, "account")
					if e != nil {
						t.Fatal(e)
					}
					if len(rows) != 1 {
						t.Fatal("source length", rows)
					}
					if !reflect.DeepEqual(rows[0], Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}) {
						t.Fatal("source strict row", rows[0])
					}
				})
			}
		})
	}
}
