package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestDeploymentCommands(t *testing.T) {
	url := os.Getenv("PONDER_TEST_POSTGRES")
	if url == "" {
		t.Skip("explicit task PostgreSQL required")
	}
	ctx := t.Context()
	open := func() store.Backend {
		t.Helper()
		b, err := store.OpenPostgres(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close() })
		return b
	}
	admin := open()
	ns := fmt.Sprintf("ponder_go_cmd_%x", time.Now().UnixNano())
	old, next, views, external, staged := ns+"_one", ns+"_two", ns+"_views", ns+"_dep", ns+"_stage"
	// Every name belongs to this test, including the intentionally retained view.
	t.Cleanup(func() {
		for _, name := range []string{external, views, old, old + "_sync", next, next + "_sync", staged, staged + "_sync"} {
			_ = admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(name)+" CASCADE")
		}
	})
	model := schema.Schema{Tables: []schema.Table{{Name: "balance", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "integer", NotNull: true}}}}}
	writer := func(name string, value int, model schema.Schema) *store.Store {
		t.Helper()
		backend := open()
		s, err := store.New(ctx, backend, model, name, schema.Multichain)
		if err != nil {
			t.Fatal(err)
		}
		err = s.Apply(ctx, store.Event{ID: "write", Checkpoint: checkpoint.Block(100, 1, 1, true)}, func(w *store.Writer) error {
			return w.Write(ctx, "balance", store.Row{"id": "id", "value": value}, store.Insert)
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	oldWriter := writer(old, 11, model)
	nextModel := schema.Schema{Tables: []schema.Table{{Name: "balance", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "int8", NotNull: true}}}, {Name: "new_only", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
	newWriter := writer(next, 22, nextModel)
	publish := func(source, target string) error {
		return run(ctx, []string{"db", "create-views", "--database-url", url, "--schema", source, "--views-schema", target}, io.Discard)
	}
	prune := func(name, status string) {
		t.Helper()
		var out bytes.Buffer
		if err := run(ctx, []string{"db", "prune", "--database-url", url, "--schema", name}, &out); err != nil || !bytes.Contains(out.Bytes(), []byte(`"status":"`+status+`"`)) {
			t.Fatal(name, out.String(), err)
		}
	}
	value := func(ns string) string {
		t.Helper()
		r, err := admin.Query(ctx, "SELECT value::text AS value FROM "+schema.Quote(ns)+".balance")
		if err != nil || len(r.Rows) != 1 {
			t.Fatal(r, err)
		}
		return fmt.Sprint(r.Rows[0]["value"])
	}
	prune(old, "active")
	if err := publish(old, views); err != nil {
		t.Fatal(err)
	}
	if err := publish(old, views); err != nil {
		t.Fatal("idempotent publication:", err)
	}
	if value(views) != "11" {
		t.Fatal("wrong first published view")
	}
	listener, err := store.Listen(ctx, admin, views+"_live_query")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	update := func(rollback bool) error {
		return oldWriter.Apply(ctx, store.Event{ID: "second", Checkpoint: checkpoint.Block(101, 1, 2, true)}, func(w *store.Writer) error {
			if err := w.Write(ctx, "balance", store.Row{"id": "id", "value": 12}, store.Update); err != nil {
				return err
			}
			if rollback {
				return errors.New("rollback")
			}
			return nil
		})
	}
	if err := update(true); err == nil {
		t.Fatal("unexpected commit")
	}
	noMessage, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = listener.Wait(noMessage)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rolled back notification: %v", err)
	}
	if err := update(false); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	notice, err := listener.Wait(wait)
	cancel()
	if err != nil || notice.Payload != `["balance"]` {
		t.Fatalf("committed notification: %+v %v", notice, err)
	}
	oldWriter.Backend.Close()
	prune(old, "published")
	if err := admin.Exec(ctx, "CREATE VIEW "+schema.Quote(views)+".new_only AS SELECT id FROM "+schema.Quote(old)+".balance"); err != nil {
		t.Fatal(err)
	}
	if err := publish(next, views); err == nil || value(views) != "12" {
		t.Fatalf("adopted unrelated view: %v", err)
	}
	if err := admin.Exec(ctx, "DROP VIEW "+schema.Quote(views)+".new_only"); err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(ctx, "CREATE VIEW "+schema.Quote(views)+".reader AS SELECT * FROM "+schema.Quote(views)+".balance"); err != nil {
		t.Fatal(err)
	}
	if err := publish(next, views); err == nil || value(views) != "12" {
		t.Fatalf("changed incompatible view with a dependent: %v", err)
	}
	if err := admin.Exec(ctx, "DROP VIEW "+schema.Quote(views)+".reader"); err != nil {
		t.Fatal(err)
	}
	if err := publish(next, views); err != nil {
		t.Fatal(err)
	}
	if value(views) != "22" {
		t.Fatal("publishing a new deployment failed")
	}
	if err := admin.Exec(ctx, "CREATE SCHEMA "+schema.Quote(external)); err != nil {
		t.Fatal(err)
	}
	if err := publish(next, external); err == nil {
		t.Fatal("unowned views schema was adopted")
	}
	other := schema.Quote(external) + ".keep"
	if err := admin.Exec(ctx, "CREATE VIEW "+other+" AS SELECT * FROM "+schema.Quote(old)+".balance"); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"db", "prune", "--database-url", url, "--schema", old}, io.Discard); err == nil {
		t.Fatal("external dependency lost")
	}
	if value(old) != "12" || value(views) != "22" {
		t.Fatal("failed prune changed rows")
	}
	if err := admin.Exec(ctx, "DROP VIEW "+other); err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(ctx, "CREATE TABLE "+schema.Quote(old)+"._ponder_go_app (note text)"); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"db", "prune", "--database-url", url, "--schema", old}, io.Discard); err == nil || value(old) != "12" {
		t.Fatalf("unowned internal name lost: %v", err)
	}
	if err := admin.Exec(ctx, "DROP TABLE "+schema.Quote(old)+"._ponder_go_app"); err != nil {
		t.Fatal(err)
	}
	prune(old, "pruned")
	prune(old, "absent")
	// Terminate only the dedicated shared-lock session for this test namespace.
	stopped, err := admin.Query(ctx, `SELECT pg_terminate_backend(pid,5000) AS stopped FROM pg_locks
 WHERE locktype='advisory' AND granted AND mode='ShareLock' AND objsubid=1 AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
 AND classid=((hashtextextended($1,1)>>32)&4294967295)::oid AND objid=(hashtextextended($1,1)&4294967295)::oid`, next)
	if err != nil || len(stopped.Rows) != 1 || stopped.Rows[0]["stopped"] != true {
		t.Fatalf("disconnect test session: %v %v", stopped, err)
	}
	called := false
	if err := newWriter.Apply(ctx, store.Event{ID: "stale", Checkpoint: checkpoint.Block(101, 1, 2, true)}, func(w *store.Writer) error { called = true; return nil }); err == nil || called {
		t.Fatalf("stale writer reached handler: %v", err)
	}
	if err := newWriter.CachePut(ctx, "stale", json.RawMessage("1")); err == nil {
		t.Fatal("disconnected writer changed the RPC cache")
	}
	newWriter.Backend.Close()
	reopened, err := store.New(ctx, open(), nextModel, next, schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Apply(ctx, store.Event{ID: "reopened", Checkpoint: checkpoint.Block(101, 1, 2, true)}, func(w *store.Writer) error {
		return w.Write(ctx, "balance", store.Row{"id": "id", "value": 23}, store.Update)
	}); err != nil || value(views) != "23" {
		t.Fatalf("reopen: %v", err)
	}
	reopened.Backend.Close()
	prune(next, "published")
	lifecycle, err := store.NewLifecycle(admin, model, staged, schema.Multichain, store.LifecycleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lifecycle.Close() })
	if _, err = lifecycle.Migrate(ctx, store.Migration{BuildID: "prune-staged"}); err != nil {
		t.Fatal(err)
	}
	prune(staged, "active")
	if err = lifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	prune(staged, "pruned")
	prune(external, "unregistered")
}

