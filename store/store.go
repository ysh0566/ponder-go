// Package store keeps durable sync data separate from journaled indexing data.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type Store struct {
	staged                   bool
	leaseToken               string
	leaseTimeout             int64
	viewsSchema              string
	profileState             *CacheProfile
	protocol                 atomic.Pointer[ProtocolCache]
	Backend                  Backend
	Namespace, SyncNamespace string
	Schema                   schema.Schema
	Ordering                 schema.Ordering
	mu                       sync.Mutex
	subMu                    sync.Mutex
	subscribers              map[chan Change]struct{}
}
type Change struct{ Tables []string }
type Event struct {
	ID         string                `json:"id"`
	Checkpoint checkpoint.Checkpoint `json:"checkpoint"`
	Name       string                `json:"name"`
	Payload    json.RawMessage       `json:"payload"`
}
type Writer struct {
	advanced bool
	s        *Store
	tx       Transaction
	event    Event
	encoded  string
	changed  map[string]bool
	err      error
	ormMu    sync.Mutex
	orm      *IndexingStore
	closed   atomic.Bool
}
type Row map[string]any
type WriteMode int

// Storage changes use a fresh default shadow. Explicit older namespaces are
// rejected by the fingerprint instead of silently rewriting an undo journal.
const storageVersion = "json-v3"

const (
	Insert WriteMode = iota
	Upsert
	Update
	Delete
)

func New(ctx context.Context, backend Backend, model schema.Schema, namespace string, ordering schema.Ordering) (*Store, error) {
	return newStore(ctx, backend, model, namespace, ordering, false)
}
func describeStore(backend Backend, model schema.Schema, namespace string, ordering schema.Ordering, staged bool) (*Store, error) {
	if ordering == "" {
		ordering = schema.Multichain
	}
	if e := model.Validate(ordering); e != nil {
		return nil, e
	}
	encoded, _ := json.Marshal(model)
	hash := sha256.Sum256(encoded)
	if namespace == "" {
		prefix := "shadow"
		if staged {
			prefix = "actions"
		}
		namespace = fmt.Sprintf("ponder_go_%s_v3_%x", prefix, hash[:6])
	}
	if !schema.Identifier(namespace) || len(namespace) > 45 || !strings.HasPrefix(namespace, "ponder_go_") {
		return nil, fmt.Errorf("task schemas must start with ponder_go_ and contain at most 45 identifier characters")
	}
	return &Store{Backend: backend, Namespace: namespace, SyncNamespace: namespace + "_sync", Schema: model, Ordering: ordering, subscribers: map[chan Change]struct{}{}, profileState: NewCacheProfile(), staged: staged}, nil
}

func newStore(ctx context.Context, backend Backend, model schema.Schema, namespace string, ordering schema.Ordering, staged bool) (*Store, error) {
	s, e := describeStore(backend, model, namespace, ordering, staged)
	if e != nil {
		return nil, e
	}
	tx, e := backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return nil, e
	}
	if e = s.initializeInternal(ctx, tx); e != nil {
		return nil, e
	}
	managed, e := tx.Query(ctx, "SELECT to_regclass($1)::text AS name", s.internal("_ponder_go_app"))
	if e != nil {
		return nil, e
	}
	if len(managed.Rows) > 0 && managed.Rows[0]["name"] != nil {
		return nil, fmt.Errorf("managed namespace requires the database lifecycle")
	}
	if e = s.createUserObjects(ctx, tx, true); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return s, nil
}

