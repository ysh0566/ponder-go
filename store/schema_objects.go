package store

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/sqlquery"
	"strings"
)

func (s *Store) createSchemaObjects(ctx context.Context, tx Transaction) error {
	for _, enum := range s.Schema.Enums {
		found, e := tx.Query(ctx, "SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=$1 AND t.typname=$2", s.Namespace, enum.Name)
		if e != nil {
			return e
		}
		if len(found.Rows) > 0 {
			continue
		}
		values := []string{}
		for _, value := range enum.Values {
			values = append(values, "'"+strings.ReplaceAll(value, "'", "''")+"'")
		}
		if e = tx.Exec(ctx, "CREATE TYPE "+s.table(enum.Name)+" AS ENUM ("+strings.Join(values, ",")+")"); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) createViews(ctx context.Context, tx Transaction) error {
	if len(s.Schema.Views) == 0 {
		return nil
	}
	if e := tx.Exec(ctx, "SET LOCAL search_path TO "+schema.Quote(s.Namespace)+",pg_catalog"); e != nil {
		return e
	}
	pending := map[string]schema.View{}
	for _, v := range s.Schema.Views {
		pending[v.Name] = v
	}
	done := map[string]bool{}
	for len(pending) > 0 {
		progress := false
		for _, v := range s.Schema.Views {
			if _, ok := pending[v.Name]; !ok {
				continue
			}
			deps, e := sqlquery.Relations(v.Query)
			if e != nil {
				return e
			}
			ready := true
			for _, dep := range deps {
				if _, isView := pending[dep]; isView && !done[dep] {
					ready = false
				}
			}
			if !ready {
				continue
			}
			names := []string{}
			for _, f := range v.Fields {
				names = append(names, schema.Quote(f.Name))
			}
			if e = tx.Exec(ctx, "CREATE OR REPLACE VIEW "+s.table(v.Name)+" ("+strings.Join(names, ",")+") AS "+v.Query); e != nil {
				return e
			}
			delete(pending, v.Name)
			done[v.Name] = true
			progress = true
		}
		if !progress {
			return fmt.Errorf("cyclic view dependencies")
		}
	}
	return nil
}

// QueryDependencies resolves views to their base tables for SQL/SSE invalidation.
func (s *Store) QueryDependencies(relations []string) ([]string, error) {
	views := map[string]schema.View{}
	for _, v := range s.Schema.Views {
		views[v.Name] = v
	}
	tables := map[string]bool{}
	for _, t := range s.Schema.Tables {
		tables[t.Name] = true
	}
	out := []string{}
	seen := map[string]bool{}
	visiting := map[string]bool{}
	var resolve func(string) error
	resolve = func(name string) error {
		if seen[name] {
			return nil
		}
		if tables[name] {
			seen[name] = true
			out = append(out, name)
			return nil
		}
		v, ok := views[name]
		if !ok {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("cyclic view dependencies")
		}
		visiting[name] = true
		deps, e := sqlquery.Relations(v.Query)
		if e != nil {
			return e
		}
		for _, dep := range deps {
			if e = resolve(dep); e != nil {
				return e
			}
		}
		delete(visiting, name)
		seen[name] = true
		return nil
	}
	for _, name := range relations {
		if e := resolve(name); e != nil {
			return nil, e
		}
	}
	return out, nil
}
