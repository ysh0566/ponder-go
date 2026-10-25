package query

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"runtime"
	"testing"
	"time"
)

func TestOriginalClientCompatibility(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, relation := range []string{"account", "account_nested"} {
			t.Run(dialect+"/"+relation, func(t *testing.T) {
				ctx := context.Background()
				var backend store.Backend
				var e error
				if dialect == "postgres" {
					url := os.Getenv("PONDER_TEST_POSTGRES")
					if url == "" {
						t.Skip("explicit task PostgreSQL URL required; not a parity pass")
					}
					backend, e = store.OpenPostgres(ctx, url)
				} else {
					backend, e = store.OpenPGlite(ctx, "memory://")
				}
				if e != nil {
					t.Fatal(e)
				}
				defer backend.Close()
				model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint"}}}}}
				if relation != "account" {
					cols := []schema.ViewField{}
					for _, column := range model.Tables[0].Columns {
						c := column
						cols = append(cols, schema.ViewField{Name: c.Name, Column: &c})
					}
					model.Views = []schema.View{{Name: "account_nested", Fields: cols, Query: `SELECT * FROM account_view`}, {Name: "account_view", Fields: cols, Query: `SELECT * FROM account`}}
				}
				s, e := store.New(ctx, backend, model, fmt.Sprintf("ponder_go_http_%x", time.Now().UnixNano()), schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				var n uint64 = 1
				apply := func(balance string) error {
					return s.Apply(ctx, store.Event{ID: fmt.Sprint(n), Checkpoint: checkpoint.Checkpoint{BlockTimestamp: n, ChainID: 1, BlockNumber: n, EventType: 5}}, func(w *store.Writer) error {
						if relation != "account" {
							_, e := w.SQL(ctx, `INSERT INTO account (id,balance) VALUES ($1,$2) ON CONFLICT(id) DO UPDATE SET balance=excluded.balance`, "alice", balance)
							return e
						}
						return w.Write(ctx, "account", store.Row{"id": "alice", "balance": balance}, store.Upsert)
					})
				}
				if e = apply("7"); e != nil {
					t.Fatal(e)
				}
				server := New(s)
				server.Ready.Store(true)
				server.API = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/test/change" {
						http.NotFound(w, r)
						return
					}
					n++
					if e := apply(r.URL.Query().Get("balance")); e != nil {
						http.Error(w, e.Error(), 500)
						return
					}
					writeJSON(w, 200, map[string]bool{"ok": true})
				})
				httpServer := httptest.NewServer(server)
				defer httpServer.Close()
				_, file, _, _ := runtime.Caller(0)
				project := filepath.Dir(filepath.Dir(file))
				// Environment-adapted launch only: preserve the same TypeScript test,
				// original seven assertions and real HTTP/SSE/database interactions.
				cmd := exec.Command("node", filepath.Join(project, "tools/client-compat-launch.mjs"), httpServer.URL, relation)
				out, e := cmd.CombinedOutput()
				if e != nil {
					t.Fatalf("original client: %v\n%s", e, out)
				}
				t.Log(string(out))
				client := Client{BaseURL: httpServer.URL + "/sql"}
				result, e := client.Query(ctx, "SELECT id,balance FROM account")
				if e != nil || len(result.Rows) != 1 || fmt.Sprint(result.Rows[0]["balance"]) != "9" {
					t.Fatalf("Go client: %v %v", result, e)
				}
				for _, sql := range []string{"DELETE FROM account", "SELECT * FROM pg_stat_activity", "SELECT pg_read_file('/etc/passwd')", "SELECT * FROM other.account", "SELECT 1; SELECT 2", "WITH q AS (DELETE FROM account RETURNING *) SELECT * FROM q"} {
					if e := serverTestQuery(ctx, client, sql); e == nil {
						t.Fatalf("accepted forbidden query %s", sql)
					}
				}
			})
		}
	}
}
func serverTestQuery(ctx context.Context, c Client, sql string) error {
	_, e := c.Query(ctx, sql)
	return e
}

func TestSuperJSON(t *testing.T) {
	q, e := decodeQuery(`{"json":{"sql":"SELECT $1","params":["123","2024-01-01T00:00:00Z",null]},"meta":{"values":{"params.0":["bigint"],"params.1":["Date"],"params.2":["undefined"]}}}`)
	if e != nil || q.Params[0] != "123" || q.Params[2] != nil {
		t.Fatalf("%v %v", q, e)
	}
	if _, e = decodeQuery(`{"json":{"sql":"SELECT $1","params":[]},"meta":{"values":{"params.7":["bigint"]}}}`); e == nil {
		t.Fatal("accepted invalid metadata")
	}
}
