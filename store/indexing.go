package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/sqlquery"
	"reflect"
	"sync"
	"time"
)

// Undefined distinguishes an omitted update value from SQL NULL (nil).
var Undefined = undefinedValue{}

type undefinedValue struct{}

func isUndefined(value any) bool { _, ok := value.(undefinedValue); return ok }

type NonRetryableUserError struct{ Message string }

func (e *NonRetryableUserError) Error() string { return e.Message }

type RecordNotFoundError struct{ NonRetryableUserError }
type UniqueConstraintError struct{ NonRetryableUserError }
type NotNullConstraintError struct{ NonRetryableUserError }
type BigIntSerializationError struct{ NonRetryableUserError }
type InvalidStoreAccessError struct{ NonRetryableUserError }

type CacheOptions struct {
	Complete bool
	MaxBytes int64
	Profile  *CacheProfile
}
type ConflictAction int

const (
	ConflictError ConflictAction = iota
	ConflictNothing
	ConflictUpdate
)

type RowUpdater func(Row) (Row, error)
type Conflict struct {
	Action ConflictAction
	Set    Row
	Update RowUpdater
}

type IndexingStore struct {
	readonly bool
	writer   *Writer
	Cache    *IndexingCache
}

// Close ends the native handler scope. Store.Apply also closes it automatically.
func (db *IndexingStore) Close() { db.writer.closed.Store(true) }

type tableCache struct {
	prefetched, spillover    map[string]bool
	diskReads                uint64
	bytes                    int64
	rows                     map[string]Row
	inserts                  map[string]Row
	updates                  map[string]Row
	insertOrder, updateOrder []string
	complete                 bool
}
type IndexingCache struct {
	profileState *CacheProfile
	profileEvent *ProfileEvent
	maxBytes     int64
	mu           sync.Mutex
	db           *IndexingStore
	tables       map[string]*tableCache
}

func (w *Writer) indexingSession() *IndexingStore {
	w.ormMu.Lock()
	defer w.ormMu.Unlock()
	return w.orm
}

// Indexing supplies the buffered native ORM for the current event. A complete
// cache hint is only appropriate when the caller owns all existing table rows.
func (w *Writer) Indexing(options ...CacheOptions) *IndexingStore {
	w.ormMu.Lock()
	defer w.ormMu.Unlock()
	return w.indexingLocked(options...)
}

// indexingLocked requires ormMu, including when installing a session binding.
func (w *Writer) indexingLocked(options ...CacheOptions) *IndexingStore {
	if w.orm != nil {
		return w.orm
	}
	complete := len(options) > 0 && options[0].Complete
	db := &IndexingStore{writer: w}
	profile := w.s.profileState
	maxBytes := int64(128 << 20)
	if len(options) > 0 {
		if options[0].Profile != nil {
			profile = options[0].Profile
		}
		if options[0].MaxBytes > 0 {
			maxBytes = options[0].MaxBytes
		}
	}
	if profile == nil {
		profile = NewCacheProfile()
	}
	db.Cache = &IndexingCache{db: db, tables: map[string]*tableCache{}, profileState: profile, maxBytes: maxBytes}
	for _, table := range w.s.Schema.Tables {
		db.Cache.tables[table.Name] = &tableCache{rows: map[string]Row{}, inserts: map[string]Row{}, updates: map[string]Row{}, complete: complete, prefetched: map[string]bool{}, spillover: map[string]bool{}}
	}
	w.orm = db
	return db
}

func cloneValue(value any) any {
	if value == nil {
		return nil
	}
	if n, ok := value.(*big.Int); ok {
		if n == nil {
			return nil
		}
		return new(big.Int).Set(n)
	}
	if value, ok := value.(*time.Time); ok {
		if value == nil {
			return nil
		}
		copy := *value
		return &copy
	}
	switch v := value.(type) {
	case Row:
		return cloneRow(v)
	case map[string]any:
		out := map[string]any{}
		for k, v := range v {
			out[k] = cloneValue(v)
		}
		return out
	}
	r := reflect.ValueOf(value)
	if r.Kind() == reflect.Slice {
		out := reflect.MakeSlice(r.Type(), r.Len(), r.Len())
		for i := 0; i < r.Len(); i++ {
			v := cloneValue(r.Index(i).Interface())
			if v == nil {
				out.Index(i).SetZero()
			} else {
				out.Index(i).Set(reflect.ValueOf(v))
			}
		}
		return out.Interface()
	}
	return value
}
func cloneRow(row Row) Row {
	if row == nil {
		return nil
	}
	out := Row{}
	for k, v := range row {
		out[k] = cloneValue(v)
	}
	return out
}
func property(c schema.Column) string {
	if c.Property != "" {
		return c.Property
	}
	return c.Name
}

