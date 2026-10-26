package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

const managedFiniteVersion = "managed-finite-v1"

// ErrManagedFiniteBusy rejects concurrent activation or serialized PGlite reads
// rather than waiting beyond the caller deadline behind an active transaction.
var ErrManagedFiniteBusy = errors.New("managed finite operation is busy")

// ManagedFiniteOptions identifies a new finite-only managed namespace. PlanID
// must describe the immutable compiled ranges/filters; BuildID identifies the
// application's handler implementation. Neither may be reused for a changed plan.
type ManagedFiniteOptions struct {
	BuildID, PlanID, ViewsSchema        string
	Chains                              []MigrationChain
	EndBlocks                           map[uint64]uint64
	HeartbeatInterval, HeartbeatTimeout time.Duration
}

// ManagedStageError never equates a failed COMMIT acknowledgement with rollback.
// Outcome is "unknown" unless an ordered readback proved the stage absent.
type ManagedStageError struct {
	Stage, Outcome string
	Err            error
}

func (e *ManagedStageError) Error() string {
	return fmt.Sprintf("managed stage %s outcome %s: %v", e.Stage, e.Outcome, e.Err)
}
func (e *ManagedStageError) Unwrap() error { return e.Err }

type managedOwnerIdentity struct {
	PID          int
	Started, Key string
}

// ManagedFinite uses the existing actual storage exclusion. PostgreSQL also
// renews a token-fenced heartbeat on a separate control pool lane; a long business
// transaction does not block that lane. PGlite deliberately uses inherited
// filesystem exclusion, NOT the source's stale-heartbeat takeover semantics.
// A still-owned PGlite process cannot expire due to a serialized long SQL call.
type ManagedFinite struct {
	s           *Store
	storage     *IndependentStorage
	opts        ManagedFiniteOptions
	identity    string
	mode        string
	owners      []managedOwnerIdentity
	control     *pgBackend
	cancel      context.CancelFunc
	done        chan struct{}
	closed      atomic.Bool
	lost        atomic.Bool
	mu          sync.Mutex
	initialized bool
}

func normalizeManagedOptions(o IndependentStorageOptions, m ManagedFiniteOptions) (ManagedFiniteOptions, error) {
	if err := independentNames(o); err != nil {
		return m, err
	}
	if o.Ordering != "" && o.Ordering != schema.Multichain {
		return m, fmt.Errorf("managed finite requires multichain ordering")
	}
	if m.BuildID == "" || m.PlanID == "" || len(m.Chains) == 0 || len(m.EndBlocks) != len(m.Chains) {
		return m, fmt.Errorf("managed finite requires build, plan and exact chain endpoints")
	}
	m.Chains = append([]MigrationChain(nil), m.Chains...)
	ends := map[uint64]uint64{}
	names := map[string]bool{}
	ids := map[uint64]bool{}
	for _, c := range m.Chains {
		end, ok := m.EndBlocks[c.ID]
		if !ok || c.Name == "" || names[c.Name] || ids[c.ID] || end > 9007199254740991 {
			return m, fmt.Errorf("invalid managed finite chain/end identity")
		}
		names[c.Name] = true
		ids[c.ID] = true
		ends[c.ID] = end
	}
	m.EndBlocks = ends
	sort.Slice(m.Chains, func(i, j int) bool { return m.Chains[i].ID < m.Chains[j].ID })
	if m.ViewsSchema != "" {
		if !schema.Identifier(m.ViewsSchema) || len(m.ViewsSchema) > 45 || !strings.HasPrefix(m.ViewsSchema, o.BusinessNamespace+"_") {
			return m, fmt.Errorf("managed alias must be an owned child identifier")
		}
		for _, root := range []string{o.BusinessNamespace, o.SyncNamespace} {
			for _, n := range independentNamespaceFamily(root) {
				if m.ViewsSchema == n {
					return m, fmt.Errorf("managed alias collides with a storage namespace family")
				}
			}
		}
	}
	if m.HeartbeatInterval == 0 {
		m.HeartbeatInterval = 10 * time.Second
	}
	if m.HeartbeatTimeout == 0 {
		m.HeartbeatTimeout = 25 * time.Second
	}
	if m.HeartbeatInterval < time.Millisecond || m.HeartbeatTimeout <= m.HeartbeatInterval {
		return m, fmt.Errorf("managed heartbeat timeout must exceed positive interval")
	}
	return m, nil
}
func OpenManagedIndependentPostgres(ctx context.Context, o IndependentStorageOptions, m ManagedFiniteOptions) (*IndependentStorage, error) {
	var err error
	m, err = normalizeManagedOptions(o, m)
	if err != nil {
		return nil, err
	}
	return openIndependentPostgres(ctx, o, &m)
}
func OpenManagedIndependentPGlite(ctx context.Context, o IndependentStorageOptions, m ManagedFiniteOptions) (*IndependentStorage, error) {
	if o.BusinessDirectory == "" || o.SyncDirectory == "" || o.BusinessDirectory == "memory://" || o.SyncDirectory == "memory://" {
		return nil, fmt.Errorf("managed durable PGlite requires two persistent filesystem directories")
	}
	var err error
	m, err = normalizeManagedOptions(o, m)
	if err != nil {
		return nil, err
	}
	return openIndependentPGlite(ctx, o, &m)
}

