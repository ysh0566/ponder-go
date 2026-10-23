package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

type MigrationError struct{ Message string }

func (e *MigrationError) Error() string { return e.Message }

var ErrLeaseLost = errors.New("database writer lease has expired or changed owner")

type LifecycleOptions struct {
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	ViewsSchema       string
}
type MigrationChain struct {
	ID   uint64
	Name string
}
type FinalizedBlock struct {
	Number, Timestamp uint64
	Hash, ParentHash  string
}
type Migration struct {
	BuildID         string
	Chains          []MigrationChain
	FinalizedBlocks []FinalizedBlock
}
type RecoveryCheckpoint struct {
	ChainID    uint64 `json:"chainId"`
	Checkpoint string `json:"checkpoint"`
}
type AppMetadata struct {
	Version     string   `json:"version"`
	BuildID     string   `json:"build_id"`
	TableNames  []string `json:"table_names"`
	ViewNames   []string `json:"view_names"`
	IsDev       int      `json:"is_dev"`
	IsLocked    int      `json:"is_locked"`
	IsReady     int      `json:"is_ready"`
	HeartbeatAt int64    `json:"heartbeat_at"`
	ViewsSchema string   `json:"views_schema,omitempty"`
}
type AppMetadataRow struct {
	Key   string
	Value AppMetadata
}

// Lifecycle owns startup, a fenced writer lease, recovery and shutdown for a
// new native staged namespace. It never adopts an upstream or unmanaged schema.
// Call Close before closing the caller-owned Backend. Dev rebuild/legacy sync
// migration use owned shadows. The automatic single-chain staged scheduler is
// in runtime.NewStaged; dev rebuild and multichain scheduling remain unfinished.
type Lifecycle struct {
	s               *Store
	opts            LifecycleOptions
	mu              sync.Mutex
	started, closed bool
	cancel          context.CancelFunc
	done            chan struct{}
	errMu           sync.Mutex
	heartbeatErr    error
}

func NewLifecycle(backend Backend, model schema.Schema, namespace string, ordering schema.Ordering, options LifecycleOptions) (*Lifecycle, error) {
	if options.HeartbeatInterval == 0 {
		options.HeartbeatInterval = 10 * time.Second
	}
	if options.HeartbeatTimeout == 0 {
		options.HeartbeatTimeout = 25 * time.Second
	}
	if options.HeartbeatInterval < time.Millisecond || options.HeartbeatTimeout < time.Millisecond {
		return nil, fmt.Errorf("heartbeat interval and timeout must be at least one millisecond")
	}
	s, e := describeStore(backend, model, namespace, ordering, true)
	if e != nil {
		return nil, e
	}
	if namespace == "" {
		s.Namespace = strings.Replace(s.Namespace, "ponder_go_actions_", "ponder_go_lifecycle_", 1)
		s.SyncNamespace = s.Namespace + "_sync"
	}
	if options.ViewsSchema != "" {
		if !schema.Identifier(options.ViewsSchema) || !strings.HasPrefix(options.ViewsSchema, s.Namespace+"_") || len(options.ViewsSchema) > 45 {
			return nil, fmt.Errorf("views schema must be an owned child identifier of the indexing namespace, at most 45 characters")
		}
		s.viewsSchema = options.ViewsSchema
	}
	var token [16]byte
	if _, e = rand.Read(token[:]); e != nil {
		return nil, e
	}
	s.leaseToken = hex.EncodeToString(token[:])
	s.leaseTimeout = options.HeartbeatTimeout.Milliseconds()
	return &Lifecycle{s: s, opts: options}, nil
}
func (d *Lifecycle) Actions() *Actions { return &Actions{d.s} }