func (s *Store) initializeInternal(ctx context.Context, tx Transaction) error {
	namespace := s.Namespace
	found, e := tx.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", namespace)
	if e != nil {
		return e
	}
	if len(found.Rows) > 0 {
		meta, e := tx.Query(ctx, "SELECT to_regclass($1)::text AS name", schema.Quote(namespace)+"._ponder_go_meta")
		if e != nil || len(meta.Rows) == 0 || meta.Rows[0]["name"] == nil {
			return fmt.Errorf("refusing to adopt an existing unowned schema %s", namespace)
		}
	}
	syncFound, e := tx.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", s.SyncNamespace)
	if e != nil {
		return e
	}
	if len(syncFound.Rows) > 0 {
		meta, e := tx.Query(ctx, "SELECT to_regclass($1)::text AS name", schema.Quote(s.SyncNamespace)+"._ponder_go_meta")
		if e != nil || len(meta.Rows) == 0 || meta.Rows[0]["name"] == nil {
			return fmt.Errorf("refusing to adopt an existing unowned sync schema %s", s.SyncNamespace)
		}
	}
	if e = tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema.Quote(namespace)); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema.Quote(s.SyncNamespace)); e != nil {
		return e
	}
	statements := []string{
		"CREATE TABLE IF NOT EXISTS " + s.syncTable("_ponder_go_meta") + " (key text PRIMARY KEY,value text NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_meta") + " (key text PRIMARY KEY,value text NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_journal") + " (operation_id bigserial PRIMARY KEY, table_name text NOT NULL, row_key jsonb NOT NULL, before_row json, row_data json NOT NULL, operation smallint NOT NULL, checkpoint char(75) NOT NULL, chain_id numeric NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_chains") + " (chain_id numeric PRIMARY KEY,chain_name text NOT NULL UNIQUE)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_events") + " (event_id text PRIMARY KEY, checkpoint char(75) NOT NULL, chain_id numeric NOT NULL, operation_boundary bigint NOT NULL, operation_end bigint NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_progress") + " (chain_id numeric PRIMARY KEY, checkpoint char(75) NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_finalized") + " (chain_id numeric PRIMARY KEY, checkpoint char(75) NOT NULL, safe_checkpoint char(75) NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.syncTable("blocks") + " (chain_id numeric NOT NULL,number numeric NOT NULL,hash text NOT NULL,parent_hash text NOT NULL,timestamp numeric NOT NULL,payload jsonb NOT NULL,PRIMARY KEY(chain_id,number))",
		"CREATE TABLE IF NOT EXISTS " + s.syncTable("intervals") + " (chain_id numeric NOT NULL,filter text NOT NULL,start_block numeric NOT NULL,end_block numeric NOT NULL,PRIMARY KEY(chain_id,filter,start_block,end_block))",
		"CREATE TABLE IF NOT EXISTS " + s.syncTable("rpc_cache") + " (key text PRIMARY KEY,value jsonb NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.syncTable("factory_addresses") + " (factory_id text NOT NULL,chain_id numeric NOT NULL,address text NOT NULL,block_number numeric NOT NULL,block_hash text NOT NULL,log_index numeric NOT NULL,PRIMARY KEY(factory_id,chain_id,block_number,log_index,address))",
	}
	for _, sql := range statements {
		if e = tx.Exec(ctx, sql); e != nil {
			return e
		}
	}
	encoded, _ := json.Marshal(s.Schema)
	hash := sha256.Sum256(encoded)
	fingerprint := fmt.Sprintf("%s:%x:%s:staged=%t", storageVersion, hash, s.Ordering, s.staged)
	if e = tx.Exec(ctx, "INSERT INTO "+s.syncTable("_ponder_go_meta")+" VALUES ('owner',$1) ON CONFLICT(key) DO NOTHING", namespace); e != nil {
		return e
	}
	old, e := tx.Query(ctx, "SELECT value FROM "+s.internal("_ponder_go_meta")+" WHERE key='fingerprint'")
	if e != nil {
		return e
	}
	if len(old.Rows) > 0 && old.Rows[0]["value"] != fingerprint {
		return fmt.Errorf("schema, ordering, or storage format changed; rebuild into a new shadow schema")
	}
	if e = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_meta")+" VALUES ('fingerprint',$1) ON CONFLICT (key) DO NOTHING", fingerprint); e != nil {
		return e
	}
	return nil
}

