package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

type syncFaultBackend struct {
	Backend
	stage     string
	terminate func()
}
type syncFaultTx struct {
	Transaction
	fault *syncFaultBackend
}

var errSyncFault = errors.New("injected sync migration persistence failure")

func (b *syncFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &syncFaultTx{Transaction: tx, fault: b}, nil
}
func (tx *syncFaultTx) trip() error {
	if tx.fault.terminate != nil {
		tx.fault.terminate()
	}
	return errSyncFault
}
func (tx *syncFaultTx) Exec(ctx context.Context, sql string, args ...any) error {
	if e := tx.Transaction.Exec(ctx, sql, args...); e != nil {
		return e
	}
	if tx.fault.stage == "during_copy" && strings.HasPrefix(sql, "\nINSERT INTO ") && strings.Contains(sql, `."intervals" `) {
		return tx.trip()
	}
	return nil
}
func (tx *syncFaultTx) Commit(ctx context.Context) error {
	if e := tx.Transaction.Commit(ctx); e != nil {
		return e
	}
	if tx.fault.stage == "after_commit" {
		return tx.trip()
	}
	return nil
}

// Every copied relation is populated, including exact numeric and nullable,
// Unicode and opaque-text values. Legacy-only traces remain deliberately uncopied.
func seedSyncCopyRows(t *testing.T, m *SyncMigrator) {
	t.Helper()
	sourceLegacySync(t, m)
	sourceSyncSQL(t, m, `INSERT INTO ponder_sync.blocks(chain_id,number,timestamp,hash,parent_hash,logs_bloom,miner,gas_used,gas_limit,state_root,receipts_root,transactions_root,size,difficulty,extra_data) VALUES(1,123,456,'0xblock','0xparent','0xbloom','0xminer',123456789012345678901234567890,999,'0xstate','0xreceipts','0xtxs',1,0,E'opaque界\n\\')`)
	sourceSyncSQL(t, m, `INSERT INTO ponder_sync.transactions(chain_id,block_number,transaction_index,hash,block_hash,"from","to",input,value,nonce,type,gas) VALUES(1,123,2,'0xtx','0xblock','0xfrom',NULL,'0xinput',123456789012345678901234567890,0,'0x2',21000)`)
	sourceSyncSQL(t, m, `INSERT INTO ponder_sync.transaction_receipts(chain_id,block_number,transaction_index,transaction_hash,block_hash,"from",logs_bloom,gas_used,cumulative_gas_used,effective_gas_price,status,type) VALUES(1,123,2,'0xtx','0xblock','0xfrom','0xbloom',21000,22000,3,'0x1','0x2')`)
	sourceSyncSQL(t, m, `INSERT INTO ponder_sync.logs(chain_id,block_number,log_index,transaction_index,block_hash,transaction_hash,address,topic1,data) VALUES(1,123,4,2,'0xblock','0xtx','0xcontract','0xtopic',E'界"\n\\')`)
	sourceSyncSQL(t, m, `INSERT INTO ponder_sync.rpc_request_results(request_hash,chain_id,block_number,result) VALUES('nullable',1,NULL,E'opaque界\n\\')`)
	sourceSyncSQL(t, m, `INSERT INTO ponder_sync.intervals(fragment_id,chain_id,blocks) VALUES('log_1_null_null_null_null_null_0',1,'{[1,10]}')`)
	sourceSyncSQL(t, m, `INSERT INTO ponder_sync.traces(chain_id,block_number,transaction_index,trace_index,"from",input,type,gas,gas_used,subcalls) VALUES(1,123,2,0,'0xfrom','0x','CALL',21000,20000,1)`)
}
func syncRowsSnapshot(t *testing.T, m *SyncMigrator, latest bool) map[string]Result {
	t.Helper()
	ns := m.LegacySchema()
	if latest {
		ns = m.LatestSchema()
	}
	out := map[string]Result{}
	for _, table := range []string{"blocks", "transactions", "transaction_receipts", "logs", "rpc_request_results", "intervals"} {
		r, e := m.backend.Query(context.Background(), "SELECT row_to_json(t)::text AS row FROM "+schema.Quote(ns)+"."+schema.Quote(table)+" t ORDER BY row_to_json(t)::text")
		if e != nil {
			t.Fatal(e)
		}
		out[table] = r
	}
	return out
}
func assertSyncCopied(t *testing.T, m *SyncMigrator, want map[string]Result) {
	t.Helper()
	if got := syncRowsSnapshot(t, m, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("copy changed column values: got %#v want %#v", got, want)
	}
	expectSourceRows(t, sourceSyncSQL(t, m, `SELECT type FROM ponder_rpc_cache_1.traces`), []map[string]any{})
}

