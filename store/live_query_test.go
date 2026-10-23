package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

func liveQueryActions(t *testing.T, dialect string) (*Actions, NotificationListener) {
	t.Helper()
	ctx := context.Background()
	model := sourceActionsModel()
	fields := []schema.ViewField{}
	for _, column := range model.Tables[0].Columns {
		c := column
		fields = append(fields, schema.ViewField{Name: c.Name, Column: &c})
	}
	model.Views = []schema.View{{Name: "account_view", Fields: fields, Query: `SELECT * FROM account`}}
	a := sourceActions(t, dialect, model, schema.Multichain)
	zero := checkpoint.MustEncode(actionCheckpoint(0))
	if e := a.InitializeCheckpoints(ctx, []CheckpointState{{ChainName: "mainnet", ChainID: 1, SafeCheckpoint: zero, FinalizedCheckpoint: zero, LatestCheckpoint: zero}}); e != nil {
		t.Fatal(e)
	}
	if e := a.CreateTriggers(ctx); e != nil {
		t.Fatal(e)
	}
	if e := a.CreateLiveQueryProcedures(ctx); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e := a.CreateLiveQueryTriggers(ctx, []string{"account"}); e != nil {
			t.Fatal(e)
		}
	}
	l, e := Listen(ctx, a.s.Backend, a.LiveQueryChannel())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	return a, l
}

func TestLiveQueryCommitBoundary(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			a, listener := liveQueryActions(t, dialect)
			commit := func(w *Writer, block uint64) error {
				if e := w.CommitBlock(ctx, "account", actionCheckpoint(block)); e != nil {
					return e
				}
				return w.AdvanceLatest(ctx, actionCheckpoint(block))
			}
			if e := a.RawBatch(ctx, 1, func(w *Writer) error {
				if _, e := w.Indexing().InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Conflict{}); e != nil {
					return e
				}
				return commit(w, 1)
			}); e != nil {
				t.Fatal(e)
			}
			waitNotification(t, listener, a.LiveQueryChannel(), `["account"]`)
			abort := errors.New("abort after checkpoint notification statement")
			if e := a.RawBatch(ctx, 1, func(w *Writer) error {
				if _, e := w.SQL(ctx, `UPDATE account_view SET balance=11 WHERE address=$1`, sourceZeroAddress); e != nil {
					return e
				}
				if e := commit(w, 2); e != nil {
					return e
				}
				return abort
			}); !errors.Is(e, abort) {
				t.Fatalf("rollback error: %v", e)
			}
			expectNoNotification(t, listener)
			rows, e := a.Select(ctx, "account")
			if e != nil || len(rows) != 1 || fmt.Sprint(rows[0]["balance"]) != "10" {
				t.Fatalf("rollback state: %#v %v", rows, e)
			}
			journal, e := a.Journal(ctx, "account")
			if e != nil || len(journal) != 1 {
				t.Fatalf("rollback journal: %#v %v", journal, e)
			}
			if e := a.RawBatch(ctx, 1, func(w *Writer) error {
				if _, e := w.SQL(ctx, `UPDATE account_view SET balance=11 WHERE address=$1`, sourceZeroAddress); e != nil {
					return e
				}
				return commit(w, 2)
			}); e != nil {
				t.Fatal(e)
			}
			waitNotification(t, listener, a.LiveQueryChannel(), `["account"]`)
			// Undo still executes business triggers. It must have its own temp
			// collector even after PostgreSQL dropped the previous transaction's.
			if e := a.Revert(ctx, actionCheckpoint(1)); e != nil {
				t.Fatal(e)
			}
			rows, e = a.Select(ctx, "account")
			if e != nil || len(rows) != 1 || fmt.Sprint(rows[0]["balance"]) != "10" {
				t.Fatalf("revert with live triggers: %#v %v", rows, e)
			}
			expectNoNotification(t, listener) // primitive intentionally leaves metadata
			if e := a.AdvanceLatest(ctx, actionCheckpoint(1)); e != nil {
				t.Fatal(e)
			}
			waitNotification(t, listener, a.LiveQueryChannel(), `[]`)
			if e := a.Finalize(ctx, actionCheckpoint(1)); e != nil {
				t.Fatal(e)
			}
			waitNotification(t, listener, a.LiveQueryChannel(), `[]`)
			for i := 0; i < 2; i++ {
				if e := a.DropLiveQueryTriggers(ctx, []string{"account"}); e != nil {
					t.Fatal(e)
				}
			}
			if e := a.RawBatch(ctx, 1, func(w *Writer) error {
				if _, e := w.SQL(ctx, `UPDATE account_view SET balance=12 WHERE address=$1`, sourceZeroAddress); e != nil {
					return e
				}
				return commit(w, 3)
			}); e != nil {
				t.Fatal(e)
			}
			expectNoNotification(t, listener)
			journal, e = a.Journal(ctx, "account")
			if e != nil || len(journal) != 1 || journal[0]["balance"] != "10" {
				t.Fatalf("notification removal changed undo capture: %#v %v", journal, e)
			}
		})
	}
}

func TestLiveQueryUTF8Payload(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			a, listener := liveQueryActions(t, dialect)
			names := make([]string, 160)
			for i := range names {
				names[i] = fmt.Sprintf("table_%03d_%s\"\n\\", 159-i, strings.Repeat("界", 18))
			}
			if e := a.transaction(ctx, func(tx Transaction) error {
				for _, name := range names {
					if e := tx.Exec(ctx, "INSERT INTO pg_temp.live_query_tables VALUES($1)", name); e != nil {
						return e
					}
				}
				return tx.Exec(ctx, "UPDATE "+a.s.internal("_ponder_go_progress")+" SET checkpoint=$1", checkpoint.MustEncode(actionCheckpoint(1)))
			}); e != nil {
				t.Fatal(e)
			}
			got := []string{}
			deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			parts := 0
			for len(got) < len(names) {
				n, e := listener.Wait(deadline)
				if e != nil {
					t.Fatal(e)
				}
				if len([]byte(n.Payload)) >= 7900 {
					t.Fatalf("UTF-8 byte boundary: %d", len([]byte(n.Payload)))
				}
				var entries []string
				if e = json.Unmarshal([]byte(n.Payload), &entries); e != nil {
					t.Fatal(e)
				}
				got = append(got, entries...)
				parts++
			}
			sort.Strings(names)
			if parts < 2 || !reflect.DeepEqual(got, names) {
				t.Fatalf("JSON escaping, sort or chunk loss: %d parts, %d/%d names", parts, len(got), len(names))
			}
		})
	}
}
