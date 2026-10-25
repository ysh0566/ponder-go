package store

import (
	"context"
	"fmt"
	"sync"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

// IndexingSession retains committed row predictions and learned profiles across
// sequential transactions. Each binding has its own immutable writer and cache;
// an escaped binding is never retargeted to a later transaction. The owner must
// call Finish after the enclosing transaction returns, passing its actual error
// (including an uncertain commit), not merely the callback's result.
// External writes/reverts require Invalidate before the next binding.
type IndexingSession struct {
	reading bool
	mu      sync.Mutex
	owner   *Store
	options CacheOptions
	active  *IndexingStore
	tables  map[string]*tableCache
}

func NewIndexingSession(owner *Store, options ...CacheOptions) *IndexingSession {
	s := &IndexingSession{owner: owner}
	if len(options) > 0 {
		s.options = options[0]
	}
	return s
}

// NewIndexingSession binds the session's ownership to this Actions instance.
func (a *Actions) NewIndexingSession(options ...CacheOptions) *IndexingSession {
	return NewIndexingSession(a.s, options...)
}

func (s *IndexingSession) Bind(w *Writer) (*IndexingStore, error) {
	return s.bind(w, false)
}
func (s *IndexingSession) bind(w *Writer, readonlyAdmission bool) (*IndexingStore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == nil || w == nil || w.s != s.owner {
		return nil, fmt.Errorf("indexing session writer has wrong owner")
	}
	if s.active != nil || (s.reading && !readonlyAdmission) {
		return nil, fmt.Errorf("indexing session already bound")
	}
	if w.closed.Load() {
		return nil, fmt.Errorf("indexing session writer is closed")
	}
	w.ormMu.Lock()
	defer w.ormMu.Unlock()
	if w.orm != nil {
		return nil, fmt.Errorf("indexing session requires an unused writer")
	}
	db := w.indexingLocked(s.options)
	if s.tables != nil {
		db.Cache.tables = copySessionTables(s.tables)
	}
	s.active = db
	return db, nil
}

// Finish revokes the exact binding and retains rows only after a confirmed
// commit. Failure invalidates all rows, negative predictions and pending writes,
// but deliberately retains the independent learned CacheProfile.
func (s *IndexingSession) Finish(db *IndexingStore, transactionErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if db == nil || s.active != db {
		return fmt.Errorf("indexing session binding is not active")
	}
	if !db.writer.closed.Load() {
		return fmt.Errorf("indexing session transaction scope is still active")
	}
	db.Cache.mu.Lock()
	defer db.Cache.mu.Unlock()
	s.active = nil
	s.tables = nil
	if transactionErr != nil {
		clearSessionTables(db.Cache.tables)
		s.options.Complete = false
		return nil
	}
	for _, table := range db.Cache.tables {
		if len(table.inserts) != 0 || len(table.updates) != 0 {
			s.options.Complete = false
			clearSessionTables(db.Cache.tables)
			return fmt.Errorf("indexing session completed with unflushed writes")
		}
	}
	s.tables = copySessionTables(db.Cache.tables)
	return nil
}

// Invalidate is required after an external write/revert. It cannot race a bound
// transaction, and never discards learned access profiles.
func (s *IndexingSession) Invalidate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil || s.reading {
		return fmt.Errorf("cannot invalidate a bound indexing session")
	}
	s.tables = nil
	s.options.Complete = false
	return nil
}

func clearSessionTables(tables map[string]*tableCache) {
	for name := range tables {
		tables[name] = &tableCache{rows: map[string]Row{}, inserts: map[string]Row{}, updates: map[string]Row{}, prefetched: map[string]bool{}, spillover: map[string]bool{}}
	}
}
func copySessionTables(tables map[string]*tableCache) map[string]*tableCache {
	out := make(map[string]*tableCache, len(tables))
	for name, table := range tables {
		t := *table
		t.rows = make(map[string]Row, len(table.rows))
		for key, row := range table.rows {
			t.rows[key] = cloneRow(row)
		}
		t.inserts, t.updates = map[string]Row{}, map[string]Row{}
		t.insertOrder, t.updateOrder = nil, nil
		t.prefetched, t.spillover = map[string]bool{}, map[string]bool{}
		for key, value := range table.prefetched {
			t.prefetched[key] = value
		}
		for key, value := range table.spillover {
			t.spillover[key] = value
		}
		out[name] = &t
	}
	return out
}

// WithReadOnly explicitly owns a short read-only SQL binding for row prefetch
// outside business batches. It never supplies a Writer to the callback. Buffered
// mutations and raw SQL mutations are rejected; the SQL transaction is also set
// READ ONLY. Callback handles are revoked even on panic, and panic/commit errors
// conservatively discard row state. Callers must join callback goroutines.
func (s *IndexingSession) WithReadOnly(ctx context.Context, chain uint64, fn func(*IndexingStore) error) (err error) {
	if s.owner == nil || fn == nil {
		return fmt.Errorf("read-only session requires owner and callback")
	}
	s.mu.Lock()
	if s.active != nil || s.reading {
		s.mu.Unlock()
		return fmt.Errorf("indexing session already bound")
	}
	s.reading = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.reading = false; s.mu.Unlock() }()
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	tx, err := s.owner.Backend.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
		return err
	}
	if err = tx.Exec(ctx, "SET LOCAL search_path TO "+schema.Quote(s.owner.Namespace)+",pg_catalog"); err != nil {
		return err
	}
	if err = s.owner.requireLease(ctx, tx); err != nil {
		return err
	}
	w := &Writer{s: s.owner, tx: tx, event: Event{Checkpoint: checkpoint.Checkpoint{ChainID: chain}}, changed: map[string]bool{}}
	db, err := s.bind(w, true)
	if err != nil {
		return err
	}
	db.readonly = true
	completed := false
	defer func() {
		w.closed.Store(true)
		outcome := err
		if !completed {
			outcome = fmt.Errorf("read-only binding did not complete")
		}
		finishErr := s.Finish(db, outcome)
		if err == nil {
			err = finishErr
		}
	}()
	err = fn(db)
	w.closed.Store(true)
	if err == nil {
		err = tx.Commit(ctx)
	}
	completed = true
	return err
}
