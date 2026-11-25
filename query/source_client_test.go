package query

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// Own the whole database so the source's literal public and mixed-case
// namespaces can be exercised without sharing a table with any other test.
func sourceClientDB(t *testing.T, dialect string) store.Backend {
	t.Helper()
	ctx := t.Context()
	if dialect == "pglite" {
		b, err := store.OpenPGlite(ctx, "memory://")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close() })
		return b
	}
	address := os.Getenv("PONDER_TEST_POSTGRES")
	if address == "" {
		t.Skip("requires task PostgreSQL")
	}
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := store.OpenPostgres(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("ponder_go_client_%x", time.Now().UnixNano())
	if err = admin.Exec(ctx, "CREATE DATABASE "+schema.Quote(name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(context.Background(), "DROP DATABASE "+schema.Quote(name)); err != nil {
			t.Error(err)
		}
	})
	u.Path = "/" + name
	b, err := store.OpenPostgres(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

type countTransactions struct {
	store.Backend
	count atomic.Int64
}

func (b *countTransactions) Begin(ctx context.Context) (store.Transaction, error) {
	b.count.Add(1)
	return b.Backend.Begin(ctx)
}

func sourceQuery(t *testing.T, s *Server, statement string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"json": map[string]any{"sql": statement, "params": []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/sql/db?sql="+url.QueryEscape(string(raw)), nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestSourceSQLClient(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			b := sourceClientDB(t, dialect)
			cases := []string{"client.db", "client.db error", "client.db search_path", "client.db readonly", "client.db recursive", "client.db load", "client.db cache", "client.live releases quota when request fails before streaming"}
			for _, name := range cases {
				if name == "client.db cache" && dialect == "pglite" {
					continue
				} // The source's transaction spy is PostgreSQL-only.
				t.Run(name, func(t *testing.T) {
					ctx := t.Context()
					ns := "public"
					if name == "client.db search_path" {
						ns = "Ponder"
						if err := b.Exec(ctx, `CREATE SCHEMA "Ponder"`); err != nil {
							t.Fatal(err)
						}
					}
					var model schema.Schema
					if name != "client.db error" && !strings.HasPrefix(name, "client.live") {
						model.Tables = []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "text", Primary: true}, {Name: "balance", Type: "numeric"}}}}
						table := schema.Quote(ns) + ".account"
						if err := b.Exec(ctx, "CREATE TABLE "+table+" (address text PRIMARY KEY,balance numeric)"); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if err := b.Exec(context.Background(), "DROP TABLE "+table); err != nil {
								t.Error(err)
							}
						})
					}
					storeNS := fmt.Sprintf("ponder_go_client_%x", time.Now().UnixNano())
					st, err := store.New(ctx, b, model, storeNS, schema.Multichain)
					if err != nil {
						t.Fatal(err)
					}
					lifecycleNS := fmt.Sprintf("ponder_go_ready_%x", time.Now().UnixNano())
					owner, err := store.NewLifecycle(b, model, lifecycleNS, schema.Multichain, store.LifecycleOptions{})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { owner.Close() })
					if _, err = owner.Migrate(ctx, store.Migration{BuildID: "client"}); err != nil {
						t.Fatal(err)
					}
					s, err := NewAtNamespace(ctx, st, ns)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := s.Close(); err != nil {
							t.Error(err)
						}
					})
					s.Readiness = owner
					switch name {
					case "client.db":
						rec := sourceQuery(t, s, "SELECT * FROM account")
						var rows store.Result
						if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rows) != nil || rows.Rows == nil || len(rows.Rows) != 0 {
							t.Fatal(rec.Code, rec.Body.String())
						}
						if rec := sourceQuery(t, s, "SELECT 1;"); rec.Code != 200 {
							t.Fatal(rec.Code, rec.Body.String())
						}
					case "client.db error":
						rec := sourceQuery(t, s, "SELECT * FROM account")
						if rec.Code != 500 || !strings.Contains(rec.Body.String(), `relation "account" does not exist`) {
							t.Fatal(rec.Code, rec.Body.String())
						}
					case "client.db search_path":
						if rec := sourceQuery(t, s, "SELECT * FROM account"); rec.Code != 200 {
							t.Fatal(rec.Code, rec.Body.String())
						}
					case "client.db readonly", "client.db recursive":
						statement := "INSERT INTO account (address, balance) VALUES ('0x123', 100)"
						want := "InsertStmt not supported"
						if name == "client.db recursive" {
							statement = `
WITH RECURSIVE infinite_cte AS (
  SELECT 1 AS num
  UNION ALL
  SELECT num + 1
  FROM infinite_cte
)
SELECT *
FROM infinite_cte;`
							want = "Recursive CTEs not supported"
						}
						rec := sourceQuery(t, s, statement)
						if rec.Code != 500 || !strings.Contains(rec.Body.String(), want) {
							t.Fatal(rec.Code, rec.Body.String())
						}
					case "client.db load":
						// new Array(250).map skips holes, so the original starts no requests.
						slots := make([]*int, 250)
						var requests sync.WaitGroup
						for _, i := range slots {
							if i == nil {
								continue
							}
							requests.Add(1)
							go func() { defer requests.Done(); sourceQuery(t, s, fmt.Sprintf("SELECT %d", *i)) }()
						}
						requests.Wait()
					case "client.db cache":
						if err = owner.MarkReady(ctx); err != nil {
							t.Fatal(err)
						}
						time.Sleep(1200 * time.Millisecond)
						// Install the spy after setup, as in the original. Both the
						// metadata reader and LISTEN session retain the original backend.
						counted := &countTransactions{Backend: b}
						st.Backend = counted
						results := make(chan int, 3)
						start := make(chan struct{})
						for range 3 {
							go func() { <-start; results <- sourceQuery(t, s, "SELECT 1").Code }()
						}
						close(start)
						for range 3 {
							if status := <-results; status != 200 {
								t.Fatal(status)
							}
						}
						if n := counted.count.Load(); n != 1 {
							t.Fatalf("%d database transactions", n)
						}
					default:
						if err = owner.MarkReady(ctx); err != nil {
							t.Fatal(err)
						}
						call := func() int {
							rec := httptest.NewRecorder()
							s.ServeHTTP(rec, httptest.NewRequest("GET", "/sql/live", nil))
							return rec.Code
						}
						status := call()
						for i := 0; i < 20 && status != 400; i++ {
							time.Sleep(50 * time.Millisecond)
							status = call()
						}
						if status != 400 {
							t.Fatal(status)
						}
						for range 1000 {
							if n := call(); n != 400 {
								t.Fatal(n)
							}
						}
					}
				})
			}
		})
	}
}