func (db *IndexingStore) active() error {
	if db.writer.closed.Load() {
		return &NonRetryableUserError{Message: "A store API method (find, update, insert, delete) was called after the indexing function returned."}
	}
	return nil
}
func (db *IndexingStore) writable() error {
	if e := db.active(); e != nil {
		return e
	}
	if db.readonly {
		return &NonRetryableUserError{Message: "read-only indexing binding cannot write"}
	}
	return nil
}
func (db *IndexingStore) table(name string) (schema.Table, *tableCache, error) {
	t, e := db.writer.s.model(name)
	if e != nil {
		return t, nil, e
	}
	return t, db.Cache.tables[name], nil
}

func (db *IndexingStore) checkAccess(table schema.Table, method string, key Row) error {
	if db.writer.s.Ordering != schema.Isolated {
		return nil
	}
	value, present := key["chainId"]
	if present && fmt.Sprint(value) == fmt.Sprint(db.writer.event.Checkpoint.ChainID) {
		return nil
	}
	if present {
		return &InvalidStoreAccessError{NonRetryableUserError{fmt.Sprintf("db.%s(%s) cannot access rows on different chains when ordering is 'isolated'.", method, table.Name)}}
	}
	return &InvalidStoreAccessError{NonRetryableUserError{fmt.Sprintf("db.%s(%s) must specify 'chainId' when ordering is 'isolated'.", method, table.Name)}}
}
func cacheKey(t schema.Table, key Row) (string, error) {
	values := []any{}
	for _, physical := range t.Keys() {
		name := physical
		for _, c := range t.Columns {
			if c.Name == physical {
				name = property(c)
				break
			}
		}
		v, ok := key[name]
		if !ok || v == nil || isUndefined(v) {
			return "", fmt.Errorf("missing primary key %s", name)
		}
		for _, column := range t.Columns {
			if property(column) == name {
				var e error
				v, e = NormalizeColumn(column, v, false)
				if e != nil {
					return "", e
				}
				break
			}
		}
		if n, ok := v.(*big.Int); ok {
			v = n.String()
		}
		values = append(values, v)
	}
	raw, e := json.Marshal(values)
	return string(raw), e
}
func (db *IndexingStore) get(ctx context.Context, t schema.Table, cache *tableCache, key Row) (Row, error) {
	if e := db.Cache.recordProfile(t, key); e != nil {
		return nil, e
	}
	ck, e := cacheKey(t, key)
	if e != nil {
		return nil, e
	}
	if row, ok := cache.updates[ck]; ok {
		return row, nil
	}
	if row, ok := cache.inserts[ck]; ok {
		return row, nil
	}
	if row, ok := cache.rows[ck]; ok {
		if !cache.complete && !cache.prefetched[ck] {
			cache.spillover[ck] = true
		}
		return row, nil
	}
	cache.diskReads++
	if cache.complete {
		return nil, nil
	}
	cache.spillover[ck] = true
	physical := Row{}
	for _, c := range t.Columns {
		if v, ok := key[property(c)]; ok {
			var e error
			physical[c.Name], e = NormalizeColumn(c, v, false)
			if e != nil {
				return nil, e
			}
		}
	}
	row, e := db.writer.findPhysical(ctx, t.Name, physical)
	if e != nil {
		return nil, e
	}
	if row != nil {
		logical := Row{}
		for _, c := range t.Columns {
			logical[property(c)] = row[c.Name]
		}
		row = logical
	}
	cache.rows[ck] = row
	return row, nil
}
func (db *IndexingStore) Find(ctx context.Context, name string, key Row) (Row, error) {
	db.Cache.mu.Lock()
	defer db.Cache.mu.Unlock()
	if e := db.active(); e != nil {
		return nil, e
	}
	t, cache, e := db.table(name)
	if e != nil {
		return nil, e
	}
	if e = db.checkAccess(t, "find", key); e != nil {
		return nil, e
	}
	row, e := db.get(ctx, t, cache, key)
	return cloneRow(row), e
}
func normalizeInsert(t schema.Table, input Row) (Row, error) {
	row := Row{}
	for key, value := range input {
		row[key] = value
	}
	for _, c := range t.Columns {
		name := property(c)
		v, present := row[name]
		if !present {
			v = Undefined
		}
		var e error
		v, e = NormalizeColumn(c, v, false)
		if e != nil {
			return nil, e
		}
		if v == nil && (c.NotNull || c.Primary) && !c.HasDefault && c.Default == nil && c.DefaultFn == nil && c.OnUpdateFn == nil {
			return nil, &NotNullConstraintError{NonRetryableUserError{fmt.Sprintf("Column '%s.%s' violates not-null constraint.", t.Name, name)}}
		}
		row[name] = cloneValue(v)
	}
	return row, nil
}
func (db *IndexingStore) insert(ctx context.Context, t schema.Table, cache *tableCache, input Row, conflict Conflict) (Row, error) {
	if e := db.checkAccess(t, "insert", input); e != nil {
		return nil, e
	}
	key, e := cacheKey(t, input)
	if e != nil {
		return nil, e
	}
	if conflict.Action != ConflictError || db.writer.s.Backend.Dialect() == "pglite" {
		previous, e := db.get(ctx, t, cache, input)
		if e != nil {
			return nil, e
		}
		if previous != nil {
			switch conflict.Action {
			case ConflictNothing:
				return nil, nil
			case ConflictUpdate:
				return db.update(ctx, t, cache, input, conflict.Set, conflict.Update)
			default:
				return nil, &UniqueConstraintError{NonRetryableUserError{fmt.Sprintf("Primary key conflict in table '%s'", t.Name)}}
			}
		}
	}
	row, e := normalizeInsert(t, input)
	if e != nil {
		return nil, e
	}
	if _, ok := cache.inserts[key]; !ok {
		cache.insertOrder = append(cache.insertOrder, key)
	}
	cache.inserts[key] = row
	return cloneRow(row), nil
}
func (db *IndexingStore) InsertOne(ctx context.Context, name string, input Row, conflict Conflict) (Row, error) {
	db.Cache.mu.Lock()
	defer db.Cache.mu.Unlock()
	if e := db.writable(); e != nil {
		return nil, e
	}
	t, cache, e := db.table(name)
	if e != nil {
		return nil, e
	}
	return db.insert(ctx, t, cache, input, conflict)
}