func (d *Lifecycle) Migrate(ctx context.Context, request Migration) ([]RecoveryCheckpoint, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, fmt.Errorf("database lifecycle is closed")
	}
	if d.started {
		return nil, fmt.Errorf("database lifecycle has already migrated")
	}
	if request.BuildID == "" {
		return nil, fmt.Errorf("build ID is required")
	}
	if len(request.Chains) != len(request.FinalizedBlocks) {
		return nil, fmt.Errorf("each migration chain requires its finalized block")
	}
	seen := map[uint64]bool{}
	for _, chain := range request.Chains {
		if seen[chain.ID] || chain.Name == "" {
			return nil, fmt.Errorf("migration chains require unique IDs and names")
		}
		seen[chain.ID] = true
	}
	result, expiry, e := d.tryMigrate(ctx, request)
	if e != nil {
		return nil, e
	}
	if expiry != 0 {
		duration := time.Until(time.UnixMilli(expiry))
		if duration < 0 {
			duration = 0
		}
		timer := time.NewTimer(duration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		result, expiry, e = d.tryMigrate(ctx, request)
		if e != nil {
			return nil, e
		}
		if expiry != 0 {
			return nil, &MigrationError{Message: fmt.Sprintf("Failed to acquire lock on schema %q. A different Ponder app is actively using this schema.", d.s.Namespace)}
		}
	}
	d.started = true
	lifetime, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.done = make(chan struct{})
	go d.heartbeat(lifetime, d.done)
	return result, nil
}