// Exercise the actual transaction path even when the original sparse array
// dispatches zero queries. Every returned value belongs to its own SQL key.
func TestSQLClientDenseLoad(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			b := sourceClientDB(t, dialect)
			st, err := store.New(t.Context(), b, schema.Schema{}, fmt.Sprintf("ponder_go_dense_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			s := New(st)
			s.Ready.Store(true)
			var workers sync.WaitGroup
			limit := make(chan struct{}, 12)
			for i := range 250 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					limit <- struct{}{}
					defer func() { <-limit }()
					rec := sourceQuery(t, s, fmt.Sprintf("SELECT %d AS value", i))
					var r store.Result
					if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &r) != nil || len(r.Rows) != 1 || fmt.Sprint(r.Rows[0]["value"]) != fmt.Sprint(i) {
						t.Errorf("query %d: %d %s", i, rec.Code, rec.Body.String())
					}
				}()
			}
			workers.Wait()
		})
	}
}

// Keep a strong reference while verifying invalidation; GC is independently
// permitted to discard the completed cached result at any point.
func TestSQLNotificationInvalidation(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			b := sourceClientDB(t, dialect)
			ctx := t.Context()
			model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
			st, err := store.New(ctx, b, model, fmt.Sprintf("ponder_go_watch_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Exec(ctx, "CREATE TABLE public.account (id text PRIMARY KEY)"); err != nil {
				t.Fatal(err)
			}
			s, err := NewAtNamespace(ctx, st, "public")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			watch, detach := s.changes().Subscribe()
			defer detach()
			q := request{SQL: "SELECT id FROM account"}
			read := func() *store.Result {
				r, err := s.queryResult(ctx, "account-query", q, []string{"account"}, true)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			first := read()
			if len(first.Rows) != 0 || read() != first {
				t.Fatal("result not reused")
			}
			tx, err := b.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if err := tx.Exec(ctx, "INSERT INTO public.account VALUES ('alice')"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Query(ctx, "SELECT pg_notify($1,$2)", "public_live_query", `["account"]`); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-watch:
			case <-time.After(3 * time.Second):
				t.Fatal("no commit notification")
			}
			if got := read(); got == first || len(got.Rows) != 1 || got.Rows[0]["id"] != "alice" {
				t.Fatalf("stale result %#v", got)
			}
			runtime.KeepAlive(first)
		})
	}
}