func (db *IndexingStore) InsertMany(ctx context.Context, name string, inputs []Row, conflict Conflict) ([]Row, error) {
	db.Cache.mu.Lock()
	defer db.Cache.mu.Unlock()
	if e := db.writable(); e != nil {
		return nil, e
	}
	t, cache, e := db.table(name)
	if e != nil {
		return nil, e
	}
	rows := make([]Row, 0, len(inputs))
	for _, input := range inputs {
		row, e := db.insert(ctx, t, cache, input, conflict)
		if e != nil {
			return nil, e
		}
		rows = append(rows, row)
	}
	return rows, nil
}
func (db *IndexingStore) update(ctx context.Context, t schema.Table, cache *tableCache, key, set Row, update RowUpdater) (Row, error) {
	if e := db.checkAccess(t, "update", key); e != nil {
		return nil, e
	}
	previous, e := db.get(ctx, t, cache, key)
	if e != nil {
		return nil, e
	}
	if previous == nil {
		return nil, &RecordNotFoundError{NonRetryableUserError{fmt.Sprintf("No existing record found in table '%s'", t.Name)}}
	}
	row := cloneRow(previous)
	if update != nil {
		set, e = update(cloneRow(row))
		if e != nil {
			return nil, e
		}
	}
	for _, physical := range t.Keys() {
		for _, c := range t.Columns {
			if c.Name == physical {
				if value, present := set[property(c)]; present && !reflect.DeepEqual(value, row[property(c)]) {
					return nil, &NonRetryableUserError{fmt.Sprintf("Primary key column '%s' cannot be updated", property(c))}
				}
			}
		}
	}
	set, e = NormalizeUpdateSet(t, set)
	if e != nil {
		return nil, e
	}
	for name, value := range set {
		if !isUndefined(value) {
			row[name] = cloneValue(value)
		}
	}
	ck, e := cacheKey(t, key)
	if e != nil {
		return nil, e
	}
	if _, ok := cache.updates[ck]; !ok {
		cache.updateOrder = append(cache.updateOrder, ck)
	}
	cache.updates[ck] = row
	return cloneRow(row), nil
}
func (db *IndexingStore) Update(ctx context.Context, name string, key, set Row) (Row, error) {
	db.Cache.mu.Lock()
	defer db.Cache.mu.Unlock()
	if e := db.writable(); e != nil {
		return nil, e
	}
	t, cache, e := db.table(name)
	if e != nil {
		return nil, e
	}
	return db.update(ctx, t, cache, key, set, nil)
}

