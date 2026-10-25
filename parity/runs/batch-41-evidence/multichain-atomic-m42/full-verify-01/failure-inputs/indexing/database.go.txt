package indexing

import (
	"context"
	"errors"
	"strings"
	"sync"

	"ponder.local/ponder-go/store"
)

// DatabaseError preserves the underlying backend error while giving the source
// parseDbError classification an explicit Go representation.
type DatabaseError struct {
	Name        string
	Cause       error
	IsRetryable bool
	Meta        []string
}

func (e *DatabaseError) sourceBaseError()  {}
func (e *DatabaseError) Error() string     { return e.Cause.Error() }
func (e *DatabaseError) Unwrap() error     { return e.Cause }
func (e *DatabaseError) ErrorName() string { return e.Name }
func (e *DatabaseError) Retryable() bool   { return e.IsRetryable }

// ParseDatabaseError follows pinned queryBuilder.ts's ordered message rules.
// Arbitrary backend failures are not retryable. Cancellation remains cancellation.
func ParseDatabaseError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if _, ok := err.(interface{ sourceBaseError() }); ok {
		return err
	}
	switch err.(type) {
	case *store.NonRetryableUserError, *store.RecordNotFoundError, *store.UniqueConstraintError, *store.NotNullConstraintError, *store.BigIntSerializationError, *store.InvalidStoreAccessError:
		return err
	}
	message := err.Error()
	name := ""
	switch {
	case strings.Contains(message, "violates not-null constraint"):
		name = "NotNullConstraintError"
	case strings.Contains(message, "violates unique constraint"):
		name = "UniqueConstraintError"
	case strings.Contains(message, "violates check constraint"):
		name = "CheckConstraintError"
	case strings.Contains(message, "Do not know how to serialize a BigInt") || strings.Contains(message, "cannot serialize BigInt"):
		name = "BigIntSerializationError"
	case strings.Contains(message, "does not exist") || strings.Contains(message, "already exists"):
		name = "NonRetryableUserError"
	}
	if name != "" {
		e := &DatabaseError{Name: name, Cause: err}
		if name == "BigIntSerializationError" {
			e.Meta = []string{"Hint:\n  The JSON column type does not support BigInt values. Use the replaceBigInts() helper function before inserting into the database. Docs: https://ponder.sh/docs/api-reference/ponder-utils#replacebigints"}
		}
		return e
	}
	for _, text := range []string{"terminating connection due to administrator command", "connection to client lost", "too many clients already", "Connection terminated unexpectedly", "ECONNRESET", "ETIMEDOUT", "timeout exceeded when trying to connect"} {
		if strings.Contains(message, text) {
			return &DatabaseError{Name: "DbConnectionError", Cause: err, IsRetryable: true}
		}
	}
	return err
}

type DatabaseErrorClassifier func(error) error

// Database never starts, commits, or substitutes a transaction. Its underlying
// IndexingStore and cache are the real transaction-bound instances supplied by caller.
type Database struct {
	underlying    *store.IndexingStore
	errors        RetryableErrorHandler
	classify      DatabaseErrorClassifier
	session       *store.IndexingSession
	bindingMu     sync.Mutex
	binding       *DatabaseBinding
	readOnlyScope func() (func(), error)
}

func NewDatabase(db *store.IndexingStore, handler RetryableErrorHandler, classifiers ...DatabaseErrorClassifier) *Database {
	classify := DatabaseErrorClassifier(ParseDatabaseError)
	if len(classifiers) > 0 && classifiers[0] != nil {
		classify = classifiers[0]
	}
	return &Database{underlying: db, errors: handler, classify: classify}
}
func (db *Database) Underlying() *store.IndexingStore { return db.underlying }
func (db *Database) capture(err error) error {
	if err == nil {
		return nil
	}
	err = db.classify(err)
	var retryable interface{ Retryable() bool }
	if _, known := err.(interface{ sourceBaseError() }); known && errors.As(err, &retryable) && retryable.Retryable() && db.errors != nil {
		db.errors.SetRetryableError(err)
	}
	return err
}
func (db *Database) Find(ctx context.Context, name string, key store.Row) (store.Row, error) {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return nil, db.capture(err)
	}
	defer release()
	v, e := underlying.Find(ctx, name, key)
	return v, db.capture(e)
}
func (db *Database) InsertOne(ctx context.Context, name string, input store.Row, conflict store.Conflict) (store.Row, error) {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return nil, db.capture(err)
	}
	defer release()
	v, e := underlying.InsertOne(ctx, name, input, conflict)
	return v, db.capture(e)
}
func (db *Database) InsertMany(ctx context.Context, name string, input []store.Row, conflict store.Conflict) ([]store.Row, error) {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return nil, db.capture(err)
	}
	defer release()
	v, e := underlying.InsertMany(ctx, name, input, conflict)
	return v, db.capture(e)
}
func (db *Database) Update(ctx context.Context, name string, key, set store.Row) (store.Row, error) {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return nil, db.capture(err)
	}
	defer release()
	v, e := underlying.Update(ctx, name, key, set)
	return v, db.capture(e)
}
func (db *Database) UpdateFunc(ctx context.Context, name string, key store.Row, update store.RowUpdater) (store.Row, error) {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return nil, db.capture(err)
	}
	defer release()
	v, e := underlying.UpdateFunc(ctx, name, key, update)
	return v, db.capture(e)
}
func (db *Database) Delete(ctx context.Context, name string, key store.Row) (bool, error) {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return false, db.capture(err)
	}
	defer release()
	v, e := underlying.Delete(ctx, name, key)
	return v, db.capture(e)
}
func (db *Database) Select(ctx context.Context, name string, key store.Row) ([]store.Row, error) {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return nil, db.capture(err)
	}
	defer release()
	v, e := underlying.Select(ctx, name, key)
	return v, db.capture(e)
}
func (db *Database) SQL(ctx context.Context, query string, args ...any) (store.Result, error) {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return store.Result{}, db.capture(err)
	}
	defer release()
	v, e := underlying.SQL(ctx, query, args...)
	return v, db.capture(e)
}
func (db *Database) Flush(ctx context.Context, tables ...string) error {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return db.capture(err)
	}
	defer release()
	return db.capture(underlying.Cache.Flush(ctx, tables...))
}

// SetProfileEvent is retained for fixed, legacy transaction adapters. A session
// requires SetProfileEventContext so an escaped caller cannot alter later profiles.
func (db *Database) SetProfileEvent(event store.ProfileEvent) error {
	return db.SetProfileEventContext(context.Background(), event)
}
func (db *Database) SetProfileEventContext(ctx context.Context, event store.ProfileEvent) error {
	underlying, release, err := db.acquire(ctx)
	if err != nil {
		return db.capture(err)
	}
	defer release()
	return db.capture(underlying.Cache.SetProfileEvent(event))
}
