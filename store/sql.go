package store

import (
	"context"
	"encoding/json"
	"fmt"
	"ponder.local/ponder-go/sqlquery"
	"strings"
)

type RawSQLError struct{ Cause error }

func (e *RawSQLError) Error() string { return e.Cause.Error() }
func (e *RawSQLError) Unwrap() error { return e.Cause }

// SQL executes one SQL statement inside the event transaction. A savepoint
// isolates recoverable statement errors, matching the original SQL API.
func (w *Writer) SQL(ctx context.Context, query string, args ...any) (Result, error) {
	if w.closed.Load() {
		return Result{}, &NonRetryableUserError{Message: "A store API method was called after the indexing function returned."}
	}
	tree, e := sqlquery.ParseStatement(query)
	if e != nil {
		return Result{}, &RawSQLError{e}
	}
	for kind := range tree {
		if kind != "SelectStmt" && kind != "InsertStmt" && kind != "UpdateStmt" && kind != "DeleteStmt" {
			return Result{}, &RawSQLError{fmt.Errorf("%s is unsupported in indexing SQL", kind)}
		}
	}
	if e = w.validateSQLScope(tree); e != nil {
		return Result{}, &RawSQLError{e}
	}
	if orm := w.indexingSession(); orm != nil {
		readonly := sqlquery.IsReadonly(query)
		if !readonly {
			if e = orm.Cache.Flush(ctx); e != nil {
				return Result{}, e
			}
			orm.Cache.Invalidate()
			orm.Cache.Clear()
		} else {
			relations, parseError := sqlquery.Relations(query)
			all := parseError != nil
			for _, view := range w.s.Schema.Views {
				for _, relation := range relations {
					if view.Name == relation {
						all = true
					}
				}
			}
			if all {
				e = orm.Cache.Flush(ctx)
			} else if len(relations) > 0 {
				e = orm.Cache.Flush(ctx, relations...)
			}
			if e != nil {
				return Result{}, e
			}
		}
	}
	if e = w.tx.Exec(ctx, "SAVEPOINT ponder_go_sql"); e != nil {
		w.err = e
		return Result{}, &RawSQLError{e}
	}
	result, e := w.tx.Query(ctx, query, args...)
	if e != nil {
		// PGlite marks a failed statement; recovering to a savepoint clears it.
		if rollback := w.tx.Exec(context.Background(), "ROLLBACK TO SAVEPOINT ponder_go_sql"); rollback != nil {
			w.err = rollback
			return Result{}, &RawSQLError{fmt.Errorf("%w; rollback failed: %v", e, rollback)}
		}
		if release := w.tx.Exec(context.Background(), "RELEASE SAVEPOINT ponder_go_sql"); release != nil {
			w.err = release
		}
		return Result{}, &RawSQLError{e}
	}
	if e = w.tx.Exec(ctx, "RELEASE SAVEPOINT ponder_go_sql"); e != nil {
		w.err = e
		return Result{}, &RawSQLError{e}
	}
	return result, nil
}
func (w *Writer) validateSQLScope(tree map[string]any) error {
	known := map[string]bool{}
	for _, t := range w.s.Schema.Tables {
		known[t.Name] = true
	}
	for _, v := range w.s.Schema.Views {
		known[v.Name] = true
	}
	var walk func(any) error
	walk = func(value any) error {
		switch n := value.(type) {
		case map[string]any:
			if _, ok := n["intoClause"]; ok {
				return fmt.Errorf("SELECT INTO creates an undeclared table and is unsupported in indexing SQL")
			}
			for _, kind := range []string{"InsertStmt", "UpdateStmt", "DeleteStmt"} {
				if statement, ok := n[kind].(map[string]any); ok {
					relation, _ := statement["relation"].(map[string]any)
					name, _ := relation["relname"].(string)
					if !known[name] {
						return fmt.Errorf("raw SQL writes require a declared onchain table or view: %s", name)
					}
				}
			}
			if name, ok := n["relname"].(string); ok {
				if schema, ok := n["schemaname"].(string); ok && schema != w.s.Namespace {
					return fmt.Errorf("SQL relation schema is outside the indexing schema")
				}
				if strings.HasPrefix(name, "_ponder_") {
					return fmt.Errorf("internal indexing tables are not exposed to raw SQL")
				}
			}
			if fn, ok := n["FuncCall"].(map[string]any); ok {
				raw, _ := json.Marshal(fn["funcname"])
				if strings.Contains(string(raw), `"set_config"`) {
					return fmt.Errorf("indexing context cannot be changed by raw SQL")
				}
			}
			for _, child := range n {
				if e := walk(child); e != nil {
					return e
				}
			}
		case []any:
			for _, child := range n {
				if e := walk(child); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return walk(tree)
}