func (s *IndependentStorage) reserveManagedAlias(ctx context.Context, alias string, other []string) error {
	p, ok := s.businessBackend.(*independentPGBackend)
	if !ok {
		return fmt.Errorf("managed alias owner requires fixed PostgreSQL connection")
	}
	key := alias + ":independent-owner"
	for _, old := range other {
		var collision bool
		if err := p.connection.QueryRowContext(ctx, "SELECT hashtextextended($1,0)=hashtextextended($2,0)", key, old).Scan(&collision); err != nil {
			return err
		}
		if collision {
			return fmt.Errorf("managed alias advisory identity collides")
		}
	}
	var acquired bool
	if err := p.connection.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", key).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("managed alias already owned")
	}
	s.ownerLocks = append(s.ownerLocks, independentOwnerLock{connection: p.connection, key: key})
	return nil
}

func (s *IndependentStorage) initializeManagedFinite(ctx context.Context, o IndependentStorageOptions, opts ManagedFiniteOptions) (*ManagedFinite, error) {
	// Snapshot the schema before publishing a managed owner; caller mutation must
	// not change later activation DDL while its persisted identity stays fixed.
	model := cloneManagedSchema(o.Schema)
	o.Schema = model
	st, err := describeStore(s.businessBackend, model, o.BusinessNamespace, schema.Multichain, true)
	if err != nil {
		return nil, err
	}
	st.viewsSchema = opts.ViewsSchema
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return nil, err
	}
	st.leaseToken = hex.EncodeToString(token[:])
	st.leaseTimeout = opts.HeartbeatTimeout.Milliseconds()
	m := &ManagedFinite{s: st, storage: s, opts: opts, mode: "pglite-inherited-filesystem-exclusion", done: make(chan struct{})}
	close(m.done)
	raw, encodeErr := json.Marshal(struct {
		Build, Plan, Alias, Ordering, Sync string
		Schema                             schema.Schema
		Chains                             []MigrationChain
		Ends                               map[uint64]uint64
	}{opts.BuildID, opts.PlanID, opts.ViewsSchema, string(schema.Multichain), o.SyncNamespace, o.Schema, opts.Chains, opts.EndBlocks})
	if encodeErr != nil {
		return nil, fmt.Errorf("encode managed identity: %w", encodeErr)
	}
	digest := sha256.Sum256(raw)
	m.identity = hex.EncodeToString(digest[:])
	if p, ok := s.businessBackend.(*independentPGBackend); ok {
		m.mode = "postgres-session-lock-and-heartbeat"
		m.control = p.pgBackend
		for _, lock := range s.ownerLocks {
			var id managedOwnerIdentity
			id.Key = lock.key
			if err = lock.connection.QueryRowContext(ctx, "SELECT pg_backend_pid(),backend_start::text FROM pg_stat_activity WHERE pid=pg_backend_pid()").Scan(&id.PID, &id.Started); err != nil {
				return nil, err
			}
			m.owners = append(m.owners, id)
		}
	}
	tx, err := st.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if err = st.lock(ctx, tx); err != nil {
		return nil, err
	}
	existing, err := tx.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", st.Namespace)
	if err != nil {
		return nil, err
	}
	if len(existing.Rows) > 0 {
		probe, e := tx.Query(ctx, "SELECT to_regclass($1)::text AS name", st.internal("_ponder_go_managed_finite"))
		if e != nil {
			return nil, e
		}
		if len(probe.Rows) != 1 || probe.Rows[0]["name"] == nil {
			return nil, fmt.Errorf("existing namespace is not managed finite; use a new namespace")
		}
	}
	if err = st.initializeInternal(ctx, tx); err != nil {
		return nil, err
	}
	if err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+st.internal("_ponder_go_app")+" (key text PRIMARY KEY CHECK(key='app'),value jsonb NOT NULL,lease_token text NOT NULL)"); err != nil {
		return nil, err
	}
	if err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+st.internal("_ponder_go_managed_finite")+" (key text PRIMARY KEY CHECK(key='identity'),identity text NOT NULL,lease_mode text NOT NULL)"); err != nil {
		return nil, err
	}
	if err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+st.internal("_ponder_go_managed_stages")+" (name text PRIMARY KEY,owner_token text NOT NULL,catalog_hash text NOT NULL)"); err != nil {
		return nil, err
	}
	old, err := tx.Query(ctx, "SELECT identity,lease_mode FROM "+st.internal("_ponder_go_managed_finite")+" WHERE key='identity'")
	if err != nil {
		return nil, err
	}
	if len(old.Rows) > 0 {
		if len(old.Rows) != 1 || old.Rows[0]["identity"] != m.identity || old.Rows[0]["lease_mode"] != m.mode {
			return nil, fmt.Errorf("managed finite build/schema/plan/chain/alias identity changed")
		}
		app, e := tx.Query(ctx, "SELECT value->>'version' AS version,lease_token FROM "+st.internal("_ponder_go_app")+" WHERE key='app'")
		if e != nil {
			return nil, e
		}
		if len(app.Rows) != 1 || app.Rows[0]["version"] != managedFiniteVersion {
			return nil, fmt.Errorf("incompatible managed metadata version")
		}
		if err = m.verifyBoundary(ctx, tx, nil, true); err != nil {
			return nil, err
		}
		// Validate old owner markers before any destructive DDL, then restore the new token.
		nextToken := st.leaseToken
		st.leaseToken = fmt.Sprint(app.Rows[0]["lease_token"])
		for _, stage := range m.activationStages() {
			present, e := m.verifyStage(ctx, tx, stage)
			if e == nil && !present {
				e = m.requireStageAbsent(ctx, tx, stage.name)
			}
			if e != nil {
				st.leaseToken = nextToken
				return nil, e
			}
		}
		st.leaseToken = nextToken
		if err = m.dropActivation(ctx, tx); err != nil {
			return nil, err
		}
	} else {
		if len(existing.Rows) > 0 {
			return nil, fmt.Errorf("managed identity missing in existing namespace")
		}
		if err = st.createUserObjects(ctx, tx, false); err != nil {
			return nil, err
		}
		if err = tx.Exec(ctx, "INSERT INTO "+st.internal("_ponder_go_managed_finite")+" VALUES ('identity',$1,$2)", m.identity, m.mode); err != nil {
			return nil, err
		}
	}
	if err = m.verifyAliasOwnership(ctx, tx); err != nil {
		return nil, err
	}
	app := AppMetadata{Version: managedFiniteVersion, BuildID: opts.BuildID, TableNames: []string{}, ViewNames: []string{}, IsLocked: 1, HeartbeatAt: time.Now().UnixMilli(), ViewsSchema: opts.ViewsSchema}
	for _, t := range st.Schema.Tables {
		app.TableNames = append(app.TableNames, t.Name)
	}
	for _, v := range st.Schema.Views {
		app.ViewNames = append(app.ViewNames, v.Name)
	}
	encoded, _ := json.Marshal(app)
	if err = tx.Exec(ctx, "INSERT INTO "+st.internal("_ponder_go_app")+" VALUES ('app',jsonb_set($1::jsonb,'{heartbeat_at}',to_jsonb((extract(epoch FROM clock_timestamp())*1000)::bigint)),$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value,lease_token=excluded.lease_token", string(encoded), st.leaseToken); err != nil {
		return nil, err
	}
	if err = tx.Exec(ctx, "DELETE FROM "+st.internal("_ponder_go_managed_stages")); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, &ManagedStageError{Stage: "initialize", Outcome: "unknown", Err: err}
	}
	m.initialized = true
	st.managedFinite = m
	return m, nil
}

