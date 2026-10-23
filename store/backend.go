package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type Result struct {
	Rows         []map[string]any `json:"rows"`
	Fields       []map[string]any `json:"fields,omitempty"`
	AffectedRows int64            `json:"affectedRows,omitempty"`
}
type Querier interface {
	Query(context.Context, string, ...any) (Result, error)
	Exec(context.Context, string, ...any) error
}
type Transaction interface {
	Querier
	Commit(context.Context) error
	Rollback(context.Context) error
}
type Backend interface {
	Querier
	Begin(context.Context) (Transaction, error)
	Close() error
	Dialect() string
}
type pgBackend struct{ db *sql.DB }
type pgTx struct{ tx *sql.Tx }

func OpenPostgres(ctx context.Context, url string) (Backend, error) {
	db, e := sql.Open("pgx", url)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(8)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return &pgBackend{db}, nil
}
func (p *pgBackend) Dialect() string { return "postgres" }
func (p *pgBackend) Close() error    { return p.db.Close() }
func (p *pgBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	return &pgTx{tx}, e
}
func (p *pgBackend) Query(c context.Context, s string, a ...any) (Result, error) {
	return pgQuery(c, p.db, s, a...)
}
func (p *pgBackend) Exec(c context.Context, s string, a ...any) error {
	_, e := p.db.ExecContext(c, s, databaseArgs(a)...)
	return e
}
func (p *pgTx) Query(c context.Context, s string, a ...any) (Result, error) {
	return pgQuery(c, p.tx, s, a...)
}
func (p *pgTx) Exec(c context.Context, s string, a ...any) error {
	_, e := p.tx.ExecContext(c, s, databaseArgs(a)...)
	return e
}
func (p *pgTx) Commit(context.Context) error   { return p.tx.Commit() }
func (p *pgTx) Rollback(context.Context) error { return p.tx.Rollback() }

type sqlQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func pgQuery(ctx context.Context, q sqlQuerier, s string, args ...any) (Result, error) {
	out := Result{Rows: []map[string]any{}}
	rows, e := q.QueryContext(ctx, s, databaseArgs(args)...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	columns, e := rows.Columns()
	if e != nil {
		return out, e
	}
	types, e := rows.ColumnTypes()
	if e != nil {
		return out, e
	}
	for _, name := range columns {
		out.Fields = append(out.Fields, map[string]any{"name": name})
	}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range dest {
			dest[i] = &values[i]
		}
		if e = rows.Scan(dest...); e != nil {
			return out, e
		}
		row := map[string]any{}
		for i, col := range columns {
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			if v != nil && (types[i].DatabaseTypeName() == "INT8" || types[i].DatabaseTypeName() == "NUMERIC") {
				v = fmt.Sprint(v)
			}
			row[col] = v
		}
		out.Rows = append(out.Rows, row)
	}
	return out, rows.Err()
}

type pgliteBackend struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	in     io.WriteCloser
	lines  *bufio.Scanner
	closed bool
}
type pgliteTx struct {
	p      *pgliteBackend
	done   bool
	failed bool
}

func OpenPGlite(ctx context.Context, directory string) (Backend, error) {
	_, file, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(file), "../bridge/pglite.mjs")
	if directory == "" {
		directory = "memory://"
	}
	cmd := exec.CommandContext(ctx, "node", script, directory)
	cmd.Stderr = os.Stderr
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	p := &pgliteBackend{cmd: cmd, in: in, lines: bufio.NewScanner(out)}
	p.lines.Buffer(make([]byte, 64*1024), 32*1024*1024)
	if !p.lines.Scan() {
		p.Close()
		return nil, fmt.Errorf("PGlite did not become ready: %v", p.lines.Err())
	}
	var ready struct{ Ready bool }
	if e = json.Unmarshal(p.lines.Bytes(), &ready); e != nil || !ready.Ready {
		p.Close()
		return nil, fmt.Errorf("invalid PGlite handshake")
	}
	return p, nil
}
func (p *pgliteBackend) Dialect() string { return "pglite" }
func (p *pgliteBackend) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.in.Close()
	return p.cmd.Wait()
}
func (p *pgliteBackend) roundtrip(ctx context.Context, action, sql string, args ...any) (Result, error) {
	out := Result{Rows: []map[string]any{}}
	if p.closed {
		return out, fmt.Errorf("PGlite is closed")
	}
	if e := ctx.Err(); e != nil {
		return out, e
	}
	request, e := json.Marshal(map[string]any{"action": action, "sql": sql, "params": databaseArgs(args)})
	if e != nil {
		return out, e
	}
	if _, e = p.in.Write(append(request, '\n')); e != nil {
		return out, e
	}
	if !p.lines.Scan() {
		return out, fmt.Errorf("PGlite transport ended: %v", p.lines.Err())
	}
	var response struct {
		Result
		Error *struct {
			Message string
			Code    string
		}
	}
	d := json.NewDecoder(strings.NewReader(p.lines.Text()))
	d.UseNumber()
	if e = d.Decode(&response); e != nil {
		return out, e
	}
	if response.Error != nil {
		return out, fmt.Errorf("%s (%s)", response.Error.Message, response.Error.Code)
	}
	return response.Result, nil
}
func (p *pgliteBackend) Query(c context.Context, s string, a ...any) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.roundtrip(c, "query", s, a...)
}
func (p *pgliteBackend) Exec(c context.Context, s string, a ...any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	action := "query"
	if len(a) == 0 {
		action = "exec"
	}
	_, e := p.roundtrip(c, action, s, a...)
	return e
}
func (p *pgliteBackend) Begin(c context.Context) (Transaction, error) {
	p.mu.Lock()
	if _, e := p.roundtrip(c, "query", "BEGIN"); e != nil {
		p.mu.Unlock()
		return nil, e
	}
	return &pgliteTx{p: p}, nil
}
func (t *pgliteTx) Query(c context.Context, s string, a ...any) (Result, error) {
	if t.done {
		return Result{}, fmt.Errorf("transaction is closed")
	}
	r, e := t.p.roundtrip(c, "query", s, a...)
	if e != nil {
		t.failed = true
	}
	if e == nil && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "ROLLBACK TO ") {
		t.failed = false
	}
	return r, e
}
func (t *pgliteTx) Exec(c context.Context, s string, a ...any) error {
	_, e := t.Query(c, s, a...)
	return e
}
func (t *pgliteTx) finish(c context.Context, command string) error {
	if t.done {
		return nil
	}
	defer t.p.mu.Unlock()
	t.done = true
	if command == "COMMIT" && (t.failed || c.Err() != nil) {
		_, rollbackErr := t.p.roundtrip(context.Background(), "query", "ROLLBACK")
		if c.Err() != nil {
			return c.Err()
		}
		if rollbackErr != nil {
			return rollbackErr
		}
		return fmt.Errorf("cannot commit failed transaction")
	}
	if command == "ROLLBACK" {
		c = context.Background()
	}
	_, e := t.p.roundtrip(c, "query", command)
	return e
}
func (t *pgliteTx) Commit(c context.Context) error   { return t.finish(c, "COMMIT") }
func (t *pgliteTx) Rollback(c context.Context) error { return t.finish(c, "ROLLBACK") }
