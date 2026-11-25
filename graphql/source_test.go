package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type sourceCase struct {
	Schema      schema.Schema
	Rows        map[string]json.RawMessage
	Checkpoints []struct {
		Name      string `json:"chain_name"`
		ID        string `json:"chain_id"`
		Latest    string `json:"latest_checkpoint"`
		Finalized string `json:"finalized_checkpoint"`
		Safe      string `json:"safe_checkpoint"`
	}
	Requests []struct {
		Method, Path, Query string
		Data                any
		Status              int
		Response            struct {
			Data   any
			Errors []struct{ Message string }
		}
	}
}

// Restore the captured source relation contents into a separate published schema.
// Reading through the normal handler crosses a real read-only transaction.
func sourceServer(t *testing.T, db store.Backend, c sourceCase, opts Options) *Server {
	t.Helper()
	ctx := t.Context()
	ns := fmt.Sprintf("ponder_go_gql_%x", time.Now().UnixNano())
	st, err := store.New(ctx, db, c.Schema, ns, schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	published := ns + "_views"
	q := schema.Quote(published) + "."
	exec := func(stmt string, args ...any) {
		t.Helper()
		if err := db.Exec(ctx, stmt, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE SCHEMA " + schema.Quote(published))
	for _, enum := range c.Schema.Enums {
		values := make([]string, len(enum.Values))
		for i, v := range enum.Values {
			values[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		}
		exec("CREATE TYPE " + q + schema.Quote(enum.Name) + " AS ENUM (" + strings.Join(values, ",") + ")")
	}
	for _, table := range c.Schema.Tables {
		exec(c.Schema.TableDDL(table, published))
		relation := q + schema.Quote(table.Name)
		exec("INSERT INTO "+relation+" SELECT * FROM json_populate_recordset(NULL::"+relation+",$1::json)", string(c.Rows[table.Name]))
	}
	exec("CREATE TABLE " + q + "_ponder_checkpoint (chain_name text,chain_id bigint, latest_checkpoint text,finalized_checkpoint text,safe_checkpoint text)")
	for _, cp := range c.Checkpoints {
		exec("INSERT INTO "+q+"_ponder_checkpoint VALUES ($1,$2,$3,$4,$5)", cp.Name, cp.ID, cp.Latest, cp.Finalized, cp.Safe)
	}
	t.Cleanup(func() {
		for _, name := range []string{published, st.SyncNamespace, st.Namespace} {
			if err := db.Exec(context.Background(), "DROP SCHEMA "+schema.Quote(name)+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
	})
	opts.Namespace = published
	s, err := New(st, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSourceGraphQL(t *testing.T) {
	b, err := os.ReadFile("../testdata/graphql/entities-http.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string]sourceCase
	if err = json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			db := testBackend(t, dialect)
			for _, name := range names {
				t.Run(name, func(t *testing.T) {
					c := cases[name]
					var opts Options
					switch name {
					case "graphQLMiddleware throws error for token limit":
						opts.MaxOperationTokens = 3
					case "graphQLMiddleware throws error for depth limit":
						opts.MaxOperationDepth = 5
					case "graphQLMiddleware throws error for max aliases", "graphQLMiddleware interactive":
						opts.MaxOperationAliases = 2
					}
					s := sourceServer(t, db, c, opts)
					for _, req := range c.Requests {
						if req.Method == "" {
							result := s.Execute(t.Context(), Request{Query: req.Query})
							if result.HasErrors() {
								t.Fatal(result.Errors)
							}
							compareJSON(t, result.Data, req.Data)
							continue
						}
						mux := http.NewServeMux()
						pattern := req.Path
						if pattern == "/not-graphql/at-all" {
							pattern = "/not-graphql/"
						}
						mux.Handle(pattern, s)
						body, _ := json.Marshal(Request{Query: req.Query})
						r := httptest.NewRequest(req.Method, req.Path, strings.NewReader(string(body)))
						r.Header.Set("Content-Type", "application/json")
						w := httptest.NewRecorder()
						mux.ServeHTTP(w, r)
						if w.Code != req.Status {
							t.Fatal(w.Code, w.Body.String())
						}
						if req.Method == "GET" {
							if !strings.Contains(w.Body.String(), "GraphiQL.createFetcher") {
								t.Fatal("playground unavailable")
							}
							continue
						}
						var got struct {
							Data   any
							Errors []struct{ Message string }
						}
						if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got.Errors, req.Response.Errors) {
							t.Fatalf("errors = %v want %v", got.Errors, req.Response.Errors)
						}
						compareJSON(t, got.Data, req.Response.Data)
					}
				})
			}
		})
	}
}

func compareJSON(t *testing.T, actual, expected any) {
	t.Helper()
	b, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("got %s; want %v", b, expected)
	}
}

func testBackend(t *testing.T, dialect string) store.Backend {
	t.Helper()
	// Keep the database process alive until the tests have dropped their schemas.
	lifetime, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var db store.Backend
	var err error
	if dialect == "postgres" {
		dsn := os.Getenv("PONDER_TEST_POSTGRES")
		if dsn == "" {
			t.Skip("requires task PostgreSQL")
		}
		db, err = store.OpenPostgres(lifetime, dsn)
	} else {
		db, err = store.OpenPGlite(lifetime, "memory://")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
