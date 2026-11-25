package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SourceQueryAttempt reports a labeled operation after one attempt.
type SourceQueryAttempt struct {
	Label       string
	Transaction bool
	Number      int
	Duration    time.Duration
	Err         error
}

type CheckConstraintError struct{ NonRetryableUserError }
type DBConnectionError struct{ Cause error }

func (e *DBConnectionError) Error() string { return e.Cause.Error() }
func (e *DBConnectionError) Unwrap() error { return e.Cause }

// As also matches the embedded base of each constraint or application error.
func (e *NonRetryableUserError) As(target any) bool {
	base, ok := target.(**NonRetryableUserError)
	if ok {
		*base = e
	}
	return ok
}

func isNonRetryableDBError(err error) bool {
	var user *NonRetryableUserError
	return errors.As(err, &user)
}

// ParseDBError preserves existing application errors and identifies PostgreSQL
// SQLSTATEs or driver messages used by both supported backends.
func ParseDBError(err error) error {
	if err == nil || isNonRetryableDBError(err) {
		return err
	}
	var connection *DBConnectionError
	if errors.As(err, &connection) {
		return err
	}
	message := err.Error()
	user := NonRetryableUserError{Message: message}
	var pg interface{ SQLState() string }
	var code string
	if errors.As(err, &pg) {
		code = pg.SQLState()
	}
	switch {
	case code == "23502" || strings.Contains(message, "violates not-null constraint"):
		return &NotNullConstraintError{user}
	case code == "23505" || strings.Contains(message, "violates unique constraint"):
		return &UniqueConstraintError{user}
	case code == "23514" || strings.Contains(message, "violates check constraint"):
		return &CheckConstraintError{user}
	case strings.Contains(message, "Do not know how to serialize a BigInt") || strings.Contains(message, "cannot serialize BigInt"):
		return &BigIntSerializationError{user}
	case strings.Contains(message, "does not exist") || strings.Contains(message, "already exists"):
		return &user
	}
	for _, part := range []string{"terminating connection due to administrator command", "connection to client lost", "too many clients already", "Connection terminated unexpectedly", "ECONNRESET", "ETIMEDOUT", "timeout exceeded when trying to connect"} {
		if strings.Contains(message, part) {
			return &DBConnectionError{err}
		}
	}
	return err
}

// ErrIndeterminateCommit means the caller must establish the transaction's
// outcome before replaying it.
var ErrIndeterminateCommit = errors.New("database commit outcome is unknown")

type beginSourceQuery func(context.Context) (Transaction, any, func(), error)

type sourceTxFailure struct{ err error }

type SourceQueryBuilder struct {
	gate      sync.RWMutex
	txFailure *atomic.Pointer[sourceTxFailure]
	raw       Querier
	client    any
	dialect   string
	begin     beginSourceQuery
	observe   func(SourceQueryAttempt)
	inTx      bool
	depth     int
	closed    atomic.Bool
}

// NewSourceQueryBuilder borrows a backend. The caller owns its lifetime.
// Source PGlite transaction callbacks use the same client, as in createQB.
// Durable writes requiring an atomic PGlite transaction use Backend.Begin.
func NewSourceQueryBuilder(b Backend, observe func(SourceQueryAttempt)) (*SourceQueryBuilder, error) {
	if b == nil {
		return nil, errors.New("query builder requires a backend")
	}
	q := &SourceQueryBuilder{raw: b, client: b, dialect: b.Dialect(), observe: observe}
	switch q.dialect {
	case "pglite":
		return q, nil
	case "postgres":
		if pg, ok := b.(*pgBackend); ok {
			q.client = pg.db
			q.begin = func(ctx context.Context) (Transaction, any, func(), error) {
				conn, err := pg.db.Conn(ctx)
				if err != nil {
					return nil, nil, nil, err
				}
				return beginSourceConnection(ctx, conn, func() { conn.Close() })
			}
		} else {
			// A caller-provided backend retains its own connection ownership.
			q.begin = func(ctx context.Context) (Transaction, any, func(), error) {
				tx, err := b.Begin(ctx)
				return tx, tx, func() {}, err
			}
		}
		return q, nil
	default:
		return nil, fmt.Errorf("unsupported query builder dialect %q", q.dialect)
	}
}

type sourceSQLConnection struct{ conn *sql.Conn }

func (c sourceSQLConnection) Query(ctx context.Context, s string, args ...any) (Result, error) {
	return pgQuery(ctx, c.conn, s, args...)
}
func (c sourceSQLConnection) Exec(ctx context.Context, s string, args ...any) error {
	_, err := c.conn.ExecContext(ctx, s, databaseArgs(args)...)
	return err
}

// NewSourceConnectionQueryBuilder borrows one dedicated PostgreSQL connection.
func NewSourceConnectionQueryBuilder(conn *sql.Conn, observe func(SourceQueryAttempt)) (*SourceQueryBuilder, error) {
	if conn == nil {
		return nil, errors.New("query builder requires a connection")
	}
	q := &SourceQueryBuilder{raw: sourceSQLConnection{conn}, client: conn, dialect: "postgres", observe: observe}
	q.begin = func(ctx context.Context) (Transaction, any, func(), error) {
		return beginSourceConnection(ctx, conn, func() {})
	}
	return q, nil
}

func beginSourceConnection(ctx context.Context, conn *sql.Conn, release func()) (Transaction, any, func(), error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	return &pgTx{tx}, conn, release, nil
}

func (q *SourceQueryBuilder) Dialect() string { return q.dialect }
func (q *SourceQueryBuilder) Client() any     { return q.client }

