package query

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestPostgresQueryListenerReconnect(t *testing.T) {
	b := sourceClientDB(t, "postgres")
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	admin, err := store.OpenPostgres(ctx, os.Getenv("PONDER_TEST_POSTGRES"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	info, err := b.Query(ctx, "SELECT current_database() AS name")
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprint(info.Rows[0]["name"])
	if !strings.HasPrefix(name, "ponder_go_client_") || !schema.Identifier(name) {
		t.Fatalf("unexpected test database %q", name)
	}
	model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
	db, err := store.New(ctx, b, model, "ponder_go_reconnect", schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Exec(ctx, "CREATE TABLE public.account (id text PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	s, err := NewAtNamespace(ctx, db, "public")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Ready.Store(true)
	host := httptest.NewServer(s)
	defer host.Close()
	statement := "SELECT id FROM account ORDER BY id"
	envelope, err := json.Marshal(map[string]any{"json": request{SQL: statement, Params: []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host.URL+"/sql/live?sql="+url.QueryEscape(string(envelope)), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := host.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected live response %s %s", stream.Status, stream.Header.Get("Content-Type"))
	}
	lines := bufio.NewScanner(stream.Body)
	expectRows := func(ids ...string) {
		t.Helper()
		for lines.Scan() {
			payload, ok := strings.CutPrefix(lines.Text(), "data: ")
			if !ok {
				continue
			}
			var result store.Result
			if err := json.Unmarshal([]byte(payload), &result); err != nil || len(result.Rows) != len(ids) {
				t.Fatalf("SSE data %q, want %v: %v", payload, ids, err)
			}
			for i, id := range ids {
				if result.Rows[i]["id"] != id {
					t.Fatalf("SSE row %d: %v, want %q", i, result.Rows[i], id)
				}
			}
			return
		}
		t.Fatalf("original SSE connection ended: %v", lines.Err())
	}
	write := func(id string) {
		t.Helper()
		if err := b.Exec(ctx, "INSERT INTO public.account VALUES ($1)", id); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Query(ctx, "SELECT pg_notify('public_live_query','[\"account\"]')"); err != nil {
			t.Fatal(err)
		}
	}
	write("a")
	expectRows("a")

	// Keep the existing SQL pool open for writes and fresh reads, but prevent
	// the dedicated LISTEN session from reconnecting to this disposable database.
	connectionSetting := "ALTER DATABASE " + schema.Quote(name) + " ALLOW_CONNECTIONS "
	if err := admin.Exec(ctx, connectionSetting+"false"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := admin.Exec(context.Background(), connectionSetting+"true"); err != nil {
			t.Error(err)
		}
	}()
	killed, err := admin.Query(ctx, "SELECT pg_terminate_backend(pid) AS stopped FROM pg_stat_activity WHERE datname=$1 AND query=$2", name, `LISTEN "public_live_query"`)
	if err != nil || len(killed.Rows) != 1 || killed.Rows[0]["stopped"] != true {
		t.Fatalf("could not isolate and close the listener: %v, %v", killed, err)
	}
	for {
		if _, listening := s.changes().QueryVersion([]string{"account"}); !listening {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("listener stayed active")
		case <-time.After(10 * time.Millisecond):
		}
	}
	read := Client{BaseURL: host.URL + "/sql", HTTP: host.Client()}
	for i, id := range []string{"b", "c"} {
		write(id)
		result, err := read.Query(ctx, statement)
		if err != nil || len(result.Rows) != i+2 {
			t.Fatalf("fresh SQL read while listener is offline: %v %v", result, err)
		}
	}
	// Leave enough time for a new connection attempt to be rejected.
	select {
	case <-s.subscription.Done():
		t.Fatal("subscription ended during the outage")
	case <-time.After(550 * time.Millisecond):
	}
	// A query server started during the interruption can serve uncached SQL
	// and begins listening as soon as new connections are permitted again.
	cold, err := NewAtNamespace(ctx, db, "public")
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if _, listening := cold.changes().QueryVersion([]string{"account"}); listening {
		t.Fatal("cold server cached data before establishing LISTEN")
	}
	coldChanges, detach := cold.changes().Subscribe()
	defer detach()
	abandoned, err := NewAtNamespace(ctx, db, "public")
	if err != nil {
		t.Fatal(err)
	}
	if err := abandoned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(ctx, connectionSetting+"true"); err != nil {
		t.Fatal(err)
	}
	expectRows("a", "b", "c")
	select {
	case <-coldChanges:
		if _, listening := cold.changes().QueryVersion([]string{"account"}); !listening {
			t.Fatal("cold listener failed to connect")
		}
	case <-ctx.Done():
		t.Fatal("cold listener did not recover")
	}
	write("d")
	expectRows("a", "b", "c", "d")

	// Interrupt a SELECT already dispatched to the SQL pool. The LISTEN
	// session stays healthy, so a fresh notification cannot mask a lost read.
	writer, err := b.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	if err := writer.Exec(ctx, "LOCK TABLE public.account IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Exec(ctx, "INSERT INTO public.account VALUES ('e')"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Query(ctx, "SELECT pg_notify('public_live_query','[\"account\"]')"); err != nil {
		t.Fatal(err)
	}
	for {
		blocked, err := admin.Query(ctx, "SELECT pg_terminate_backend(pid) AS stopped FROM pg_stat_activity WHERE datname=$1 AND wait_event_type='Lock' AND query=$2", name, statement)
		if err != nil {
			t.Fatal(err)
		}
		if len(blocked.Rows) == 1 && blocked.Rows[0]["stopped"] == true {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("no live read reached the locked table")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expectRows("a", "b", "c", "d", "e")
	if err := b.Exec(ctx, "DROP TABLE public.account"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Query(ctx, "SELECT pg_notify('public_live_query','[\"account\"]')"); err != nil {
		t.Fatal(err)
	}
	for lines.Scan() {
		if strings.HasPrefix(lines.Text(), "data: ") {
			t.Fatalf("unexpected result after table removal: %s", lines.Text())
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatal("permanent SQL error kept retrying until the client timed out")
	}
}
