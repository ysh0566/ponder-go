package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	pgquery "github.com/pganalyze/pg_query_go/v5"
	"sync"
)

// All business SQL uses the same non-reconnecting connection that holds the
// lifetime owner locks. Losing that connection cannot silently resume cached
// bindings on another pool connection after a replacement owner is admitted.
type independentPGBackend struct {
	*pgBackend
	connection *sql.Conn
	mu         sync.Mutex
	closed     bool
}

func (p *independentPGBackend) Begin(ctx context.Context) (Transaction, error) {
	p.mu.Lock()
	tx, err := p.connection.BeginTx(ctx, nil)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	return &independentPGTx{Transaction: &pgTx{tx: tx}, release: p.mu.Unlock}, nil
}
func (p *independentPGBackend) Query(ctx context.Context, q string, args ...any) (Result, error) {
	if err := rejectIndependentUnlock(q); err != nil {
		return Result{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return pgQuery(ctx, p.connection, q, args...)
}
func (p *independentPGBackend) Exec(ctx context.Context, q string, args ...any) error {
	if err := rejectIndependentUnlock(q); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.connection.ExecContext(ctx, q, databaseArgs(args)...)
	return err
}
func (p *independentPGBackend) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	e := p.connection.Close()
	if errors.Is(e, sql.ErrConnDone) {
		e = nil
	}
	return errors.Join(e, p.pgBackend.Close())
}

type independentPGTx struct {
	Transaction
	release func()
	once    sync.Once
}

func (t *independentPGTx) Commit(ctx context.Context) error {
	defer t.once.Do(t.release)
	return t.Transaction.Commit(ctx)
}
func (t *independentPGTx) Rollback(ctx context.Context) error {
	defer t.once.Do(t.release)
	return t.Transaction.Rollback(ctx)
}

func (t *independentPGTx) Query(ctx context.Context, q string, args ...any) (Result, error) {
	if err := rejectIndependentUnlock(q); err != nil {
		return Result{}, err
	}
	return t.Transaction.Query(ctx, q, args...)
}
func (t *independentPGTx) Exec(ctx context.Context, q string, args ...any) error {
	if err := rejectIndependentUnlock(q); err != nil {
		return err
	}
	return t.Transaction.Exec(ctx, q, args...)
}
func rejectIndependentUnlock(query string) error {
	parsed, err := pgquery.ParseToJSON(query)
	if err != nil {
		return err
	}
	var tree any
	if err = json.Unmarshal([]byte(parsed), &tree); err != nil {
		return err
	}
	var walk func(any) error
	walk = func(value any) error {
		switch n := value.(type) {
		case map[string]any:
			if fn, ok := n["FuncCall"].(map[string]any); ok {
				names, _ := fn["funcname"].([]any)
				if len(names) > 0 {
					node, _ := names[len(names)-1].(map[string]any)
					str, _ := node["String"].(map[string]any)
					name, _ := str["sval"].(string)
					switch name {
					case "pg_advisory_unlock", "pg_advisory_unlock_all", "pg_advisory_unlock_shared":
						return fmt.Errorf("independent storage owner locks cannot be released by SQL")
					}
				}
			}
			for _, child := range n {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range n {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(tree)
}