func (m *ManagedFinite) verifyAliasOwnership(ctx context.Context, q Querier) error {
	if m.opts.ViewsSchema == "" {
		return nil
	}
	r, e := q.Query(ctx, "SELECT obj_description(oid,'pg_namespace') AS owner FROM pg_namespace WHERE nspname=$1", m.opts.ViewsSchema)
	if e != nil {
		return e
	}
	if len(r.Rows) > 0 && (len(r.Rows) != 1 || r.Rows[0]["owner"] != m.s.aliasOwner()) {
		return fmt.Errorf("managed alias schema is not owned by this namespace")
	}
	return nil
}
func (m *ManagedFinite) dropActivation(ctx context.Context, tx Transaction) error {
	// Drop only explicitly configured objects. No business rows or checkpoints are changed.
	for _, t := range m.s.Schema.Tables {
		for _, n := range []string{"_ponder_go_capture", "_ponder_go_no_truncate", "live_query"} {
			if e := tx.Exec(ctx, "DROP TRIGGER IF EXISTS "+schema.Quote(n)+" ON "+m.s.table(t.Name)); e != nil {
				return e
			}
		}
		for _, i := range t.Indexes {
			if e := tx.Exec(ctx, "DROP INDEX IF EXISTS "+m.s.internal(i.Name)); e != nil {
				return e
			}
		}
	}
	for _, n := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
		for _, trigger := range []string{"live_query_notify", m.opts.ViewsSchema + "_live_query_notify"} {
			if m.opts.ViewsSchema == "" && trigger == "_live_query_notify" {
				continue
			}
			if e := tx.Exec(ctx, "DROP TRIGGER IF EXISTS "+schema.Quote(trigger)+" ON "+m.s.internal(n)); e != nil {
				return e
			}
		}
	}
	if m.opts.ViewsSchema != "" {
		if e := m.verifyAliasOwnership(ctx, tx); e != nil {
			return e
		}
		r, e := tx.Query(ctx, "SELECT 1 FROM pg_namespace WHERE nspname=$1", m.opts.ViewsSchema)
		if e != nil {
			return e
		}
		if len(r.Rows) > 0 {
			names := []string{"_ponder_meta", "_ponder_checkpoint"}
			for _, t := range m.s.Schema.Tables {
				names = append(names, t.Name)
			}
			for _, v := range m.s.Schema.Views {
				names = append(names, v.Name)
			}
			for _, n := range names {
				if e = tx.Exec(ctx, "DROP VIEW IF EXISTS "+m.s.aliasTable(n)); e != nil {
					return e
				}
			}
		}
	}
	for _, n := range []string{"_ponder_go_capture", "live_query", "live_query_notify"} {
		if e := tx.Exec(ctx, "DROP FUNCTION IF EXISTS "+m.s.internal(n)+"()"); e != nil {
			return e
		}
	}
	if m.opts.ViewsSchema != "" {
		if e := tx.Exec(ctx, "DROP FUNCTION IF EXISTS "+schema.Quote(m.opts.ViewsSchema)+".live_query_notify()"); e != nil {
			return e
		}
	}

	return nil
}

