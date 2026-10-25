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

func sourceActionsModel() schema.Schema {
	return schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint"}}}}}
}
func sourceActions(t *testing.T, dialect string, model schema.Schema, ordering schema.Ordering) *Actions {
	t.Helper()
	ctx := context.Background()
	b := sourceBackend(t, ctx, dialect)
	a, e := NewActions(ctx, b, model, fmt.Sprintf("ponder_go_actions_%x", time.Now().UnixNano()), ordering)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func expectSourceJournal(t *testing.T, a *Actions, want []Row) {
	t.Helper()
	got, e := a.Journal(context.Background(), "account")
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source journal strict/snapshot assertion:\ngot %#v\nwant %#v", got, want)
	}
}
func TestUpstreamStagedActions(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"createTriggers()", "createTriggers() duplicate", "commitBlock()", "commitBlock() isolated", "empty schema", "finalizeMultichain() with empty schema only updates matching chain"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					model := sourceActionsModel()
					ordering := schema.Multichain
					if name == "empty schema" || name == "finalizeMultichain() with empty schema only updates matching chain" {
						model = schema.Schema{}
					}
					if name == "commitBlock() isolated" {
						ordering = schema.Isolated
						model.Tables[0] = schema.Table{Name: "account", Columns: []schema.Column{{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "address", Type: "hex", NotNull: true}, {Name: "balance", Type: "bigint"}}, PrimaryKey: []string{"chain_id", "address"}}
					}
					a := sourceActions(t, dialect, model, ordering)
					if name == "finalizeMultichain() with empty schema only updates matching chain" {
						mainnet := checkpoint.MustEncode(checkpoint.Checkpoint{ChainID: 1, BlockNumber: 1})
						optimism := checkpoint.MustEncode(checkpoint.Checkpoint{ChainID: 10, BlockNumber: 1})
						finalized := checkpoint.MustEncode(checkpoint.Checkpoint{ChainID: 1, BlockNumber: 10})
						if e := a.InitializeCheckpoints(ctx, []CheckpointState{{ChainName: "mainnet", ChainID: 1, SafeCheckpoint: mainnet, FinalizedCheckpoint: mainnet, LatestCheckpoint: mainnet}, {ChainName: "optimism", ChainID: 10, SafeCheckpoint: optimism, FinalizedCheckpoint: optimism, LatestCheckpoint: optimism}}); e != nil {
							t.Fatal(e)
						}
						if e := a.Finalize(ctx, checkpoint.Checkpoint{ChainID: 1, BlockNumber: 10}); e != nil {
							t.Fatal(e)
						}
						got, e := a.Checkpoints(ctx)
						if e != nil {
							t.Fatal(e)
						}
						want := []CheckpointState{{ChainName: "mainnet", ChainID: 1, SafeCheckpoint: finalized, LatestCheckpoint: mainnet, FinalizedCheckpoint: finalized}, {ChainName: "optimism", ChainID: 10, SafeCheckpoint: optimism, LatestCheckpoint: optimism, FinalizedCheckpoint: optimism}}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("source matching-chain strict assertion: %#v want %#v", got, want)
						}
						return
					}
					if e := a.CreateTriggers(ctx); e != nil {
						t.Fatal(e)
					}
					if name == "createTriggers() duplicate" {
						if e := a.CreateTriggers(ctx); e != nil {
							t.Fatal(e)
						}
						return
					}
					if name == "empty schema" {
						if e := a.DropTriggers(ctx); e != nil {
							t.Fatal(e)
						}
						if e := a.CreateViews(ctx); e != nil {
							t.Fatal(e)
						}
						if e := a.Revert(ctx, checkpoint.Checkpoint{ChainID: 1, BlockNumber: 10}); e != nil {
							t.Fatal(e)
						}
						if e := a.Finalize(ctx, checkpoint.Checkpoint{ChainID: 1, BlockNumber: 10}); e != nil {
							t.Fatal(e)
						}
						return
					}
					if e := a.RawBatch(ctx, 1, func(w *Writer) error {
						if ordering == schema.Isolated {
							_, e := w.SQL(ctx, `INSERT INTO account (chain_id,address,balance) VALUES ($1,$2,$3)`, 1, sourceZeroAddress, "10")
							return e
						}
						_, e := w.SQL(ctx, `INSERT INTO account (address,balance) VALUES ($1,$2)`, sourceZeroAddress, "10")
						return e
					}); e != nil {
						t.Fatal(e)
					}
					if name == "createTriggers()" {
						expectSourceJournal(t, a, []Row{{"address": sourceZeroAddress, "balance": "10", "operation": int64(0), "operation_id": int64(1), "checkpoint": checkpoint.MaxString}})
						return
					}
					if ordering == schema.Isolated {
						if e := a.CommitBlock(ctx, "account", checkpoint.Checkpoint{ChainID: 2, BlockNumber: 10}); e != nil {
							t.Fatal(e)
						}
						expectSourceJournal(t, a, []Row{{"address": sourceZeroAddress, "balance": "10", "chain_id": int64(1), "operation": int64(0), "operation_id": int64(1), "checkpoint": "999999999999999999999999999999999999999999999999999999999999999999999999999"}})
						if e := a.CommitBlock(ctx, "account", checkpoint.Checkpoint{ChainID: 1, BlockNumber: 10}); e != nil {
							t.Fatal(e)
						}
						expectSourceJournal(t, a, []Row{{"address": sourceZeroAddress, "balance": "10", "chain_id": int64(1), "operation": int64(0), "operation_id": int64(1), "checkpoint": "000000000000000000000000010000000000000010000000000000000000000000000000000"}})
						return
					}
					if e := a.CommitBlock(ctx, "account", checkpoint.Checkpoint{ChainID: 1, BlockNumber: 10}); e != nil {
						t.Fatal(e)
					}
					expectSourceJournal(t, a, []Row{{"address": sourceZeroAddress, "balance": "10", "operation": int64(0), "operation_id": int64(1), "checkpoint": checkpoint.MustEncode(actionCheckpoint(10))}})
				})
			}
		})
	}
}
