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

	gql "github.com/ysh0566/ponder-go/third_party/graphqlgo"

	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type sourceCase struct {
	Relations   []Relation
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
		FilterFields        map[string]string
		SelectCalls         map[string]int
		Errors              []string
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
	if len(c.Schema.Views) > 0 {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema.Quote(published)+",pg_catalog"); err != nil {
			t.Fatal(err)
		}
		for _, v := range c.Schema.Views {
			cols := make([]string, len(v.Fields))
			for i, f := range v.Fields {
				cols[i] = schema.Quote(f.Name)
			}
			if err := tx.Exec(ctx, "CREATE VIEW "+q+schema.Quote(v.Name)+" ("+strings.Join(cols, ",")+") AS "+v.Query); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
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
	opts.Relations = c.Relations
	s, err := New(st, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSourceGraphQL(t *testing.T) {
	runSourceGraphQL(t, "entities-http.json")
}

func TestSourceGraphQLTablePages(t *testing.T) {
	runSourceGraphQL(t, "table-pages.json")
}

func TestSourceGraphQLRelations(t *testing.T) {
	runSourceGraphQL(t, "relations-views.json")
}

func runSourceGraphQL(t *testing.T, fixture string) {
	t.Helper()
	b, err := os.ReadFile("../testdata/graphql/" + fixture)
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
						if req.FilterFields != nil {
							typ, ok := s.schema.Type("tableFilter").(*gql.InputObject)
							if !ok {
								t.Fatal("missing tableFilter")
							}
							actual := map[string]string{}
							for key, field := range typ.Fields() {
								actual[key] = field.Type.String()
							}
							if !reflect.DeepEqual(actual, req.FilterFields) {
								t.Fatalf("filter fields = %v, want %v", actual, req.FilterFields)
							}
							continue
						}
						if req.Method == "" {
							var counter *selectCounter
							if req.SelectCalls != nil {
								counter = &selectCounter{Backend: db, namespace: s.namespace, counts: map[string]int{}}
								for name := range req.SelectCalls {
									counter.counts[name] = 0
								}
								s.database.Backend = counter
							}
							result := s.Execute(t.Context(), Request{Query: req.Query})
							if counter != nil {
								s.database.Backend = db
								if !reflect.DeepEqual(counter.counts, req.SelectCalls) {
									t.Fatalf("SELECT counts: got %v, want %v", counter.counts, req.SelectCalls)
								}
							}
							var errors []string
							for _, e := range result.Errors {
								errors = append(errors, e.Message)
							}
							if !reflect.DeepEqual(errors, req.Errors) {
								t.Fatalf("query %s: errors %v, want %v", req.Query, errors, req.Errors)
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