// Wrap may replay the callback outside a transaction. Use a replay-safe query
// here and use Transaction for a unit of dependent PostgreSQL writes.
func (q *SourceQueryBuilder) Wrap(ctx context.Context, label string, fn func(Querier) error) error {
	if q.inTx {
		if !q.gate.TryRLock() {
			return &NonRetryableUserError{Message: "parent transaction scope is busy"}
		}
		defer q.gate.RUnlock()
	}
	return q.run(ctx, label, false, !q.inTx, func() error { return fn(q.raw) })
}

func (q *SourceQueryBuilder) Query(ctx context.Context, statement string, args ...any) (Result, error) {
	var r Result
	err := q.Wrap(ctx, "", func(db Querier) error {
		var err error
		r, err = db.Query(ctx, statement, args...)
		return err
	})
	return r, err
}

func (q *SourceQueryBuilder) run(ctx context.Context, label string, transaction, retry bool, fn func() error) error {
	var first error
	for attempt := 0; attempt <= 9; attempt++ {
		if q.txFailure != nil {
			if failed := q.txFailure.Load(); failed != nil {
				return failed.err
			}
		}
		if q.closed.Load() {
			return sql.ErrTxDone
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		start := time.Now()
		err := fn()
		if !errors.Is(err, ErrIndeterminateCommit) {
			err = ParseDBError(err)
		}
		if label != "" && q.observe != nil {
			q.observe(SourceQueryAttempt{Label: label, Transaction: transaction, Number: attempt + 1, Duration: time.Since(start), Err: err})
		}
		if q.txFailure != nil {
			if failed := q.txFailure.Load(); failed != nil {
				return errors.Join(err, failed.err)
			}
		}
		if err == nil || errors.Is(err, ErrIndeterminateCommit) {
			return err
		}
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if !retry || isNonRetryableDBError(err) || errors.Is(err, sql.ErrTxDone) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if attempt == 0 {
			first = err
		}
		if attempt < 9 {
			timer := time.NewTimer((125 * time.Millisecond) << attempt)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return first
}

func (q *SourceQueryBuilder) Transaction(ctx context.Context, label string, fn func(*SourceQueryBuilder) error) error {
	if q.dialect == "pglite" {
		return q.run(ctx, label, true, false, func() error { return fn(q) })
	}
	if q.inTx {
		if !q.gate.TryLock() {
			return &NonRetryableUserError{Message: "parent transaction scope is busy"}
		}
		defer q.gate.Unlock()
		return q.run(ctx, label, true, true, func() error { return q.savepoint(ctx, fn) })
	}
	return q.run(ctx, label, true, true, func() error {
		tx, client, release, err := q.begin(ctx)
		if err != nil {
			return err
		}
		defer release()
		child := &SourceQueryBuilder{raw: tx, client: client, dialect: q.dialect, inTx: true, observe: q.observe, txFailure: new(atomic.Pointer[sourceTxFailure])}
		finished := false
		defer func() {
			if !finished {
				_ = tx.Rollback(context.WithoutCancel(ctx))
			}
		}()
		if err := child.invoke(fn); err != nil {
			rb := tx.Rollback(context.WithoutCancel(ctx))
			finished = true
			if rb != nil && !errors.Is(rb, sql.ErrTxDone) {
				return errors.Join(err, rb)
			}
			return err
		}
		err = tx.Commit(ctx)
		finished = true
		if err != nil {
			// A transport failure after COMMIT may have applied every write.
			// PostgreSQL's serialization and deadlock errors establish rollback.
			var pg interface{ SQLState() string }
			if errors.As(err, &pg) && (pg.SQLState() == "40001" || pg.SQLState() == "40P01") {
				return err
			}
			if errors.As(err, &pg) && strings.HasPrefix(pg.SQLState(), "23") {
				return ParseDBError(err)
			}
			return errors.Join(ErrIndeterminateCommit, err)
		}
		return nil
	})
}

// Close the scope before releasing its savepoint or committing. Detached calls
// that already entered the scope must return their borrowed query handles.
func (q *SourceQueryBuilder) invoke(fn func(*SourceQueryBuilder) error) (err error) {
	defer func() {
		q.gate.Lock()
		defer q.gate.Unlock()
		q.closed.Store(true)
		if failed := q.txFailure.Load(); failed != nil {
			err = errors.Join(err, failed.err)
		}
	}()
	return fn(q)
}

// Nested transactions serialize savepoints on the borrowed connection.
func (q *SourceQueryBuilder) savepoint(ctx context.Context, fn func(*SourceQueryBuilder) error) (result error) {
	depth := q.depth + 1
	name := fmt.Sprintf("ponder_qb_%d", depth)
	if err := q.raw.Exec(ctx, "SAVEPOINT "+name); err != nil {
		q.txFailure.CompareAndSwap(nil, &sourceTxFailure{err})
		return err
	}
	child := &SourceQueryBuilder{raw: q.raw, client: q.client, dialect: q.dialect, inTx: true, depth: depth, observe: q.observe, txFailure: q.txFailure}
	released := false
	defer func() {
		if released {
			return
		}
		cleanup := context.WithoutCancel(ctx)
		err := q.raw.Exec(cleanup, "ROLLBACK TO SAVEPOINT "+name)
		if err == nil {
			err = q.raw.Exec(cleanup, "RELEASE SAVEPOINT "+name)
		}
		if err != nil {
			q.txFailure.CompareAndSwap(nil, &sourceTxFailure{err})
			result = errors.Join(result, err)
		}
	}()
	if err := child.invoke(fn); err != nil {
		return err
	}
	result = q.raw.Exec(ctx, "RELEASE SAVEPOINT "+name)
	released = result == nil
	return result
}