func (d *Lifecycle) tryMigrate(ctx context.Context, request Migration) ([]RecoveryCheckpoint, int64, error) {
	tx, e := d.s.Backend.Begin(ctx)
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(context.Background())
	if e = d.s.lock(ctx, tx); e != nil {
		return nil, 0, e
	}
	// Existing native standalone Actions are not silently upgraded into a
	// managed application; startup must choose a fresh shadow for that change.
	existing, e := tx.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", d.s.Namespace)
	if e != nil {
		return nil, 0, e
	}
	if len(existing.Rows) > 0 {
		r, e := tx.Query(ctx, "SELECT to_regclass($1)::text AS name", d.s.internal("_ponder_go_app"))
		if e != nil {
			return nil, 0, e
		}
		if len(r.Rows) == 0 || r.Rows[0]["name"] == nil {
			return nil, 0, &MigrationError{Message: "existing namespace is unmanaged; rebuild into a new shadow schema"}
		}
	}
	if e = d.s.initializeInternal(ctx, tx); e != nil {
		return nil, 0, e
	}
	if e = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+d.s.internal("_ponder_go_app")+" (key text PRIMARY KEY CHECK(key='app'),value jsonb NOT NULL,lease_token text NOT NULL)"); e != nil {
		return nil, 0, e
	}
	rows, e := tx.Query(ctx, "SELECT value::text AS value FROM "+d.s.internal("_ponder_go_app")+" WHERE key='app' FOR UPDATE")
	if e != nil {
		return nil, 0, e
	}
	metadata := AppMetadata{Version: "6", BuildID: request.BuildID, TableNames: []string{}, ViewNames: []string{}, IsLocked: 1, HeartbeatAt: time.Now().UnixMilli(), ViewsSchema: d.s.viewsSchema}
	for _, table := range d.s.Schema.Tables {
		metadata.TableNames = append(metadata.TableNames, table.Name)
	}
	for _, view := range d.s.Schema.Views {
		metadata.ViewNames = append(metadata.ViewNames, view.Name)
	}
	var recovery []RecoveryCheckpoint
	if len(rows.Rows) == 0 {
		if e = d.s.createUserObjects(ctx, tx, false); e != nil {
			return nil, 0, e
		}
		if e = d.Actions().createLiveQueryProcedures(ctx, tx); e != nil {
			return nil, 0, e
		}
		if e = d.s.createAliasViews(ctx, tx); e != nil {
			return nil, 0, e
		}
	} else {
		var old AppMetadata
		if e = json.Unmarshal([]byte(fmt.Sprint(rows.Rows[0]["value"])), &old); e != nil {
			return nil, 0, e
		}
		if old.Version != "6" {
			return nil, 0, &MigrationError{Message: "schema was previously used by an incompatible application metadata version; use a new shadow schema"}
		}
		if old.IsDev != 0 {
			return nil, 0, &MigrationError{Message: "development schema rebuild is not implemented; use a new shadow schema"}
		}
		if old.BuildID != request.BuildID {
			return nil, 0, &MigrationError{Message: "schema was previously used by a different Ponder app; use a new shadow schema"}
		}
		if old.ViewsSchema != d.s.viewsSchema {
			return nil, 0, &MigrationError{Message: "views schema configuration changed; use a new shadow schema"}
		}
		expiry := old.HeartbeatAt + d.opts.HeartbeatTimeout.Milliseconds()
		if old.IsLocked != 0 && expiry > time.Now().UnixMilli() {
			return nil, expiry, nil
		}
		states, e := checkpointStates(ctx, tx, d.s)
		if e != nil {
			return nil, 0, e
		}
		if len(states) > 0 {
			recovery = make([]RecoveryCheckpoint, 0, len(states))
		}
		for _, state := range states {
			index := -1
			for i, chain := range request.Chains {
				if chain.ID == state.ChainID {
					index = i
					break
				}
			}
			if index < 0 {
				return nil, 0, &MigrationError{Message: fmt.Sprintf("missing finalized block for existing chain %d", state.ChainID)}
			}
			cp, e := checkpoint.Decode(state.FinalizedCheckpoint)
			if e != nil {
				return nil, 0, e
			}
			if request.FinalizedBlocks[index].Timestamp < cp.BlockTimestamp {
				return nil, 0, &MigrationError{Message: fmt.Sprintf("Finalized block for chain %q cannot move backwards", fmt.Sprint(state.ChainID))}
			}
			recovery = append(recovery, RecoveryCheckpoint{ChainID: state.ChainID, Checkpoint: state.SafeCheckpoint})
		}
		if e = prepareLiveQueryChanges(ctx, tx, d.s.Backend.Dialect()); e != nil {
			return nil, 0, e
		}
		if e = d.dropRuntimeObjects(ctx, tx); e != nil {
			return nil, 0, e
		}
		if e = d.s.eventContext(ctx, tx, checkpoint.ZeroString, 0, true); e != nil {
			return nil, 0, e
		}
		journal, e := tx.Query(ctx, "SELECT table_name,row_key::text AS key,before_row::text AS before FROM "+d.s.internal("_ponder_go_journal")+" ORDER BY operation_id DESC")
		if e != nil {
			return nil, 0, e
		}
		if _, e = d.s.undoJournal(ctx, tx, journal); e != nil {
			return nil, 0, e
		}
		if e = tx.Exec(ctx, "DELETE FROM "+d.s.internal("_ponder_go_journal")); e != nil {
			return nil, 0, e
		}
	}
	// Recovery or initial DDL can outlast a lease window. Start the heartbeat
	// age at publication, rather than returning an already-expired owner.
	metadata.HeartbeatAt = time.Now().UnixMilli()
	encoded, e := json.Marshal(metadata)
	if e != nil {
		return nil, 0, e
	}
	if e = tx.Exec(ctx, "INSERT INTO "+d.s.internal("_ponder_go_app")+" VALUES ('app',$1::jsonb,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value,lease_token=excluded.lease_token", string(encoded), d.s.leaseToken); e != nil {
		return nil, 0, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, 0, e
	}
	return recovery, 0, nil
}