func (s *Store) createUserObjects(ctx context.Context, tx Transaction, indexes bool) error {
	if e := s.createSchemaObjects(ctx, tx); e != nil {
		return e
	}
	for _, t := range s.Schema.Tables {
		if e := tx.Exec(ctx, s.Schema.TableDDL(t, s.Namespace)); e != nil {
			return e
		}
	}
	if indexes {
		if e := s.createIndexes(ctx, tx); e != nil {
			return e
		}
	}
	if e := s.createViews(ctx, tx); e != nil {
		return e
	}
	if e := s.createWriteGuards(ctx, tx); e != nil {
		return e
	}
	if !s.staged {
		if e := s.createJournalTriggers(ctx, tx); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) createIndexes(ctx context.Context, tx Transaction) error {
	for _, t := range s.Schema.Tables {
		for _, idx := range t.Indexes {
			cols := []string{}
			for _, c := range idx.Columns {
				cols = append(cols, schema.Quote(c))
			}
			unique := ""
			if idx.Unique {
				unique = "UNIQUE "
			}
			if e := tx.Exec(ctx, "CREATE "+unique+"INDEX IF NOT EXISTS "+schema.Quote(idx.Name)+" ON "+s.table(t.Name)+" ("+strings.Join(cols, ",")+")"); e != nil {
				return e
			}
		}
	}
	return nil
}

func (s *Store) internal(name string) string {
	return schema.Quote(s.Namespace) + "." + schema.Quote(name)
}
func (s *Store) table(name string) string { return s.internal(name) }
func (s *Store) syncTable(name string) string {
	return schema.Quote(s.SyncNamespace) + "." + schema.Quote(name)
}
func (s *Store) model(name string) (schema.Table, error) {
	for _, t := range s.Schema.Tables {
		if t.Name == name {
			return t, nil
		}
	}
	return schema.Table{}, fmt.Errorf("unknown table %q", name)
}
func (s *Store) lock(ctx context.Context, tx Transaction) error {
	if s.Backend.Dialect() == "postgres" {
		return tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", s.Namespace)
	}
	return nil
}
func (s *Store) Apply(ctx context.Context, event Event, handler func(*Writer) error) error {
	if s.staged {
		return fmt.Errorf("staged actions store cannot use atomic Apply")
	}
	encoded, e := checkpoint.Encode(event.Checkpoint)
	if e != nil {
		return e
	}
	if event.ID == "" {
		return fmt.Errorf("event ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return e
	}
	if e = s.eventContext(ctx, tx, encoded, event.Checkpoint.ChainID, false); e != nil {
		return e
	}
	existing, e := tx.Query(ctx, "SELECT event_id FROM "+s.internal("_ponder_go_events")+" WHERE event_id=$1", event.ID)
	if e != nil {
		return e
	}
	if len(existing.Rows) > 0 {
		return nil
	}
	final, e := tx.Query(ctx, "SELECT safe_checkpoint AS checkpoint FROM "+s.internal("_ponder_go_finalized")+" WHERE chain_id=$1", event.Checkpoint.ChainID)
	if e != nil {
		return e
	}
	if len(final.Rows) > 0 && encoded <= fmt.Sprint(final.Rows[0]["checkpoint"]) {
		return fmt.Errorf("event is at or before finalized checkpoint")
	}
	progress, e := tx.Query(ctx, "SELECT checkpoint FROM "+s.internal("_ponder_go_progress")+" WHERE chain_id=$1", event.Checkpoint.ChainID)
	if e != nil {
		return e
	}
	if len(progress.Rows) > 0 && encoded < fmt.Sprint(progress.Rows[0]["checkpoint"]) {
		return fmt.Errorf("event checkpoint regressed; rollback first")
	}
	boundary, e := tx.Query(ctx, "SELECT COALESCE(MAX(operation_id),0)::text AS id FROM "+s.internal("_ponder_go_journal"))
	if e != nil {
		return e
	}
	w := &Writer{s: s, tx: tx, event: event, encoded: encoded, changed: map[string]bool{}}
	defer w.closed.Store(true)
	e = handler(w)
	w.closed.Store(true)
	if e != nil {
		return e
	}
	if orm := w.indexingSession(); orm != nil {
		if e = orm.Cache.flush(ctx, false); e != nil {
			return e
		}
	}
	if w.err != nil {
		return w.err
	}
	end, e := tx.Query(ctx, "SELECT COALESCE(MAX(operation_id),0)::text AS id FROM "+s.internal("_ponder_go_journal"))
	if e != nil {
		return e
	}
	mutations, e := tx.Query(ctx, "SELECT DISTINCT table_name FROM "+s.internal("_ponder_go_journal")+" WHERE operation_id>$1", boundary.Rows[0]["id"])
	if e != nil {
		return e
	}
	for _, row := range mutations.Rows {
		w.changed[fmt.Sprint(row["table_name"])] = true
	}
	if e = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_events")+" VALUES ($1,$2,$3,$4,$5)", event.ID, encoded, event.Checkpoint.ChainID, boundary.Rows[0]["id"], end.Rows[0]["id"]); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_progress")+" VALUES ($1,$2) ON CONFLICT(chain_id) DO UPDATE SET checkpoint=excluded.checkpoint", event.Checkpoint.ChainID, encoded); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	tables := []string{}
	for name := range w.changed {
		tables = append(tables, name)
	}
	s.notify(tables)
	return nil
}
func keyWhere(t schema.Table, row Row, start int) (string, []any, error) {
	clauses := []string{}
	values := []any{}
	for _, k := range t.Keys() {
		v, ok := row[k]
		if !ok || v == nil {
			return "", nil, fmt.Errorf("missing primary key %s", k)
		}
		clauses = append(clauses, fmt.Sprintf("%s=$%d", schema.Quote(k), start+len(values)))
		values = append(values, v)
	}
	return strings.Join(clauses, " AND "), values, nil
}
func snapshot(ctx context.Context, q Querier, table string, t schema.Table, key Row) (*string, error) {
	where, values, e := keyWhere(t, key, 1)
	if e != nil {
		return nil, e
	}
	r, e := q.Query(ctx, "SELECT row_to_json(t)::text AS snapshot FROM "+table+" AS t WHERE "+where, values...)
	if e != nil {
		return nil, e
	}
	if len(r.Rows) == 0 {
		return nil, nil
	}
	s := fmt.Sprint(r.Rows[0]["snapshot"])
	return &s, nil
}
func (w *Writer) Find(ctx context.Context, name string, key Row) (Row, error) {
	if w.closed.Load() {
		return nil, &NonRetryableUserError{Message: "A store API method was called after the indexing function returned."}
	}
	if orm := w.indexingSession(); orm != nil {
		if e := orm.Cache.Flush(ctx, name); e != nil {
			return nil, e
		}
	}
	return w.findPhysical(ctx, name, key)
}

