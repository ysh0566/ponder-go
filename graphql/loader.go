package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/ysh0566/ponder-go/schema"
)

type loaderKey struct{}
type requestLoaders struct {
	mu     sync.Mutex
	groups map[string]*relationBatch
}
type relationBatch struct {
	pending []*oneResult
	cached  map[string]*oneResult
}
type oneResult struct {
	values []any
	row    map[string]any
	err    error
	ready  bool
}

// graphql-go gathers sibling fields before evaluating their deferred results.
// The first deferred field reads the pending keys together, within one request.
func (s *Server) loadOne(ctx context.Context, table schema.Table, cols []schema.Column, values []any) (any, error) {
	for _, v := range values {
		if v == nil {
			return nil, nil
		}
	}
	keys := make([]string, len(cols))
	for i, c := range cols {
		keys[i] = c.Name
	}
	group := table.Name + ":" + strings.Join(keys, ",")
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	loaders := ctx.Value(loaderKey{}).(*requestLoaders)
	loaders.mu.Lock()
	batch := loaders.groups[group]
	if batch == nil {
		batch = &relationBatch{cached: map[string]*oneResult{}}
		loaders.groups[group] = batch
	}
	result := batch.cached[string(data)]
	if result == nil {
		result = &oneResult{values: values}
		batch.cached[string(data)] = result
		batch.pending = append(batch.pending, result)
	}
	loaders.mu.Unlock()
	return func() (any, error) {
		loaders.mu.Lock()
		defer loaders.mu.Unlock()
		for !result.ready {
			n := min(1000, len(batch.pending))
			entries := batch.pending[:n]
			batch.pending = batch.pending[n:]
			err := s.readKeys(ctx, table, cols, entries)
			for _, entry := range entries {
				entry.ready = true
				entry.err = err
			}
		}
		if result.err != nil || result.row == nil {
			return nil, result.err
		}
		return result.row, nil
	}, nil
}

func (s *Server) readKeys(ctx context.Context, table schema.Table, columns []schema.Column, entries []*oneResult) error {
	params := queryParams{model: s.database.Schema}
	aliases := []string{"ord"}
	predicates := make([]string, len(columns))
	for i, c := range columns {
		alias := fmt.Sprintf("k%d", i)
		aliases = append(aliases, alias)
		predicates[i] = "t." + schema.Quote(c.Name) + "=k." + alias
	}
	tuples := make([]string, len(entries))
	for i, entry := range entries {
		values := []string{strconv.Itoa(i)}
		for j, c := range columns {
			v, err := params.value(c, entry.values[j])
			if err != nil {
				return err
			}
			values = append(values, v)
		}
		tuples[i] = "(" + strings.Join(values, ",") + ")"
	}
	// Let the database compare typed keys. The ordinal keeps distinct inputs
	// aligned even when the database normalizes a decimal or hexadecimal value.
	stmt := "SELECT k.ord,r.payload FROM (VALUES " + strings.Join(tuples, ",") + ") AS k(" + strings.Join(aliases, ",") + ") CROSS JOIN LATERAL (SELECT row_to_json(t)::text AS payload FROM " + schema.Quote(s.namespace) + "." + schema.Quote(table.Name) + " t WHERE " + strings.Join(predicates, " AND ") + " LIMIT 1) r"
	rows, err := s.database.QueryReadOnlyInNamespace(ctx, s.namespace, stmt, params.args...)
	if err != nil {
		return err
	}
	for _, row := range rows.Rows {
		i, err := strconv.Atoi(fmt.Sprint(row["ord"]))
		if err != nil || i < 0 || i >= len(entries) {
			return fmt.Errorf("invalid relation row ordinal %v", row["ord"])
		}
		dec := json.NewDecoder(strings.NewReader(fmt.Sprint(row["payload"])))
		dec.UseNumber()
		if err := dec.Decode(&entries[i].row); err != nil {
			return err
		}
	}
	return nil
}
