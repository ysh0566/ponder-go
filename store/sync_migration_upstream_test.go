package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

//go:embed testdata/legacy_ponder_sync.sql
var legacySyncFixture string

func syncFixtureSQL(m *SyncMigrator, sql string) string {
	sql = strings.ReplaceAll(sql, `"ponder_sync"`, schema.Quote(m.LegacySchema()))
	sql = strings.ReplaceAll(sql, "ponder_sync.", schema.Quote(m.LegacySchema())+".")
	sql = strings.ReplaceAll(sql, "ponder_rpc_cache_1.", schema.Quote(m.LatestSchema())+".")
	return sql
}
func sourceSyncSQL(t *testing.T, m *SyncMigrator, sql string) Result {
	t.Helper()
	r, e := m.backend.Query(context.Background(), syncFixtureSQL(m, sql))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func sourceLegacySync(t *testing.T, m *SyncMigrator) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range strings.Split(legacySyncFixture, "--> statement-breakpoint") {
		if strings.TrimSpace(statement) != "" {
			if e := m.backend.Exec(ctx, syncFixtureSQL(m, statement)); e != nil {
				t.Fatal(e)
			}
		}
	}
	// Extra native ownership comment does not alter the original fixture rows,
	// constraints, migration history or copy SQL. No external schema is adopted.
	if e := m.backend.Exec(ctx, "COMMENT ON SCHEMA "+schema.Quote(m.LegacySchema())+" IS '"+m.legacyOwner()+"'"); e != nil {
		t.Fatal(e)
	}
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
			for _, name := range []string{"migrateSync() creates only the latest sync schema", "migrateSync() no-ops on repeated startup", "migrateSync() accepts the latest legacy migration", "migrateSync() excludes trace and transfer intervals from ponder_sync", "migrateSync() excludes factories, child addresses, and factory intervals from ponder_sync", "migrateSync() rejects outdated legacy migrations", "migrateSync() handles concurrent migrations", "migrateSync() copies ponder_sync from one snapshot"} {
				if dialect == "pglite" && name == "migrateSync() copies ponder_sync from one snapshot" {
					continue
				} // original returns before its PG-only fixture/assertion
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
						r := sourceSyncSQL(t, m, "SELECT table_name FROM information_schema.tables WHERE table_schema='"+m.LatestSchema()+"'")
						names := []string{}
						for _, row := range r.Rows {
							names = append(names, fmt.Sprint(row["table_name"]))
						}
						sort.Strings(names)
						if !reflect.DeepEqual(names, []string{"blocks", "factories", "factory_addresses", "intervals", "logs", "rpc_request_results", "traces", "transaction_receipts", "transactions"}) {
							t.Fatalf("source sorted latest tables assertion: %#v", names)
						}
						expectSourceRows(t, sourceSyncSQL(t, m, "SELECT 1 FROM information_schema.schemata WHERE schema_name='"+m.LegacySchema()+"'"), []map[string]any{})
					case "migrateSync() no-ops on repeated startup", "migrateSync() handles concurrent migrations", "migrateSync() accepts the latest legacy migration":
						var history Result
						if name == "migrateSync() accepts the latest legacy migration" {
							sourceLegacySync(t, m)
						}
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
						if name == "migrateSync() accepts the latest legacy migration" {
							sourceSyncSQL(t, m, `UPDATE ponder_sync.kysely_migration SET timestamp='2025-02-26T00:00:00.000Z'`)
							history = sourceSyncSQL(t, m, `SELECT name,timestamp FROM ponder_sync.kysely_migration ORDER BY name`)
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
						if name == "migrateSync() accepts the latest legacy migration" {
							expectSourceRows(t, sourceSyncSQL(t, m, `SELECT name,timestamp FROM ponder_sync.kysely_migration ORDER BY name`), history.Rows)
						}
					case "migrateSync() excludes trace and transfer intervals from ponder_sync":
						sourceLegacySync(t, m)
						sourceSyncSQL(t, m, `INSERT INTO ponder_sync.intervals (fragment_id,chain_id,blocks) VALUES ('log_1_null_null_null_null_null_0',1,'{[1,10]}'),('transaction_1_null_null',1,'{[1,10]}'),('trace_1_null_null_null_0',1,'{[1,10]}'),('transfer_1_null_null_0',1,'{[1,10]}')`)
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						expectSourceRows(t, sourceSyncSQL(t, m, `SELECT fragment_id FROM ponder_rpc_cache_1.intervals ORDER BY fragment_id`), []map[string]any{{"fragment_id": "log_1_null_null_null_null_null_0"}, {"fragment_id": "transaction_1_null_null"}})
					case "migrateSync() excludes factories, child addresses, and factory intervals from ponder_sync":
						sourceLegacySync(t, m)
						sourceSyncSQL(t, m, `INSERT INTO ponder_sync.factories (id,factory) OVERRIDING SYSTEM VALUE VALUES (1,'{"type":"log","chainId":1,"address":"0xa","eventSelector":"0xe","childAddressLocation":"topic1"}')`)
						sourceSyncSQL(t, m, `INSERT INTO ponder_sync.factory_addresses (factory_id,chain_id,block_number,address) VALUES (1,1,6,'0x1')`)
						sourceSyncSQL(t, m, `INSERT INTO ponder_sync.intervals (fragment_id,chain_id,blocks) VALUES ('factory_log_1_0xa_0xe_topic1_null_null',1,'{[5,10]}'),('log_1_0xa_0xe_topic1_0xf_null_null_null_0',1,'{[5,10]}'),('log_1_null_0xe_offset0_0xf_null_null_null_0',1,'{[5,10]}'),('transaction_1_0xa_0xe_topic1_null',1,'{[5,10]}'),('transaction_1_0xb_0xa_0xe_topic1',1,'{[5,10]}'),('log_1_0xa_0xf_null_null_null_0',1,'{[0,10]}'),('transaction_1_0xa_0xb',1,'{[0,10]}')`)
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						expectSourceRows(t, sourceSyncSQL(t, m, `SELECT fragment_id FROM ponder_rpc_cache_1.intervals ORDER BY fragment_id`), []map[string]any{{"fragment_id": "log_1_0xa_0xf_null_null_null_0"}, {"fragment_id": "transaction_1_0xa_0xb"}})
						expectSourceRows(t, sourceSyncSQL(t, m, `SELECT id FROM ponder_rpc_cache_1.factories`), []map[string]any{})
						expectSourceRows(t, sourceSyncSQL(t, m, `SELECT address FROM ponder_rpc_cache_1.factory_addresses`), []map[string]any{})
					case "migrateSync() rejects outdated legacy migrations":
						sourceSyncSQL(t, m, `CREATE SCHEMA "`+m.LegacySchema()+`"`)
						if e = b.Exec(ctx, "COMMENT ON SCHEMA "+schema.Quote(m.LegacySchema())+" IS '"+m.legacyOwner()+"'"); e != nil {
							t.Fatal(e)
						}
						sourceSyncSQL(t, m, `CREATE TABLE ponder_sync.kysely_migration (name text PRIMARY KEY)`)
						sourceSyncSQL(t, m, `INSERT INTO ponder_sync.kysely_migration VALUES ('2025_02_26_0_factories')`)
						var migration *MigrationError
						e = m.Migrate(ctx)
						if !errors.As(e, &migration) || e.Error() != `"ponder_sync" migration failed. Please first update to v0.10 or "DROP SCHEMA ponder_sync CASCADE".` {
							t.Fatalf("source MigrationError exact message: %T %v", e, e)
						}
						expectSourceRows(t, sourceSyncSQL(t, m, `SELECT name FROM ponder_sync.kysely_migration`), []map[string]any{{"name": "2025_02_26_0_factories"}})
					case "migrateSync() copies ponder_sync from one snapshot":
						sourceLegacySync(t, m)
						writer, e := b.Begin(ctx)
						if e != nil {
							t.Fatal(e)
						}
						defer writer.Rollback(context.Background())
						if e = writer.Exec(ctx, "LOCK TABLE "+schema.Quote(m.LegacySchema())+".intervals IN ACCESS EXCLUSIVE MODE"); e != nil {
							t.Fatal(e)
						}
						if e = writer.Exec(ctx, syncFixtureSQL(m, `INSERT INTO ponder_sync.intervals (fragment_id,chain_id,blocks) VALUES ('log_1_null_null_null_null_null_0',1,'{[1,10]}')`)); e != nil {
							t.Fatal(e)
						}
						result := make(chan error, 1)
						go func() { result <- m.Migrate(ctx) }()
						for {
							r, e := writer.Query(ctx, "SELECT 1 FROM pg_locks WHERE relation=$1::regclass AND NOT granted", schema.Quote(m.LegacySchema())+".intervals")
							if e != nil {
								t.Fatal(e)
							}
							if len(r.Rows) > 0 {
								break
							}
							select {
							case e := <-result:
								t.Fatalf("migration completed before copy lock: %v", e)
							case <-ctx.Done():
								t.Fatal(ctx.Err())
							case <-time.After(10 * time.Millisecond):
							}
						}
						if e = writer.Commit(ctx); e != nil {
							t.Fatal(e)
						}
						if e = <-result; e != nil {
							t.Fatal(e)
						}
						expectSourceRows(t, sourceSyncSQL(t, m, `SELECT fragment_id FROM ponder_rpc_cache_1.intervals`), []map[string]any{})
					}
				})
			}
		})
	}
}
func int64Pointer(value int64) *int64 { return &value }