func TestDeploymentSweep(t *testing.T) {
	url := os.Getenv("PONDER_TEST_PRUNE_DATABASE")
	if url == "" {
		t.Skip("dedicated empty database for deployment sweep required")
	}
	ctx := t.Context()
	b, err := store.OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	identity, err := b.Query(ctx, `SELECT starts_with(current_database(),'ponder_prune_test_') AND NOT EXISTS(SELECT FROM pg_namespace WHERE nspname<>'public' AND nspname<>'information_schema' AND NOT starts_with(nspname,'pg_')) AS empty_owned`)
	if err != nil || len(identity.Rows) != 1 || identity.Rows[0]["empty_owned"] != true {
		t.Fatalf("sweep requires a fresh dedicated database: %v", err)
	}
	model := schema.Schema{Tables: []schema.Table{{Name: "balance", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
	ns := fmt.Sprintf("ponder_go_sweep_%x", time.Now().UnixNano())
	for _, suffix := range []string{"_a", "_b"} {
		backend, err := store.OpenPostgres(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.New(ctx, backend, model, ns+suffix, schema.Multichain)
		backend.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run(ctx, []string{"db", "prune", "--database-url", url}, &out); err != nil {
		t.Fatal(err)
	}
	var got []store.PrunedDeployment
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []store.PrunedDeployment{{Schema: ns + "_a", Status: "pruned"}, {Schema: ns + "_b", Status: "pruned"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sweep = %v, want %v", got, want)
	}
}