// findPhysical is used for cache misses while the cache lock is held. The
// public Find boundary first makes pending writes visible to direct callers.
func (w *Writer) findPhysical(ctx context.Context, name string, key Row) (Row, error) {
	t, e := w.s.model(name)
	if e != nil {
		return nil, e
	}
	before, e := snapshot(ctx, w.tx, w.s.table(name), t, key)
	if before == nil || e != nil {
		return nil, e
	}
	return decodeSnapshot(t, *before)
}
func (w *Writer) Write(ctx context.Context, name string, row Row, mode WriteMode) (err error) {
	if w.closed.Load() {
		return &NonRetryableUserError{Message: "A store API method was called after the indexing function returned."}
	}
	defer func() {
		if err != nil {
			w.err = errors.Join(w.err, err)
		}
	}()
	if orm := w.indexingSession(); orm != nil {
		if e := orm.Cache.Flush(ctx); e != nil {
			return e
		}
		orm.Cache.Invalidate()
		orm.Cache.Clear()
	}
	t, e := w.s.model(name)
	if e != nil {
		return e
	}
	allowed := map[string]schema.Column{}
	for _, c := range t.Columns {
		allowed[c.Name] = c
	}
	for c := range row {
		if _, ok := allowed[c]; !ok {
			return fmt.Errorf("unknown column %s.%s", name, c)
		}
	}
	if w.s.Ordering == schema.Isolated && fmt.Sprint(row["chain_id"]) != strconv.FormatUint(w.event.Checkpoint.ChainID, 10) {
		return fmt.Errorf("isolated write must match event chain")
	}
	where, keyValues, e := keyWhere(t, row, 1)
	if e != nil {
		return e
	}
	before, e := snapshot(ctx, w.tx, w.s.table(name), t, row)
	if e != nil {
		return e
	}
	if mode == Update && before == nil {
		return fmt.Errorf("cannot update missing row")
	}
	if mode == Insert && before != nil {
		return fmt.Errorf("duplicate primary key")
	}
	if mode == Delete && before == nil {
		return nil
	}
	if before != nil && mode != Insert && mode != Delete {
		columns := []string{}
		for c := range row {
			isKey := false
			for _, k := range t.Keys() {
				isKey = isKey || c == k
			}
			if !isKey {
				columns = append(columns, c)
			}
		}
		sort.Strings(columns)
		if len(columns) > 0 {
			sets := []string{}
			args := []any{}
			for i, c := range columns {
				sets = append(sets, fmt.Sprintf("%s=$%d", schema.Quote(c), i+1))
				args = append(args, row[c])
			}
			where, keys, e := keyWhere(t, row, len(args)+1)
			if e != nil {
				return e
			}
			args = append(args, keys...)
			e = w.tx.Exec(ctx, "UPDATE "+w.s.table(name)+" SET "+strings.Join(sets, ",")+" WHERE "+where, args...)
		}
	} else if mode == Delete {
		e = w.tx.Exec(ctx, "DELETE FROM "+w.s.table(name)+" WHERE "+where, keyValues...)
	} else {
		values := Row{}
		for k, v := range row {
			values[k] = v
		}
		if before == nil {
			for _, c := range t.Columns {
				if _, ok := values[c.Name]; !ok && c.Default != nil {
					values[c.Name] = c.Default
				}
			}
		}
		columns := []string{}
		for col := range values {
			columns = append(columns, col)
		}
		sort.Strings(columns)
		names, params, updates := []string{}, []string{}, []string{}
		args := []any{}
		pk := map[string]bool{}
		for _, k := range t.Keys() {
			pk[k] = true
		}
		for i, c := range columns {
			names = append(names, schema.Quote(c))
			params = append(params, fmt.Sprintf("$%d", i+1))
			args = append(args, values[c])
			if !pk[c] {
				updates = append(updates, schema.Quote(c)+"=excluded."+schema.Quote(c))
			}
		}
		sql := "INSERT INTO " + w.s.table(name) + " (" + strings.Join(names, ",") + ") VALUES (" + strings.Join(params, ",") + ")"
		if mode == Upsert || mode == Update {
			keys := []string{}
			for _, k := range t.Keys() {
				keys = append(keys, schema.Quote(k))
			}
			sql += " ON CONFLICT (" + strings.Join(keys, ",") + ") "
			if len(updates) == 0 {
				sql += "DO NOTHING"
			} else {
				sql += "DO UPDATE SET " + strings.Join(updates, ",")
			}
		}
		e = w.tx.Exec(ctx, sql, args...)
	}
	if e != nil {
		return e
	}
	w.changed[name] = true
	return nil
}

