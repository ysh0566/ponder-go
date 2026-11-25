package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/schema"
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
	if tx.fault.stage == "during_create" && strings.HasPrefix(strings.TrimSpace(sql), "CREATE TABLE ") {
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
	if len(manifest.Assets) != 1 || manifest.Assets[0].Target != "store/syncsql/create.sql" {
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
			for _, name := range []string{"unowned schemas and premature writes refused", "current cache beside an older namespace", "creation rollback and retry", "committed outcome retry and nullable values", "independent concurrent migrators", "owned incomplete schema refused"} {
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
						for _, ns := range []string{m.LatestSchema(), m.scope + "_legacy"} {
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
					case "current cache beside an older namespace":
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						cached := CachedRPCResult{ChainID: 1, RequestHash: "current", Result: "0x01"}
						if e = m.InsertRPCResult(ctx, cached); e != nil {
							t.Fatal(e)
						}
						older := schema.Quote(scope + "_legacy")
						for _, statement := range []string{
							"CREATE SCHEMA " + older,
							"CREATE TABLE " + older + ".marker (value text)",
							"INSERT INTO " + older + ".marker VALUES ('original')",
						} {
							if e = b.Exec(ctx, statement); e != nil {
								t.Fatal(e)
							}
						}
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
						rows, e := m.RPCResults(ctx)
						if e != nil || len(rows) != 1 || rows[0] != cached {
							t.Fatalf("current cache changed on reopen: %v %v", rows, e)
						}
						marker, e := b.Query(ctx, "SELECT value FROM "+older+".marker")
						if e != nil || len(marker.Rows) != 1 || marker.Rows[0]["value"] != "original" {
							t.Fatalf("older namespace changed: %v %v", marker.Rows, e)
						}
					case "creation rollback and retry":
						failed, e := NewSyncMigrator(&syncFaultBackend{Backend: b, stage: "during_create"}, scope)
						if e != nil {
							t.Fatal(e)
						}
						if e = failed.Migrate(ctx); !errors.Is(e, errSyncFault) {
							t.Fatalf("DDL failure not injected: %v", e)
						}
						r, e := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", m.LatestSchema())
						if e != nil || len(r.Rows) != 0 {
							t.Fatalf("failed initialization persisted partial latest schema: %#v %v", r, e)
						}
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
					case "committed outcome retry and nullable values":
						uncertain, e := NewSyncMigrator(&syncFaultBackend{Backend: b, stage: "after_commit"}, scope)
						if e != nil {
							t.Fatal(e)
						}
						if e = uncertain.Migrate(ctx); !errors.Is(e, errSyncFault) {
							t.Fatalf("commit acknowledgment failure: %v", e)
						}
						if e = m.InsertRPCResult(ctx, CachedRPCResult{ChainID: 1, RequestHash: "nullable", Result: "opaque界\n\\"}); e != nil {
							t.Fatal(e)
						}
						if e = m.Migrate(ctx); e != nil {
							t.Fatal(e)
						}
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