// verifyBoundary rejects partial/mixed recovery instead of applying legacy undo.
func (m *ManagedFinite) verifyBoundary(ctx context.Context, q Querier, expected []CheckpointState, allowEmpty bool) error {
	rows, e := q.Query(ctx, "SELECT chain_id::text AS id,c.chain_name,p.checkpoint AS latest,f.checkpoint AS finalized,f.safe_checkpoint AS safe,r.checkpoint AS cursor FROM "+m.s.internal("_ponder_go_chains")+" c FULL OUTER JOIN "+m.s.internal("_ponder_go_progress")+" p USING(chain_id) FULL OUTER JOIN "+m.s.internal("_ponder_go_finalized")+" f USING(chain_id) FULL OUTER JOIN "+m.s.internal("_ponder_go_runtime_cursor")+" r USING(chain_id) ORDER BY chain_id")
	if e != nil {
		return e
	}
	pending, e := q.Query(ctx, "SELECT 1 FROM "+m.s.internal("_ponder_go_journal")+" LIMIT 1")
	if e != nil {
		return e
	}
	if len(pending.Rows) > 0 {
		return fmt.Errorf("managed finite requires an empty journal at recovery/activation")
	}
	if len(rows.Rows) == 0 && allowEmpty {
		names := []string{"_ponder_go_events"}
		for _, t := range m.s.Schema.Tables {
			names = append(names, t.Name)
		}
		for _, name := range names {
			r, e := q.Query(ctx, "SELECT 1 FROM "+m.s.internal(name)+" LIMIT 1")
			if e != nil {
				return e
			}
			if len(r.Rows) > 0 {
				return fmt.Errorf("managed uninitialized boundary contains data in %s", name)
			}
		}
		return nil
	}
	if len(rows.Rows) != len(m.opts.Chains) {
		return fmt.Errorf("managed finite requires exact configured chain metadata")
	}
	targets := map[uint64]CheckpointState{}
	for _, s := range expected {
		if _, ok := targets[s.ChainID]; ok {
			return fmt.Errorf("duplicate managed activation target")
		}
		targets[s.ChainID] = s
	}
	if expected != nil && len(targets) != len(m.opts.Chains) {
		return fmt.Errorf("managed activation targets must cover every chain")
	}
	for i, row := range rows.Rows {
		id, e := strconv.ParseUint(fmt.Sprint(row["id"]), 10, 64)
		if e != nil || id != m.opts.Chains[i].ID || row["chain_name"] != m.opts.Chains[i].Name {
			return fmt.Errorf("managed chain identity mismatch")
		}
		cp, ok := row["cursor"].(string)
		decoded, e := checkpoint.Decode(cp)
		if !ok || e != nil || decoded.ChainID != id || cp == checkpoint.MaxString || row["latest"] != cp || row["finalized"] != cp || row["safe"] != cp {
			return fmt.Errorf("managed finite checkpoint boundary is partial or inconsistent")
		}
		if decoded.BlockNumber > m.opts.EndBlocks[id] {
			return fmt.Errorf("managed checkpoint exceeds finite endpoint")
		}
		if expected != nil {
			target := targets[id]
			if target.ChainName != m.opts.Chains[i].Name || target.SafeCheckpoint != cp || target.LatestCheckpoint != cp || target.FinalizedCheckpoint != cp || decoded.BlockNumber != m.opts.EndBlocks[id] || decoded != checkpoint.Block(decoded.BlockTimestamp, id, decoded.BlockNumber, true) {
				return fmt.Errorf("managed activation has not reached exact finite endpoints")
			}
		}
	}
	return nil
}

