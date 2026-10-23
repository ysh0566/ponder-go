package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"ponder.local/ponder-go/schema"
)

// These files retain the exact fixed-source SQL, including copy exclusions,
// ranges, numeric widths, identity sequences, constraints and indexes.
//
//go:embed syncsql/create.sql
var syncCreateSQL string

//go:embed syncsql/copy.sql
var syncCopySQL string

// SyncMigrator creates the source's versioned RPC cache in a fresh task-owned
// scope. Its optional legacy input is an explicitly owned shadow. It never opens,
// alters or adopts a real ponder_sync/ponder_rpc_cache_1 schema.
type SyncMigrator struct {
	backend Backend
	scope   string
}

func NewSyncMigrator(backend Backend, scope string) (*SyncMigrator, error) {
	if backend == nil {
		return nil, fmt.Errorf("sync migration requires a backend")
	}
	if !schema.Identifier(scope) || !strings.HasPrefix(scope, "ponder_go_") || len(scope) > 45 {
		return nil, fmt.Errorf("sync migration scope must be a ponder_go_ identifier of at most 45 characters")
	}
	return &SyncMigrator{backend: backend, scope: scope}, nil
}
func (m *SyncMigrator) LegacySchema() string { return m.scope + "_legacy" }
func (m *SyncMigrator) LatestSchema() string { return m.scope + "_rpc_1" }
func (m *SyncMigrator) owner(kind string) string {
	return "ponder-go:sync-migration:17c6d5ba:v1:" + m.scope + ":" + kind
}
func (m *SyncMigrator) legacyOwner() string { return m.owner("legacy-shadow") }
func (m *SyncMigrator) latestOwner() string { return m.owner("ponder_rpc_cache_1") }
func (m *SyncMigrator) remap(sql string) string {
	sql = strings.ReplaceAll(sql, `"ponder_sync"`, schema.Quote(m.LegacySchema()))
	return strings.ReplaceAll(sql, `"ponder_rpc_cache_1"`, schema.Quote(m.LatestSchema()))
}
func (m *SyncMigrator) owned(ctx context.Context, q Querier, name, owner string) (bool, error) {
	r, e := q.Query(ctx, "SELECT obj_description(oid,'pg_namespace') AS owner FROM pg_namespace WHERE nspname=$1", name)
	if e != nil {
		return false, e
	}
	if len(r.Rows) == 0 {
		return false, nil
	}
	if r.Rows[0]["owner"] != owner {
		return false, &MigrationError{Message: fmt.Sprintf("refusing to adopt unowned sync schema %q", name)}
	}
	return true, nil
}
func (m *SyncMigrator) validateLatest(ctx context.Context, q Querier) error {
	r, e := q.Query(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema=$1 AND table_type='BASE TABLE' ORDER BY table_name", m.LatestSchema())
	if e != nil {
		return e
	}
	names := []string{}
	for _, row := range r.Rows {
		names = append(names, fmt.Sprint(row["table_name"]))
	}
	if !reflect.DeepEqual(names, []string{"blocks", "factories", "factory_addresses", "intervals", "logs", "rpc_request_results", "traces", "transaction_receipts", "transactions"}) {
		return &MigrationError{Message: "owned versioned sync schema is incomplete; use a new shadow"}
	}
	return nil
}
func (m *SyncMigrator) lock(ctx context.Context, tx Transaction) error {
	if m.backend.Dialect() == "postgres" {
		return tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", m.scope+":sync-migration")
	}
	return nil
}
func (m *SyncMigrator) Migrate(ctx context.Context) error {
	e := m.migrateOnce(ctx)
	// Repeatable-read snapshots can predate a concurrent creator even after
	// waiting on its advisory lock. Match the source's namespace-conflict
	// recovery, then verify the committed owner's complete schema afresh.
	var pgError *pgconn.PgError
	duplicate := errors.As(e, &pgError) && ((pgError.Code == "23505" && pgError.ConstraintName == "pg_namespace_nspname_index") || pgError.Code == "42P06")
	if e != nil && duplicate {
		exists, checkErr := m.owned(ctx, m.backend, m.LatestSchema(), m.latestOwner())
		if checkErr != nil {
			return checkErr
		}
		if exists {
			return m.validateLatest(ctx, m.backend)
		}
	}
	return e
}
func (m *SyncMigrator) migrateOnce(ctx context.Context) error {
	tx, e := m.backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "SET LOCAL statement_timeout=3600000"); e != nil {
		return e
	}
	if e = m.lock(ctx, tx); e != nil {
		return e
	}
	legacy, e := m.owned(ctx, tx, m.LegacySchema(), m.legacyOwner())
	if e != nil {
		return e
	}
	if legacy {
		r, e := tx.Query(ctx, "SELECT name FROM "+schema.Quote(m.LegacySchema())+".kysely_migration WHERE name='2025_02_26_1_rpc_request_results'")
		if e != nil {
			return e
		}
		if len(r.Rows) == 0 {
			return &MigrationError{Message: `"ponder_sync" migration failed. Please first update to v0.10 or "DROP SCHEMA ponder_sync CASCADE".`}
		}
	}
	latest, e := m.owned(ctx, tx, m.LatestSchema(), m.latestOwner())
	if e != nil {
		return e
	}
	if latest {
		if e = m.validateLatest(ctx, tx); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	files := []string{syncCreateSQL}
	if legacy {
		files = append(files, syncCopySQL)
	}
	for _, file := range files {
		for _, statement := range strings.Split(file, "--> statement-breakpoint") {
			if strings.TrimSpace(statement) != "" {
				if e = tx.Exec(ctx, m.remap(statement)); e != nil {
					return e
				}
			}
		}
	}
	if e = tx.Exec(ctx, "COMMENT ON SCHEMA "+schema.Quote(m.LatestSchema())+" IS '"+m.latestOwner()+"'"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type CachedRPCResult struct {
	ChainID     int64  `json:"chainId"`
	RequestHash string `json:"requestHash"`
	BlockNumber *int64 `json:"blockNumber"`
	Result      string `json:"result"`
}

func (m *SyncMigrator) InsertRPCResult(ctx context.Context, result CachedRPCResult) error {
	tx, e := m.backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = m.lock(ctx, tx); e != nil {
		return e
	}
	exists, e := m.owned(ctx, tx, m.LatestSchema(), m.latestOwner())
	if e != nil {
		return e
	}
	if !exists {
		return fmt.Errorf("sync cache must be migrated before writing")
	}
	var block any
	if result.BlockNumber != nil {
		block = *result.BlockNumber
	}
	if e = tx.Exec(ctx, "INSERT INTO "+schema.Quote(m.LatestSchema())+".rpc_request_results (chain_id,request_hash,block_number,result) VALUES ($1,$2,$3,$4)", result.ChainID, result.RequestHash, block, result.Result); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (m *SyncMigrator) RPCResults(ctx context.Context) ([]CachedRPCResult, error) {
	exists, e := m.owned(ctx, m.backend, m.LatestSchema(), m.latestOwner())
	if e != nil {
		return nil, e
	}
	if !exists {
		return nil, fmt.Errorf("sync cache has not been migrated")
	}
	r, e := m.backend.Query(ctx, "SELECT chain_id::text AS chain_id,request_hash,block_number::text AS block_number,result FROM "+schema.Quote(m.LatestSchema())+".rpc_request_results ORDER BY chain_id,request_hash")
	if e != nil {
		return nil, e
	}
	rows := make([]CachedRPCResult, 0, len(r.Rows))
	for _, row := range r.Rows {
		chain, e := strconv.ParseInt(fmt.Sprint(row["chain_id"]), 10, 64)
		if e != nil {
			return nil, e
		}
		var block *int64
		if row["block_number"] != nil {
			n, e := strconv.ParseInt(fmt.Sprint(row["block_number"]), 10, 64)
			if e != nil {
				return nil, e
			}
			block = &n
		}
		rows = append(rows, CachedRPCResult{ChainID: chain, RequestHash: fmt.Sprint(row["request_hash"]), BlockNumber: block, Result: fmt.Sprint(row["result"])})
	}
	return rows, nil
}
func (d *Lifecycle) MigrateSync(ctx context.Context) error {
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return fmt.Errorf("database lifecycle is closed")
	}
	m, e := NewSyncMigrator(d.s.Backend, d.s.Namespace)
	if e != nil {
		return e
	}
	return m.Migrate(ctx)
}
