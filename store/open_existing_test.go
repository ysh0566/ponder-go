package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/schema"
)

func TestOpenExistingDeployment(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			b := sourceBackend(t, ctx, dialect)
			ns := fmt.Sprintf("ponder_go_existing_%x", time.Now().UnixNano())
			if dialect == "postgres" {
				t.Cleanup(func() {
					if err := b.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(ns)+", "+schema.Quote(ns+"_sync")+" CASCADE"); err != nil {
						t.Error(err)
					}
				})
			}
			model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
			if s, err := OpenExisting(ctx, b, model, ns, schema.Multichain); err == nil || s != nil {
				t.Fatal("opened an absent deployment")
			}
			got, err := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname IN ($1,$2)", ns, ns+"_sync")
			if err != nil || len(got.Rows) != 0 {
				t.Fatalf("read-only open created schemas: %v %v", got, err)
			}
			writer, err := New(ctx, b, model, ns, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			foreign := writer.internal("_ponder_go_query_notify") + "()"
			if err := b.Exec(ctx, "CREATE FUNCTION "+foreign+" RETURNS integer LANGUAGE sql AS 'SELECT 7'"); err != nil {
				t.Fatal(err)
			}
			if err := writer.EnableQueryNotifications(ctx); err == nil {
				t.Fatal("replaced an unrelated function")
			}
			probe, err := b.Query(ctx, "SELECT "+foreign+" AS sentinel")
			if err != nil || fmt.Sprint(probe.Rows[0]["sentinel"]) != "7" {
				t.Fatalf("unowned function changed: %v %v", probe, err)
			}
			if err := b.Exec(ctx, "DROP FUNCTION "+foreign); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := writer.EnableQueryNotifications(ctx); err != nil {
					t.Fatal(err)
				}
			}
			reader, err := OpenExisting(ctx, b, model, ns, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := reader.QueryReadOnly(ctx, "SELECT id FROM account")
			if err != nil || len(rows.Rows) != 0 {
				t.Fatalf("empty deployed table: %v %v", rows, err)
			}
			watch, err := Listen(ctx, b, ns+"_live_query")
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			event := Event{ID: "insert", Checkpoint: checkpoint.Block(100, 1, 1, true)}
			insert := func(w *Writer) error {
				return w.Write(ctx, "account", Row{"id": "one"}, Insert)
			}
			stop := errors.New("rollback")
			if err := writer.Apply(ctx, event, func(w *Writer) error {
				if err := insert(w); err != nil {
					return err
				}
				return stop
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			expectNoNotification(t, watch)
			if err := writer.Apply(ctx, event, insert); err != nil {
				t.Fatal(err)
			}
			deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			for {
				n, err := watch.Wait(deadline)
				if err != nil {
					t.Fatal(err)
				}
				if n.Channel == ns+"_live_query" && n.Payload == `["account"]` {
					break
				}
			}
			mismatch := schema.Schema{Tables: []schema.Table{{Name: "another", Columns: model.Tables[0].Columns}}}
			if s, err := OpenExisting(ctx, b, mismatch, ns, schema.Multichain); err == nil || s != nil {
				t.Fatal("opened with a changed application model")
			}
			if s, err := OpenExisting(ctx, b, model, ns, schema.Omnichain); err == nil || s != nil {
				t.Fatal("opened with different ordering")
			}
		})
	}
}
