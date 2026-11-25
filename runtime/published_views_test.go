package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type viewTestIndexer func(context.Context, func(bool)) error

func (run viewTestIndexer) RunIndexing(ctx context.Context, ready func(bool)) error {
	return run(ctx, ready)
}

func TestPublishViewsOnReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	backend := stagedBackend(t, ctx, "postgres", "")
	defer backend.Close()
	name := fmt.Sprintf("ponder_go_cut_%x", time.Now().UnixNano())
	views := name + "_views"
	model := schema.Schema{Tables: []schema.Table{{Name: "entry", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
	open := func(suffix string, m schema.Schema) *store.Store {
		t.Helper()
		db, err := store.New(ctx, backend, m, name+suffix, schema.Multichain)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	old, next := open("_old", model), open("_next", model)
	defer func() {
		cleanup := []string{views, old.Namespace, old.SyncNamespace, next.Namespace, next.SyncNamespace, name + "_bad", name + "_bad_sync"}
		for _, ns := range cleanup {
			if err := backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(ns)+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
	}()
	err := old.Apply(ctx, store.Event{ID: "old", Checkpoint: checkpoint.Block(1, 1, 0, true)}, func(w *store.Writer) error {
		return w.Write(ctx, "entry", store.Row{"id": "before"}, store.Insert)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PublishViews(ctx, backend, old.Namespace, views); err != nil {
		t.Fatal(err)
	}
	reader, err := query.NewAtNamespace(ctx, old, views)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.Ready.Store(true)
	httpReader := httptest.NewServer(reader)
	defer httpReader.Close()
	statement := "SELECT id FROM entry"
	params := url.Values{"sql": {`{"json":{"sql":"SELECT id FROM entry","params":[]}}`}}
	req, err := http.NewRequestWithContext(ctx, "GET", httpReader.URL+"/sql/live?"+params.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := httpReader.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	read := func(want string) {
		t.Helper()
		limit := time.Now().Add(5 * time.Second)
		for {
			rows, err := (query.Client{BaseURL: httpReader.URL + "/sql"}).Query(ctx, statement)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows.Rows) == 1 && rows.Rows[0]["id"] == want {
				return
			}
			if time.Now().After(limit) {
				t.Fatalf("published rows %v, want %q", rows.Rows, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	read("before")

	endpoint := testanvil.Start(t, "--no-mining")
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	markEntered := sync.OnceFunc(func() { close(entered) })
	cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{endpoint}, PollingIntervalMS: 20}}, Blocks: []config.BlockSource{{Name: "head", Chain: "local", Interval: 1, StartBlock: 0}}}
	worker, err := NewIndexer(cfg, next, map[string]Handler{"head:block": func(ctx context.Context, c *Context) error {
		markEntered()
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return c.DB.Write(ctx, "entry", store.Row{"id": "after"}, store.Insert)
	}})
	if err != nil {
		t.Fatal(err)
	}
	worker, err = PublishViewsOnReady(worker, next, views)
	if err != nil {
		t.Fatal(err)
	}
	routes := query.New(next)
	app, err := LaunchApplication(ctx, worker, routes, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		unblock()
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ready := func(want int) {
		t.Helper()
		resp, err := http.Get("http://" + app.Addr().String() + "/ready")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("ready status %d, want %d", resp.StatusCode, want)
		}
	}
	ready(http.StatusServiceUnavailable)
	read("before")
	unblock()
	select {
	case <-app.ready:
	case <-app.done:
		t.Fatalf("application stopped: %v", app.Wait())
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ready(http.StatusOK)
	scan := bufio.NewScanner(resp.Body)
	for scan.Scan() {
		payload, ok := strings.CutPrefix(scan.Text(), "data: ")
		if !ok {
			continue
		}
		var result store.Result
		if err = json.Unmarshal([]byte(payload), &result); err != nil || len(result.Rows) != 1 || result.Rows[0]["id"] != "after" {
			t.Fatalf("switch notification %q: %v", payload, err)
		}
		break
	}
	if ctx.Err() != nil || scan.Err() != nil || !strings.HasPrefix(scan.Text(), "data: ") {
		t.Fatal("no SSE refresh on publication", scan.Err(), ctx.Err())
	}
	read("after")
	if err := app.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	finite, err := PublishViewsOnReady(viewTestIndexer(func(context.Context, func(bool)) error { return nil }), old, views)
	if err != nil {
		t.Fatal(err)
	}
	if err = finite.RunIndexing(ctx, func(bool) {}); err != nil {
		t.Fatal(err)
	}
	read("before")
	repeated, err := PublishViewsOnReady(viewTestIndexer(func(ctx context.Context, ready func(bool)) error {
		ready(true)
		if err := store.PublishViews(ctx, backend, old.Namespace, views); err != nil {
			return err
		}
		ready(false)
		ready(true)
		return nil
	}), next, views)
	if err != nil {
		t.Fatal(err)
	}
	if err = repeated.RunIndexing(ctx, func(bool) {}); err != nil {
		t.Fatal(err)
	}
	read("before")

	badModel := schema.Schema{Tables: []schema.Table{{Name: "entry", Columns: []schema.Column{{Name: "id", Type: "integer", Primary: true}}}}}
	bad := open("_bad", badModel)
	dependent := schema.Quote(old.Namespace) + ".reader_dependency"
	if err := backend.Exec(ctx, "CREATE VIEW "+dependent+" AS SELECT id FROM "+schema.Quote(views)+".entry"); err != nil {
		t.Fatal(err)
	}
	failed, err := PublishViewsOnReady(viewTestIndexer(func(ctx context.Context, ready func(bool)) error {
		ready(true)
		<-ctx.Done()
		return ctx.Err()
	}), bad, views)
	if err != nil {
		t.Fatal(err)
	}
	badRoutes := query.New(bad)
	failedApp, err := LaunchApplication(ctx, failed, badRoutes, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = failedApp.Wait(); err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "publish query views") {
		t.Fatalf("publication error lost: %v", err)
	}
	select {
	case <-failedApp.ready:
		t.Fatal("failed publisher became ready")
	default:
	}
	read("before")

	// Hold the publication lock before a third deployment is allowed to
	// switch. Cancelling startup leaves the previous deployment published.
	hold, err := backend.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(context.Background())
	if err := hold.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,2))", views); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	pending, err := PublishViewsOnReady(viewTestIndexer(func(ctx context.Context, report func(bool)) error {
		close(started)
		report(true)
		return ctx.Err()
	}), next, views)
	if err != nil {
		t.Fatal(err)
	}
	stopping, stop := context.WithCancel(ctx)
	pendingRoutes := query.New(next)
	pendingApp, err := LaunchApplication(stopping, pending, pendingRoutes, "127.0.0.1", 0)
	if err != nil {
		stop()
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A waiting lock proves the callback is inside the publication transaction.
	for {
		waiting, err := backend.Query(ctx, `SELECT EXISTS (SELECT FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid=((hashtextextended($1,2)>>32)&4294967295)::oid AND objid=(hashtextextended($1,2)&4294967295)::oid) AS waiting`, views)
		if err != nil {
			t.Fatal(err)
		}
		if waiting.Rows[0]["waiting"] == true {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if pendingRoutes.Ready.Load() {
		t.Fatal("publication still waiting but application reported ready")
	}
	stop()
	if err := pendingApp.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	read("before")
}
