package store

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

func TestStagedActionGuards(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			a := sourceActions(t, dialect, sourceActionsModel(), schema.Multichain)
			zero := checkpoint.MustEncode(actionCheckpoint(0))
			if e := a.InitializeCheckpoints(ctx, []CheckpointState{{ChainName: "mainnet", ChainID: 1, SafeCheckpoint: zero, FinalizedCheckpoint: zero, LatestCheckpoint: zero}}); e != nil {
				t.Fatal(e)
			}
			write := func(w *Writer) error {
				return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert)
			}
			if e := a.RawBatch(ctx, 1, write); e == nil {
				t.Fatal("write before capture trigger installation")
			}
			if e := a.CreateTriggers(ctx); e != nil {
				t.Fatal(e)
			}
			if e := a.RawBatch(ctx, 1, write); e != nil {
				t.Fatal(e)
			}
			if e := a.Finalize(ctx, actionCheckpoint(1)); e == nil {
				t.Fatal("pending MAX finalized")
			}
			if e := a.Revert(ctx, actionCheckpoint(0)); e == nil {
				t.Fatal("normal revert accepted pending MAX")
			}
			if _, e := New(ctx, a.s.Backend, a.s.Schema, a.s.Namespace, a.s.Ordering); e == nil {
				t.Fatal("staged schema adopted by atomic runtime")
			}
			if e := a.CrashRecovery(ctx); e != nil {
				t.Fatal(e)
			}
			if e := a.DropTriggers(ctx); e != nil {
				t.Fatal(e)
			}
			if e := a.RawBatch(ctx, 1, write); e == nil {
				t.Fatal("business writes bypassed undo after dropping capture triggers")
			}
			if e := a.s.Backend.Exec(ctx, "INSERT INTO "+a.s.table("account")+" (address,balance) VALUES ($1,$2)", sourceZeroAddress, "10"); e == nil {
				t.Fatal("event-less write bypassed permanent guard")
			}
			if e := a.CreateTriggers(ctx); e != nil {
				t.Fatal(e)
			}
			if e := a.RawBatch(ctx, 1, func(w *Writer) error {
				if e := write(w); e != nil {
					return e
				}
				if e := w.CommitBlock(ctx, "account", actionCheckpoint(1)); e != nil {
					return e
				}
				if e := w.AdvanceLatest(ctx, actionCheckpoint(1)); e != nil {
					return e
				}
				return w.Write(ctx, "account", Row{"address": "0x01", "balance": "20"}, Insert)
			}); e == nil {
				t.Fatal("new pending write survived an advanced latest checkpoint")
			}
			rows, e := a.Select(ctx, "account")
			if e != nil || len(rows) != 0 {
				t.Fatalf("failed atomic action batch leaked data %v %v", rows, e)
			}
			states, e := a.Checkpoints(ctx)
			if e != nil || states[0].LatestCheckpoint != zero {
				t.Fatalf("failed atomic action batch advanced progress %v %v", states, e)
			}
			if e := a.RawBatch(ctx, 1, func(w *Writer) error {
				if e := write(w); e != nil {
					return e
				}
				if e := w.CommitBlock(ctx, "account", actionCheckpoint(1)); e != nil {
					return e
				}
				return w.AdvanceLatest(ctx, actionCheckpoint(1))
			}); e != nil {
				t.Fatal(e)
			}
			rows, e = a.Select(ctx, "account")
			if e != nil || len(rows) != 1 {
				t.Fatal(rows, e)
			}
			expectSourceBalance(t, rows[0], 10)
			states, e = a.Checkpoints(ctx)
			if e != nil || states[0].LatestCheckpoint != checkpoint.MustEncode(actionCheckpoint(1)) {
				t.Fatal(states, e)
			}
			uninitialized := sourceActions(t, dialect, sourceActionsModel(), schema.Multichain)
			if e := uninitialized.CreateTriggers(ctx); e != nil {
				t.Fatal(e)
			}
			if e := uninitialized.RawBatch(ctx, 1, write); e != nil {
				t.Fatal(e)
			}
			if e := uninitialized.CommitBlock(ctx, "account", actionCheckpoint(1)); e != nil {
				t.Fatal(e)
			}
			if e := uninitialized.Finalize(ctx, actionCheckpoint(1)); e == nil {
				t.Fatal("uninitialized chain finalized and discarded undo")
			}
			retained, e := uninitialized.Journal(ctx, "account")
			if e != nil || len(retained) != 1 {
				t.Fatalf("missing metadata lost journal: %v %v", retained, e)
			}
		})
	}
}

func TestStagedGlobalSuffixAndIsolated(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, ordering := range []schema.Ordering{schema.Multichain, schema.Isolated} {
			t.Run(dialect+"/"+string(ordering), func(t *testing.T) {
				ctx := context.Background()
				model := sourceActionsModel()
				if ordering == schema.Isolated {
					model.Tables[0] = schema.Table{Name: "account", Columns: []schema.Column{{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "address", Type: "hex", NotNull: true}, {Name: "balance", Type: "bigint"}}, PrimaryKey: []string{"chain_id", "address"}}
				}
				a := sourceActions(t, dialect, model, ordering)
				if e := a.CreateTriggers(ctx); e != nil {
					t.Fatal(e)
				}
				write := func(chain, block uint64, address string, balance int64, mode WriteMode, commit bool) {
					t.Helper()
					row := Row{"address": address, "balance": fmt.Sprint(balance)}
					if ordering == schema.Isolated {
						row["chain_id"] = chain
					}
					if e := a.RawBatch(ctx, chain, func(w *Writer) error { return w.Write(ctx, "account", row, mode) }); e != nil {
						t.Fatal(e)
					}
					if commit {
						if e := a.CommitBlock(ctx, "account", checkpoint.Checkpoint{ChainID: chain, BlockNumber: block}); e != nil {
							t.Fatal(e)
						}
					}
				}
				write(1, 1, sourceZeroAddress, 1, Insert, true)
				mode := Update
				if ordering == schema.Isolated {
					mode = Insert
				}
				write(10, 1, sourceZeroAddress, 10, mode, true)
				write(1, 2, sourceZeroAddress, 2, Update, true)
				write(10, 2, "0x01", 20, Insert, ordering != schema.Isolated)
				if e := a.Revert(ctx, actionCheckpoint(1)); e != nil {
					t.Fatal(e)
				}
				rows, e := a.Select(ctx, "account")
				if e != nil {
					t.Fatal(e)
				}
				if ordering == schema.Multichain {
					if len(rows) != 1 {
						t.Fatal("global operation suffix retained later other-chain write", rows)
					}
					expectSourceBalance(t, rows[0], 10)
				} else {
					if len(rows) != 3 {
						t.Fatal("isolated rollback touched other chain", rows)
					}
					for _, row := range rows {
						if row["chainId"] == int64(1) {
							if row["balance"].(*big.Int).Cmp(big.NewInt(1)) != 0 {
								t.Fatal(row)
							}
						}
					}
					journal, e := a.Journal(ctx, "account")
					if e != nil {
						t.Fatal(e)
					}
					pending := 0
					for _, row := range journal {
						if row["checkpoint"] == checkpoint.MaxString {
							pending++
						}
					}
					if pending != 1 {
						t.Fatal("other-chain pending operation was changed", journal)
					}
				}
			})
		}
	}
}
