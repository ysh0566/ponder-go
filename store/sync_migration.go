package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ysh0566/ponder-go/schema"
)

// The current cache schema uses the pinned upstream DDL.
//
//go:embed syncsql/create.sql
var syncCreateSQL string

// The reference describes the table and key layouts produced by syncCreateSQL.
// A column tuple contains its name, builtin type, type modifier, NOT NULL and identity mode.
//
//go:embed syncsql/layout.json
var syncLayout string

//go:embed syncsql/layout.sql
var syncLayoutSQL string

// SyncMigrator initializes the current RPC cache in an explicitly owned scope.
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
func (m *SyncMigrator) LatestSchema() string { return m.scope + "_rpc_1" }
func (m *SyncMigrator) latestOwner() string {
	return "ponder-go:sync-migration:17c6d5ba:v1:" + m.scope + ":ponder_rpc_cache_1"
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
	r, err := q.Query(ctx, syncLayoutSQL, m.LatestSchema(), syncLayout)
	if err != nil {
		return err
	}
	if len(r.Rows) != 0 {
		return &MigrationError{Message: fmt.Sprintf("unsupported sync cache layout for table %q; choose a fresh scope", r.Rows[0]["name"])}
	}
	return nil
}

func (m *SyncMigrator) requireCurrent(ctx context.Context, q Querier) error {
	exists, err := m.owned(ctx, q, m.LatestSchema(), m.latestOwner())
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("sync cache has not been migrated")
	}
	return m.validateLatest(ctx, q)
}

func (m *SyncMigrator) lock(ctx context.Context, tx Transaction) error {
	if m.backend.Dialect() == "postgres" {
		return tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", m.scope+":sync-migration")
	}
	return nil
}
func (m *SyncMigrator) Migrate(ctx context.Context) error {
	e := m.openLatest(ctx, true)
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
func (m *SyncMigrator) openLatest(ctx context.Context, allowCreate bool) error {
	tx, e := m.backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	isolation := "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"
	if !allowCreate {
		isolation += ", READ ONLY"
	}
	if e = tx.Exec(ctx, isolation); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "SET LOCAL statement_timeout=3600000"); e != nil {
		return e
	}
	if e = m.lock(ctx, tx); e != nil {
		return e
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
	if !allowCreate {
		return fmt.Errorf("sync cache has not been migrated")
	}
	// An older standalone cache is never an input to initialization.
	// Refuse an occupied scope before creating any current tables.
	previous, e := tx.Query(ctx, "SELECT 1 FROM pg_namespace WHERE nspname=$1", m.scope+"_legacy")
	if e != nil {
		return e
	}
	if len(previous.Rows) != 0 {
		return &MigrationError{Message: "unsupported sync cache format; choose a fresh scope"}
	}
	ddl := strings.ReplaceAll(syncCreateSQL, `"ponder_rpc_cache_1"`, schema.Quote(m.LatestSchema()))
	for _, statement := range strings.Split(ddl, "--> statement-breakpoint") {
		if strings.TrimSpace(statement) != "" {
			if e = tx.Exec(ctx, statement); e != nil {
				return e
			}
		}
	}
	if e = m.validateLatest(ctx, tx); e != nil {
		return e
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
	if e = m.requireCurrent(ctx, tx); e != nil {
		return e
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
	tx, e := m.backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.Background())
	if e = tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); e != nil {
		return nil, e
	}
	if e = m.requireCurrent(ctx, tx); e != nil {
		return nil, e
	}
	r, e := tx.Query(ctx, "SELECT chain_id::text AS chain_id,request_hash,block_number::text AS block_number,result FROM "+schema.Quote(m.LatestSchema())+".rpc_request_results ORDER BY chain_id,request_hash")
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
	if e = tx.Commit(ctx); e != nil {
		return nil, e
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