func (m *ManagedFinite) Metadata(ctx context.Context) (AppMetadata, error) {
	unlock, e := m.lockMetadataRead(ctx)
	if e != nil {
		return AppMetadata{}, e
	}
	defer unlock()
	return m.metadata(ctx)
}
func (m *ManagedFinite) lockMetadataRead(ctx context.Context) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if m.control != nil {
		return func() {}, nil
	}
	if !m.s.mu.TryLock() {
		return nil, ErrManagedFiniteBusy
	}
	return m.s.mu.Unlock, nil
}
func (m *ManagedFinite) metadata(ctx context.Context) (AppMetadata, error) {
	if e := ctx.Err(); e != nil {
		return AppMetadata{}, e
	}
	if m.closed.Load() {
		return AppMetadata{}, fmt.Errorf("managed finite closed")
	}
	r, e := m.reader().Query(ctx, "SELECT value::text AS value FROM "+m.s.internal("_ponder_go_app")+" WHERE key='app' AND lease_token=$1", m.s.leaseToken)
	if e != nil {
		return AppMetadata{}, e
	}
	if len(r.Rows) != 1 {
		return AppMetadata{}, ErrLeaseLost
	}
	var a AppMetadata
	e = json.Unmarshal([]byte(fmt.Sprint(r.Rows[0]["value"])), &a)
	return a, e
}
func (m *ManagedFinite) Ready(ctx context.Context) (bool, error) {
	unlock, e := m.lockMetadataRead(ctx)
	if e != nil {
		return false, e
	}
	defer unlock()
	a, e := m.metadata(ctx)
	if e != nil {
		return false, e
	}
	if e = m.requireLease(ctx, m.reader()); e != nil {
		return false, e
	}
	if a.IsLocked != 1 || a.IsReady != 1 {
		return false, nil
	}
	for _, stage := range m.activationStages() {
		present, e := m.verifyStage(ctx, m.reader(), stage)
		if e != nil {
			return false, e
		}
		if !present {
			return false, fmt.Errorf("ready has missing stage %s", stage.name)
		}
	}
	return true, nil
}
func (m *ManagedFinite) reader() Querier {
	if m.control != nil {
		return m.control
	}
	return m.s.Backend
}

