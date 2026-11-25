package store

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

func sourceSyncSQL(t *testing.T, m *SyncMigrator, sql string, args ...any) Result {
	t.Helper()
	r, e := m.backend.Query(context.Background(), sql, args...)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func expectSourceRows(t *testing.T, got Result, want []map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(got.Rows, want) {
		t.Fatalf("source exact rows: got %#v want %#v", got.Rows, want)
	}
}

func TestUpstreamSyncMigration(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"migrateSync() creates only the latest sync schema", "migrateSync() no-ops on repeated startup", "migrateSync() handles concurrent migrations"} {
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					b := sourceBackend(t, ctx, dialect)
					m, e := NewSyncMigrator(b, fmt.Sprintf("ponder_go_sync_%x", time.Now().UnixNano()))
					if e != nil {
						t.Fatal(e)
					}
					cached := CachedRPCResult{ChainID: 1, RequestHash: "cached-request", BlockNumber: int64Pointer(123), Result: "0x1234"}
					switch name {
					case "migrateSync() creates only the latest sync schema":
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						r := sourceSyncSQL(t, m, "SELECT table_name FROM information_schema.tables WHERE table_schema=$1", m.LatestSchema())
						names := []string{}
						for _, row := range r.Rows {
							names = append(names, fmt.Sprint(row["table_name"]))
						}
						sort.Strings(names)
						if !reflect.DeepEqual(names, []string{"blocks", "factories", "factory_addresses", "intervals", "logs", "rpc_request_results", "traces", "transaction_receipts", "transactions"}) {
							t.Fatalf("source sorted latest tables assertion: %#v", names)
						}
						expectSourceRows(t, sourceSyncSQL(t, m, "SELECT 1 FROM information_schema.schemata WHERE schema_name=$1", m.scope+"_legacy"), []map[string]any{})
					case "migrateSync() no-ops on repeated startup", "migrateSync() handles concurrent migrations":
						if name == "migrateSync() handles concurrent migrations" {
							results := make(chan error, 2)
							for i := 0; i < 2; i++ {
								go func() { results <- m.Migrate(ctx) }()
							}
							for i := 0; i < 2; i++ {
								if e = <-results; e != nil {
									t.Fatal(e)
								}
							}
						} else if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						if e = m.InsertRPCResult(ctx, cached); e != nil {
							t.Fatal(e)
						}
						if name != "migrateSync() handles concurrent migrations" {
							if e = m.Migrate(ctx); e != nil {
								t.Fatal(e)
							}
						}
						got, e := m.RPCResults(ctx)
						if e != nil {
							t.Fatal(e)
						}
						if !reflect.DeepEqual(got, []CachedRPCResult{cached}) {
							t.Fatalf("source cached result equality: %#v", got)
						}
					}
				})
			}
		})
	}
}
func int64Pointer(value int64) *int64 { return &value }