func (d *Lifecycle) dropRuntimeObjects(ctx context.Context, tx Transaction) error {
	for _, table := range d.s.Schema.Tables {
		for _, trigger := range []string{"_ponder_go_capture", "_ponder_go_no_truncate", "live_query"} {
			if e := tx.Exec(ctx, "DROP TRIGGER IF EXISTS "+schema.Quote(trigger)+" ON "+d.s.table(table.Name)); e != nil {
				return e
			}
		}
		for _, index := range table.Indexes {
			if e := tx.Exec(ctx, "DROP INDEX IF EXISTS "+d.s.internal(index.Name)); e != nil {
				return e
			}
		}
	}
	for _, table := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
		if e := tx.Exec(ctx, "DROP TRIGGER IF EXISTS live_query_notify ON "+d.s.internal(table)); e != nil {
			return e
		}
	}
	return nil
}
func (d *Lifecycle) Metadata(ctx context.Context) ([]AppMetadataRow, error) {
	r, e := d.s.Backend.Query(ctx, "SELECT key,value::text AS value FROM "+d.s.internal("_ponder_go_app")+" ORDER BY key")
	if e != nil {
		return nil, e
	}
	rows := make([]AppMetadataRow, 0, len(r.Rows))
	for _, row := range r.Rows {
		var value AppMetadata
		if e = json.Unmarshal([]byte(fmt.Sprint(row["value"])), &value); e != nil {
			return nil, e
		}
		rows = append(rows, AppMetadataRow{Key: fmt.Sprint(row["key"]), Value: value})
	}
	return rows, nil
}
func (d *Lifecycle) MarkReady(ctx context.Context) error {
	return d.Actions().transaction(ctx, func(tx Transaction) error {
		return tx.Exec(ctx, "UPDATE "+d.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{is_ready}','1'::jsonb) WHERE key='app' AND lease_token=$1", d.s.leaseToken)
	})
}
func (s *Store) requireLease(ctx context.Context, tx Transaction) error {
	if s.leaseToken == "" {
		return nil
	}
	r, e := tx.Query(ctx, "SELECT lease_token,value->>'is_locked' AS locked,value->>'heartbeat_at' AS heartbeat FROM "+s.internal("_ponder_go_app")+" WHERE key='app'")
	if e != nil {
		return e
	}
	if len(r.Rows) != 1 || r.Rows[0]["lease_token"] != s.leaseToken || fmt.Sprint(r.Rows[0]["locked"]) != "1" {
		return ErrLeaseLost
	}
	var heartbeat int64
	if _, e = fmt.Sscan(fmt.Sprint(r.Rows[0]["heartbeat"]), &heartbeat); e != nil {
		return e
	}
	if heartbeat+s.leaseTimeout <= time.Now().UnixMilli() {
		return ErrLeaseLost
	}
	return nil
}
func (d *Lifecycle) heartbeat(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(d.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e := d.Heartbeat(ctx)
			if e != nil {
				d.errMu.Lock()
				d.heartbeatErr = e
				d.errMu.Unlock()
				if errors.Is(e, ErrLeaseLost) {
					return
				}
			}
		}
	}
}
func (d *Lifecycle) Heartbeat(ctx context.Context) error {
	now := time.Now().UnixMilli()
	r, e := d.s.Backend.Query(ctx, "UPDATE "+d.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{heartbeat_at}',to_jsonb($1::bigint)) WHERE key='app' AND lease_token=$2 AND value->>'is_locked'='1' AND (value->>'heartbeat_at')::bigint>$3 RETURNING key", now, d.s.leaseToken, now-d.opts.HeartbeatTimeout.Milliseconds())
	if e != nil {
		return e
	}
	if len(r.Rows) != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (d *Lifecycle) HeartbeatError() error {
	d.errMu.Lock()
	defer d.errMu.Unlock()
	return d.heartbeatErr
}
func (d *Lifecycle) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	started, cancel, done := d.started, d.cancel, d.done
	d.mu.Unlock()
	if !started {
		return nil
	}
	cancel()
	<-done
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return d.s.Backend.Exec(ctx, "UPDATE "+d.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{is_locked}','0'::jsonb) WHERE key='app' AND lease_token=$1", d.s.leaseToken)
}