func TestSyncMigrationSQLAssets(t *testing.T) {
	data, e := os.ReadFile("../parity/sync-migration-assets.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		Assets []struct{ Target, SHA256 string }
	}
	if e = json.Unmarshal(data, &manifest); e != nil {
		t.Fatal(e)
	}
	if len(manifest.Assets) != 3 {
		t.Fatal("missing source SQL assets")
	}
	for _, asset := range manifest.Assets {
		data, e := os.ReadFile("../" + asset.Target)
		if e != nil {
			t.Fatal(e)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != asset.SHA256 {
			t.Fatal("fixed source SQL asset changed", asset.Target)
		}
	}
}
func TestSyncMigrationBoundaries(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"unowned schemas and premature writes refused", "copy rollback and retry", "committed outcome retry and nullable values", "independent concurrent migrators", "owned incomplete schema refused"} {
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					b := sourceBackend(t, ctx, dialect)
					scope := fmt.Sprintf("ponder_go_sync_bound_%x", time.Now().UnixNano())
					m, e := NewSyncMigrator(b, scope)
					if e != nil {
						t.Fatal(e)
					}
					switch name {
					case "unowned schemas and premature writes refused":
						if e = m.InsertRPCResult(ctx, CachedRPCResult{ChainID: 1, RequestHash: "before", Result: "0x"}); e == nil {
							t.Fatal("cache write created/adopted unmigrated data")
						}
						for _, ns := range []string{m.LatestSchema(), m.LegacySchema()} {
							if e = b.Exec(ctx, "CREATE SCHEMA "+schema.Quote(ns)); e != nil {
								t.Fatal(e)
							}
							if e = m.Migrate(ctx); e == nil {
								t.Fatal("unowned schema adopted", ns)
							}
							r, e := b.Query(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema=$1", ns)
							if e != nil || len(r.Rows) != 0 {
								t.Fatalf("unowned schema modified: %#v %v", r, e)
							}
							if e = b.Exec(ctx, "DROP SCHEMA "+schema.Quote(ns)); e != nil {
								t.Fatal(e)
							}
						}
					case "copy rollback and retry":
						seedSyncCopyRows(t, m)
						before := syncRowsSnapshot(t, m, false)
						history := sourceSyncSQL(t, m, `SELECT * FROM ponder_sync.kysely_migration ORDER BY name`)
						failed, e := NewSyncMigrator(&syncFaultBackend{Backend: b, stage: "during_copy"}, scope)
						if e != nil {
							t.Fatal(e)
						}
						if e = failed.Migrate(ctx); !errors.Is(e, errSyncFault) {
							t.Fatalf("copy failure not injected: %v", e)
						}
						r, e := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", m.LatestSchema())
						if e != nil || len(r.Rows) != 0 {
							t.Fatalf("failed copy persisted partial latest schema: %#v %v", r, e)
						}
						if !reflect.DeepEqual(before, syncRowsSnapshot(t, m, false)) {
							t.Fatal("failed copy changed legacy rows")
						}
						expectSourceRows(t, sourceSyncSQL(t, m, `SELECT * FROM ponder_sync.kysely_migration ORDER BY name`), history.Rows)
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						assertSyncCopied(t, m, before)
					case "committed outcome retry and nullable values":
						seedSyncCopyRows(t, m)
						before := syncRowsSnapshot(t, m, false)
						uncertain, e := NewSyncMigrator(&syncFaultBackend{Backend: b, stage: "after_commit"}, scope)
						if e != nil {
							t.Fatal(e)
						}
						if e = uncertain.Migrate(ctx); !errors.Is(e, errSyncFault) {
							t.Fatalf("commit acknowledgment failure: %v", e)
						}
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						assertSyncCopied(t, m, before)
						rows, e := m.RPCResults(ctx)
						if e != nil || len(rows) != 1 || rows[0].BlockNumber != nil || rows[0].Result != "opaque界\n\\" {
							t.Fatalf("nullable RPC result: %#v %v", rows, e)
						}
						max := int64(9223372036854775807)
						if e = m.InsertRPCResult(ctx, CachedRPCResult{ChainID: max, RequestHash: "max", BlockNumber: &max, Result: "0x"}); e != nil {
							t.Fatal(e)
						}
						rows, e = m.RPCResults(ctx)
						if e != nil || len(rows) != 2 || rows[1].ChainID != max || rows[1].BlockNumber == nil || *rows[1].BlockNumber != max {
							t.Fatalf("int64 RPC boundary: %#v %v", rows, e)
						}
					case "independent concurrent migrators":
						results := make(chan error, 16)
						for i := 0; i < 16; i++ {
							go func() {
								peer, e := NewSyncMigrator(b, scope)
								if e == nil {
									e = peer.Migrate(ctx)
								}
								results <- e
							}()
						}
						for i := 0; i < 16; i++ {
							if e = <-results; e != nil {
								t.Fatal(e)
							}
						}
						if e = m.validateLatest(ctx, b); e != nil {
							t.Fatal(e)
						}
						life, e := NewLifecycle(b, schema.Schema{}, scope, schema.Multichain, LifecycleOptions{})
						if e != nil {
							t.Fatal(e)
						}
						if e = life.MigrateSync(ctx); e != nil {
							t.Fatal(e)
						}
						if e = life.Close(); e != nil {
							t.Fatal(e)
						}
						r, e := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", scope)
						if e != nil || len(r.Rows) != 0 {
							t.Fatalf("sync-only startup created indexing schema: %#v %v", r, e)
						}
					case "owned incomplete schema refused":
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						if e = b.Exec(ctx, "DROP TABLE "+schema.Quote(m.LatestSchema())+".transactions"); e != nil {
							t.Fatal(e)
						}
						if e = m.Migrate(ctx); e == nil {
							t.Fatal("incomplete latest schema accepted as migrated")
						}
					}
				})
			}
		})
	}
}