// DeleteRow returns whether a row existed, matching the indexing-store delete
// result. It uses the same transaction and trigger journal as all other writes.
func (w *Writer) DeleteRow(ctx context.Context, name string, key Row) (bool, error) {
	row, e := w.Find(ctx, name, key)
	if e != nil || row == nil {
		return false, e
	}
	if e = w.Write(ctx, name, key, Delete); e != nil {
		return false, e
	}
	return true, nil
}
func (s *Store) Read(ctx context.Context, name string) (Result, error) {
	if _, e := s.model(name); e != nil {
		return Result{}, e
	}
	return s.Backend.Query(ctx, "SELECT * FROM "+s.table(name))
}
func (s *Store) Progress(ctx context.Context) (map[string]string, error) {
	r, e := s.Backend.Query(ctx, "SELECT chain_id::text AS chain_id, checkpoint FROM "+s.internal("_ponder_go_progress"))
	if e != nil {
		return nil, e
	}
	m := map[string]string{}
	for _, row := range r.Rows {
		m[fmt.Sprint(row["chain_id"])] = fmt.Sprint(row["checkpoint"])
	}
	return m, nil
}
func (s *Store) Subscribe() (chan Change, func()) {
	ch := make(chan Change, 1)
	s.subMu.Lock()
	s.subscribers[ch] = struct{}{}
	s.subMu.Unlock()
	return ch, func() { s.subMu.Lock(); delete(s.subscribers, ch); s.subMu.Unlock() }
}
func (s *Store) notify(tables []string) {
	if len(tables) == 0 {
		return
	}
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- Change{tables}:
		default:
			var old Change
			select {
			case old = <-ch:
			default:
			}
			merged := map[string]bool{}
			for _, name := range old.Tables {
				merged[name] = true
			}
			for _, name := range tables {
				merged[name] = true
			}
			all := []string{}
			for name := range merged {
				all = append(all, name)
			}
			select {
			case ch <- Change{all}:
			default:
			}
		}
	}
}
