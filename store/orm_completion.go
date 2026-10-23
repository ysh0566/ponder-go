package store

import (
	"context"
	"fmt"
	"strings"

	"ponder.local/ponder-go/schema"
)

// InsertThen and InsertManyThen are native, synchronous success continuations.
// They preserve one result/callback per operation; they do not implement JS
// Promise scheduling, rejection assimilation or arbitrary thenable interop.
func (db *IndexingStore) InsertThen(ctx context.Context, table string, row Row, then func(Row) error) error {
	result, e := db.InsertOne(ctx, table, row, Conflict{})
	if e != nil {
		return e
	}
	return then(result)
}
func (db *IndexingStore) InsertManyThen(ctx context.Context, table string, rows []Row, then func([]Row) error) error {
	result, e := db.InsertMany(ctx, table, rows, Conflict{})
	if e != nil {
		return e
	}
	return then(result)
}

// SQLInsert is an eager INSERT over the same guarded SQL/savepoint/cache
// boundary as SQL. Unlike buffered InsertOne, constraint failures come from
// PostgreSQL and retain RawSQLError. Typed clients use their generated I type;
// dynamic calls may deliberately submit NULL to test database errors.
func (db *IndexingStore) SQLInsert(ctx context.Context, name string, row Row) (Row, error) {
	if e := db.active(); e != nil {
		return nil, e
	}
	table, e := db.writer.s.model(name)
	if e != nil {
		return nil, e
	}
	known := map[string]bool{}
	cols, values := []string{}, []string{}
	args := []any{}
	for _, column := range table.Columns {
		key := property(column)
		known[key] = true
		value, present := row[key]
		if !present {
			continue
		}
		if _, undefined := value.(undefinedValue); undefined {
			value = nil
		}
		value, e = NormalizeColumn(column, value, false)
		if e != nil {
			return nil, e
		}
		parameter, e := db.writer.s.columnParameter(column, value, &args)
		if e != nil {
			return nil, e
		}
		cols, values = append(cols, schema.Quote(column.Name)), append(values, parameter)
	}
	for key := range row {
		if !known[key] {
			return nil, fmt.Errorf("unknown column %s.%s", name, key)
		}
	}
	query := "INSERT INTO " + db.writer.s.table(name) + " AS t"
	if len(cols) == 0 {
		query += " DEFAULT VALUES"
	} else {
		query += " (" + strings.Join(cols, ",") + ") VALUES (" + strings.Join(values, ",") + ")"
	}
	result, e := db.SQL(ctx, query+" RETURNING row_to_json(t)::text AS row", args...)
	if e != nil {
		return nil, e
	}
	if len(result.Rows) != 1 {
		return nil, fmt.Errorf("SQL insert returned %d rows", len(result.Rows))
	}
	physical, e := decodeSnapshot(table, fmt.Sprint(result.Rows[0]["row"]))
	if e != nil {
		return nil, e
	}
	logical := Row{}
	for _, column := range table.Columns {
		logical[property(column)] = physical[column.Name]
	}
	return logical, nil
}
