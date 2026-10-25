package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

// Exact source case from database/actions.test.ts. The checkpoint table is
// adapted to the native progress relation; notifications come from PostgreSQL
// or PGlite LISTEN/NOTIFY, not Store's in-process change subscribers.
func TestUpstreamLiveQueryNotify(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Run("live query notify trigger batches large payloads", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				a := sourceActions(t, dialect, sourceActionsModel(), schema.Multichain)
				zero := checkpoint.MustEncode(actionCheckpoint(0))
				if e := a.InitializeCheckpoints(ctx, []CheckpointState{{ChainName: "mainnet", ChainID: 1, SafeCheckpoint: zero, FinalizedCheckpoint: zero, LatestCheckpoint: zero}}); e != nil {
					t.Fatal(e)
				}
				if e := a.CreateLiveQueryProcedures(ctx); e != nil {
					t.Fatal(e)
				}
				if e := a.CreateLiveQueryTriggers(ctx, nil); e != nil {
					t.Fatal(e)
				}
				listener, e := Listen(ctx, a.s.Backend, a.LiveQueryChannel())
				if e != nil {
					t.Fatal(e)
				}
				defer listener.Close()
				e = a.transaction(ctx, func(tx Transaction) error {
					if e := prepareLiveQueryChanges(ctx, tx, dialect); e != nil {
						return e
					}
					values := make([]string, 160)
					for i := range values {
						values[i] = fmt.Sprintf("('table_%03d_%s')", i, strings.Repeat("x", 48))
					}
					if e := tx.Exec(ctx, "INSERT INTO pg_temp.live_query_tables (table_name) VALUES "+strings.Join(values, ",")); e != nil {
						return e
					}
					return tx.Exec(ctx, "UPDATE "+a.s.internal("_ponder_go_progress")+" SET checkpoint=$1 WHERE chain_id=1", checkpoint.MustEncode(actionCheckpoint(1)))
				})
				if e != nil {
					t.Fatal(e)
				}
				notifications := []string{}
				for len(notifications) < 2 {
					n, e := listener.Wait(ctx)
					if e != nil {
						t.Fatal(e)
					}
					notifications = append(notifications, n.Payload)
				}
				if len(notifications) <= 1 {
					t.Fatal("source assertion: notifications.length > 1")
				}
				entries := []string{}
				for _, payload := range notifications {
					var names []string
					if e := json.Unmarshal([]byte(payload), &names); e != nil {
						t.Fatal(e)
					}
					entries = append(entries, names...)
				}
				if len(entries) != 160 {
					t.Fatalf("source flatMap length assertion: %d != 160", len(entries))
				}
				for _, payload := range notifications {
					if len([]byte(payload)) >= 8000 {
						t.Fatal("source every Buffer.byteLength(payload) < 8000 assertion")
					}
				}
			})
		})
	}
}
