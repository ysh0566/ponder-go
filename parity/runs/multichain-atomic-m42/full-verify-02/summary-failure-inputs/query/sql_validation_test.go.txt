package query

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Supplementary actual backend coverage of the public SQL validator path.
func TestSQLValidationHTTP(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			var backend store.Backend
			var err error
			if dialect == "postgres" {
				address := os.Getenv("PONDER_TEST_POSTGRES")
				if address == "" {
					t.Fatal("Explicit task-only PostgreSQL URL required")
				}
				backend, err = store.OpenPostgres(ctx, address)
			} else {
				backend, err = store.OpenPGlite(ctx, "memory://")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			namespace := fmt.Sprintf("ponder_go_sql_http_%x", time.Now().UnixNano())
			s, err := store.New(ctx, backend, schema.Schema{}, namespace, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				for _, name := range []string{s.Namespace, s.SyncNamespace} {
					if err := backend.Exec(ctx, "DROP SCHEMA "+schema.Quote(name)+" CASCADE"); err != nil {
						t.Error(err)
					}
				}
			}()
			server := New(s)
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			client := Client{BaseURL: httpServer.URL + "/sql"}
			valid := "SELECT 1 AS value /*" + strings.Repeat("😀", 2_000) + "*/"
			result, err := client.Query(ctx, valid)
			if err != nil || len(result.Rows) != 1 || fmt.Sprint(result.Rows[0]["value"]) != "1" {
				t.Fatalf("UTF16-bounded public SQL must reach the actual database: %+v %v", result, err)
			}
			responseBody := func(sql string) (int, string, error) {
				encoded, err := json.Marshal(map[string]any{"json": request{SQL: sql}})
				if err != nil {
					return 0, "", err
				}
				response, err := http.Get(httpServer.URL + "/sql/db?sql=" + url.QueryEscape(string(encoded)))
				if err != nil {
					return 0, "", err
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				return response.StatusCode, string(body), err
			}
			bad := "SELECT blow_up() /*" + namespace + "*/"
			for _, message := range []string{"Function call not supported", "Invalid query"} {
				if status, body, err := responseBody(bad); err != nil || status != 500 || !strings.Contains(body, message) {
					t.Fatalf("Public cached diagnostic %q: status=%d body=%q error=%v", message, status, body, err)
				}
			}
			tooLong := "SELECT 1 /*" + strings.Repeat("😀", 2_500) + "*/"
			if status, body, err := responseBody(tooLong); err != nil || status != 500 || !strings.Contains(body, "Invalid query") {
				t.Fatalf("Public SQL cap: status=%d body=%q error=%v", status, body, err)
			}
		})
	}
}