func (m *ManagedFinite) start() {
	if m.control == nil {
		return
	}
	m.done = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go func() {
		defer close(m.done)
		tick := time.NewTicker(m.opts.HeartbeatInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				c, stop := context.WithTimeout(ctx, m.opts.HeartbeatTimeout)
				err := m.heartbeat(c)
				stop()
				if err != nil {
					m.lost.Store(true)
					return
				}
			}
		}
	}()
}
func (m *ManagedFinite) close() error {
	if m.closed.Swap(true) {
		return nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	<-m.done
	if !m.initialized {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	predicate, args := m.ownerPredicate(2)
	r, e := m.reader().Query(ctx, "UPDATE "+m.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{is_locked}','0'::jsonb) WHERE key='app' AND lease_token=$1 AND "+predicate+" RETURNING key", append([]any{m.s.leaseToken}, args...)...)
	if e != nil {
		return e
	}
	if len(r.Rows) != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (m *ManagedFinite) ownerPredicate(first int) (string, []any) {
	if m.control == nil {
		return "TRUE", nil
	}
	clauses := []string{}
	args := []any{}
	for _, o := range m.owners {
		i := first + len(args)
		clauses = append(clauses, fmt.Sprintf(`EXISTS(SELECT 1 FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND l.mode='ExclusiveLock' AND l.granted AND l.objsubid=1 AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND a.pid=$%d AND a.backend_start=$%d::timestamptz AND l.classid::bigint=((hashtextextended($%d,0)>>32)&4294967295) AND l.objid::bigint=(hashtextextended($%d,0)&4294967295))`, i, i+1, i+2, i+2))
		args = append(args, o.PID, o.Started, o.Key)
	}
	if len(clauses) == 0 {
		return "FALSE", nil
	}
	return strings.Join(clauses, " AND "), args
}
func (m *ManagedFinite) verifyOwnership(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if m.closed.Load() || m.lost.Load() {
		return ErrLeaseLost
	}
	if m.control == nil {
		return nil
	}
	pred, args := m.ownerPredicate(1)
	r, e := m.control.Query(ctx, "SELECT "+pred+" AS owned", args...)
	if e != nil {
		return e
	}
	if len(r.Rows) != 1 || r.Rows[0]["owned"] != true {
		m.lost.Store(true)
		return ErrLeaseLost
	}
	return nil
}
func (m *ManagedFinite) heartbeat(ctx context.Context) error {
	if m.closed.Load() {
		return ErrLeaseLost
	}
	pred, args := m.ownerPredicate(2)
	// clock_timestamp is evaluated by PostgreSQL at execution, not before waiting.
	r, e := m.control.Query(ctx, "UPDATE "+m.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{heartbeat_at}',to_jsonb((extract(epoch FROM clock_timestamp())*1000)::bigint)) WHERE key='app' AND lease_token=$1 AND value->>'is_locked'='1' AND (value->>'heartbeat_at')::bigint>(extract(epoch FROM clock_timestamp())*1000)::bigint-"+fmt.Sprint(m.opts.HeartbeatTimeout.Milliseconds())+" AND "+pred+" RETURNING key", append([]any{m.s.leaseToken}, args...)...)
	if e != nil {
		return e
	}
	if len(r.Rows) != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (m *ManagedFinite) requireLease(ctx context.Context, q Querier) error {
	if e := m.verifyOwnership(ctx); e != nil {
		return e
	}
	predicate := "TRUE"
	if m.control != nil {
		predicate = "(value->>'heartbeat_at')::bigint>(extract(epoch FROM clock_timestamp())*1000)::bigint-" + fmt.Sprint(m.opts.HeartbeatTimeout.Milliseconds())
	}
	r, e := q.Query(ctx, "SELECT lease_token FROM "+m.s.internal("_ponder_go_app")+" WHERE key='app' AND lease_token=$1 AND value->>'is_locked'='1' AND "+predicate, m.s.leaseToken)
	if e != nil {
		return e
	}
	if len(r.Rows) != 1 {
		return ErrLeaseLost
	}
	return nil
}

func managedHash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// Preserve executable column callbacks and typed defaults while snapshotting all
// mutable schema containers. BuildID remains the caller's code-identity contract.
func cloneManagedSchema(in schema.Schema) schema.Schema {
	out := in
	column := func(c schema.Column) schema.Column {
		c.Default = cloneValue(c.Default)
		c.GeneratedExpression = cloneValue(c.GeneratedExpression)
		if c.IdentityOptions != nil {
			v := *c.IdentityOptions
			c.IdentityOptions = &v
		}
		return c
	}
	out.Tables = append([]schema.Table(nil), in.Tables...)
	for n := range out.Tables {
		t := &out.Tables[n]
		t.Columns = append([]schema.Column(nil), t.Columns...)
		for k := range t.Columns {
			t.Columns[k] = column(t.Columns[k])
		}
		t.PrimaryKey = append([]string(nil), t.PrimaryKey...)
		t.CheckConstraints = append([]schema.CheckConstraint(nil), t.CheckConstraints...)
		t.Indexes = append([]schema.Index(nil), t.Indexes...)
		for k := range t.Indexes {
			t.Indexes[k].Columns = append([]string(nil), t.Indexes[k].Columns...)
		}
	}
	out.Enums = append([]schema.Enum(nil), in.Enums...)
	for n := range out.Enums {
		out.Enums[n].Values = append([]string(nil), out.Enums[n].Values...)
	}
	out.Views = append([]schema.View(nil), in.Views...)
	for n := range out.Views {
		v := &out.Views[n]
		v.Fields = append([]schema.ViewField(nil), v.Fields...)
		for k := range v.Fields {
			if v.Fields[k].Column != nil {
				c := column(*v.Fields[k].Column)
				v.Fields[k].Column = &c
			}
		}
	}
	out.Sequences = append([]schema.Sequence(nil), in.Sequences...)
	return out
}

// Capture exists inside each managed historical transaction only. It journals
// and seals writes normally, but durable activation remains an outer-run step.
func (m *ManagedFinite) dropTransientCapture(ctx context.Context, tx Transaction) error {
	for _, t := range m.s.Schema.Tables {
		for _, n := range []string{"_ponder_go_capture", "_ponder_go_no_truncate"} {
			if e := tx.Exec(ctx, "DROP TRIGGER IF EXISTS "+schema.Quote(n)+" ON "+m.s.table(t.Name)); e != nil {
				return e
			}
		}
	}
	return tx.Exec(ctx, "DROP FUNCTION IF EXISTS "+m.s.internal("_ponder_go_capture")+"()")
}
