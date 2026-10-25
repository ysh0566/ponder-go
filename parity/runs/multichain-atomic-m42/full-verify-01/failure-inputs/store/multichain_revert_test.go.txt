package store

import (
	"context"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

// A same-channel SQL sentinel is a causal fence, not a timeout-based assertion
// that an expected notification merely failed to arrive promptly.
func TestMultichainPrimitiveQuietNotificationsAndRevert(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			a := sourceActions(t, dialect, sourceActionsModel(), schema.Multichain)
			if err := a.CreateLiveQueryProcedures(ctx); err != nil {
				t.Fatal(err)
			}
			if err := a.CreateLiveQueryTriggers(ctx, []string{"account"}); err != nil {
				t.Fatal(err)
			}
			listener, err := Listen(ctx, a.s.Backend, a.LiveQueryChannel())
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			changes, unsubscribe := a.s.Subscribe()
			defer unsubscribe()
			fence := func(label string) {
				t.Helper()
				if err := a.s.Backend.Exec(ctx, "SELECT pg_notify($1,$2)", a.LiveQueryChannel(), label); err != nil {
					t.Fatal(err)
				}
				waitNotification(t, listener, a.LiveQueryChannel(), label)
				select {
				case got := <-changes:
					t.Fatalf("unexpected in-process notification: %v", got)
				default:
				}
			}
			err = a.InitializeMultichain(ctx, multichainInitialStates(), func(w *Writer, attribute func(uint64) error) error {
				if err := attribute(1); err != nil {
					return err
				}
				return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert)
			})
			if err != nil {
				t.Fatal(err)
			}
			fence("setup-finished")
			safe := checkpoint.Block(101, 1, 2, true)
			if applied, err := a.ProcessHistoricalGroup(ctx, "mainnet", safe, func(w *Writer) error {
				_, err := w.Indexing().Update(ctx, "account", Row{"address": sourceZeroAddress}, Row{"balance": "20"})
				return err
			}); err != nil || !applied {
				t.Fatal(applied, err)
			}
			fence("history-finished")
			for _, group := range []struct {
				chain, block uint64
				balance      string
			}{{1, 3, "30"}, {10, 4, "40"}} {
				target := checkpoint.Block(100+group.block, group.chain, group.block, true)
				if err := a.RawBatch(ctx, group.chain, func(w *Writer) error {
					// These are deliberate low-level unfinalized writes, not readiness or a
					// realtime runner. Suppress their unrelated checkpoint notifications.
					if err := w.tx.Exec(ctx, `SET LOCAL "ponder_go.suppress_notify"='1'`); err != nil {
						return err
					}
					if _, err := w.Indexing().Update(ctx, "account", Row{"address": sourceZeroAddress}, Row{"balance": group.balance}); err != nil {
						return err
					}
					if err := w.CommitBlock(ctx, "account", target); err != nil {
						return err
					}
					return w.AdvanceLatest(ctx, target)
				}); err != nil {
					t.Fatal(err)
				}
			}
			fence("unfinalized-finished")
			if err := a.RevertBusiness(ctx, safe); err != nil {
				t.Fatal(err)
			}
			fence("revert-finished")
			rows, err := a.Select(ctx, "account")
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			expectSourceBalance(t, rows[0], 20)
			journal, err := a.Journal(ctx, "account")
			if err != nil || len(journal) != 0 {
				t.Fatal("global suffix retained", journal, err)
			}
			if err := a.RevertBusiness(ctx, checkpoint.Block(100, 1, 1, true)); err == nil {
				t.Fatal("safe prefix rollback accepted")
			}
			fence("refused-revert-finished")
		})
	}
}