func (db *IndexingStore) UpdateFunc(ctx context.Context, name string, key Row, update RowUpdater) (Row, error) {
	db.Cache.mu.Lock()
	defer db.Cache.mu.Unlock()
	if e := db.writable(); e != nil {
		return nil, e
	}
	t, cache, e := db.table(name)
	if e != nil {
		return nil, e
	}
	return db.update(ctx, t, cache, key, nil, update)
}

func (db *IndexingStore) SQL(ctx context.Context, query string, args ...any) (Result, error) {
	if e := db.active(); e != nil {
		return Result{}, e
	}
	if db.readonly && !sqlquery.IsReadonly(query) {
		return Result{}, &NonRetryableUserError{Message: "read-only indexing binding cannot write SQL"}
	}
	return db.writer.SQL(ctx, query, args...)
}

// Select provides a typed table query over the same raw-SQL/cache boundary.
// It is a native table operation; complete Drizzle query-builder parity remains
// separate from accepting an arbitrary SQL string.
func (db *IndexingStore) Select(ctx context.Context, name string, key Row) ([]Row, error) {
	if e := db.active(); e != nil {
		return nil, e
	}
	table, e := db.writer.s.model(name)
	if e != nil {
		return nil, e
	}
	query := "SELECT row_to_json(t)::text AS row FROM " + db.writer.s.table(name) + " AS t"
	args := []any{}
	if len(key) > 0 {
		physical := Row{}
		for _, column := range table.Columns {
			if v, ok := key[property(column)]; ok {
				normalized, e := NormalizeColumn(column, v, false)
				if e != nil {
					return nil, e
				}
				physical[column.Name] = normalized
			}
		}
		where, values, e := keyWhere(table, physical, 1)
		if e != nil {
			return nil, e
		}
		query += " WHERE " + where
		args = values
	}
	result, e := db.SQL(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	rows := make([]Row, 0, len(result.Rows))
	for _, raw := range result.Rows {
		physical, e := decodeSnapshot(table, fmt.Sprint(raw["row"]))
		if e != nil {
			return nil, e
		}
		logical := Row{}
		for _, column := range table.Columns {
			logical[property(column)] = physical[column.Name]
		}
		rows = append(rows, logical)
	}
	return rows, nil
}

func (db *IndexingStore) Delete(ctx context.Context, name string, key Row) (bool, error) {
	db.Cache.mu.Lock()
	defer db.Cache.mu.Unlock()
	if e := db.writable(); e != nil {
		return false, e
	}
	t, cache, e := db.table(name)
	if e != nil {
		return false, e
	}
	if e = db.checkAccess(t, "delete", key); e != nil {
		return false, e
	}
	ck, e := cacheKey(t, key)
	if e != nil {
		return false, e
	}
	physical := Row{}
	for _, column := range t.Columns {
		if v, ok := key[property(column)]; ok {
			physical[column.Name], e = NormalizeColumn(column, v, false)
			if e != nil {
				return false, e
			}
		}
	}
	where, args, e := keyWhere(t, physical, 1)
	if e != nil {
		return false, e
	}
	_, inserted := cache.inserts[ck]
	_, updated := cache.updates[ck]
	result, e := db.writer.tx.Query(ctx, "DELETE FROM "+db.writer.s.table(name)+" WHERE "+where+" RETURNING *", args...)
	if e != nil {
		return false, e
	}
	delete(cache.inserts, ck)
	delete(cache.updates, ck)
	delete(cache.rows, ck)
	// A later reinsert must have one new position, like Map.delete()/set() in
	// the original buffers. Stale order entries would emit the row twice.
	remove := func(order []string) []string {
		out := order[:0]
		for _, key := range order {
			if key != ck {
				out = append(out, key)
			}
		}
		return out
	}
	cache.insertOrder = remove(cache.insertOrder)
	cache.updateOrder = remove(cache.updateOrder)
	return inserted || updated || len(result.Rows) > 0, nil
}
func (cache *IndexingCache) Clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.db.writer.closed.Load() {
		return
	}
	for _, c := range cache.tables {
		c.rows = map[string]Row{}
		c.inserts = map[string]Row{}
		c.updates = map[string]Row{}
		c.insertOrder = nil
		c.updateOrder = nil
		c.spillover = map[string]bool{}
	}
}
func (cache *IndexingCache) Invalidate() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.db.writer.closed.Load() {
		return
	}
	for _, c := range cache.tables {
		c.complete = false
	}
}
func (cache *IndexingCache) Flush(ctx context.Context, tables ...string) error {
	return cache.flush(ctx, true, tables...)
}
func (cache *IndexingCache) flush(ctx context.Context, checkActive bool, tables ...string) (err error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if checkActive {
		if e := cache.db.active(); e != nil {
			return e
		}
	}
	if cache.db.readonly {
		return nil
	}
	selected := map[string]bool{}
	for _, table := range tables {
		selected[table] = true
	}
	w := cache.db.writer
	if e := w.tx.Exec(ctx, "SAVEPOINT ponder_go_flush"); e != nil {
		return e
	}
	success := false
	defer func() {
		if !success {
			rollbackErr := w.tx.Exec(context.Background(), "ROLLBACK TO SAVEPOINT ponder_go_flush")
			releaseErr := w.tx.Exec(context.Background(), "RELEASE SAVEPOINT ponder_go_flush")
			if recoveryErr := errors.Join(rollbackErr, releaseErr); recoveryErr != nil {
				w.err = errors.Join(w.err, recoveryErr)
				err = errors.Join(err, recoveryErr)
			}
		}
	}()
	for _, t := range cache.db.writer.s.Schema.Tables {
		if len(tables) > 0 && !selected[t.Name] {
			continue
		}
		c := cache.tables[t.Name]
		for _, buffer := range []struct {
			rows  map[string]Row
			order []string
			mode  WriteMode
		}{{c.inserts, c.insertOrder, Insert}, {c.updates, c.updateOrder, Upsert}} {
			rows := []Row{}
			for _, key := range buffer.order {
				if row := buffer.rows[key]; row != nil {
					rows = append(rows, row)
				}
			}
			if len(rows) == 0 {
				continue
			}
			if e := w.s.writeRows(ctx, w.tx, t, rows, buffer.mode); e != nil {
				return e
			}
		}
	}
	if e := w.tx.Exec(ctx, "RELEASE SAVEPOINT ponder_go_flush"); e != nil {
		return e
	}
	success = true
	for _, t := range w.s.Schema.Tables {
		if len(tables) > 0 && !selected[t.Name] {
			continue
		}
		c := cache.tables[t.Name]
		for key, row := range c.inserts {
			if _, exists := c.rows[key]; c.complete && !exists {
				c.bytes += cacheValueBytes(row) + cacheValueBytes(key)
			}
			c.rows[key] = row
		}
		for key, row := range c.updates {
			if _, exists := c.rows[key]; c.complete && !exists {
				c.bytes += cacheValueBytes(row) + cacheValueBytes(key)
			}
			c.rows[key] = row
		}
		c.inserts = map[string]Row{}
		c.updates = map[string]Row{}
		c.insertOrder = nil
		c.updateOrder = nil
	}
	return nil
}
