package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"ponder.local/ponder-go/schema"
)

// Undo preserves database JSON numbers and escapes as raw JSON. User-facing
// column decoding follows JS number semantics, which must not rewrite a raw SQL
// value while restoring the durable before-image.
func decodeUndoColumn(column schema.Column, raw json.RawMessage) (any, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	if column.Type == "json" || column.Type == "jsonb" {
		return append(json.RawMessage{}, raw...), nil
	}
	if strings.HasSuffix(column.Type, "[]") {
		var values []json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return nil, e
		}
		base := column
		base.Type = strings.TrimSuffix(column.Type, "[]")
		out := make([]any, len(values))
		for i, value := range values {
			var e error
			out[i], e = decodeUndoColumn(base, value)
			if e != nil {
				return nil, e
			}
		}
		return out, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e := decoder.Decode(&value); e != nil {
		return nil, e
	}
	return decodeColumn(column, value)
}

func decodeUndoRow(table schema.Table, data string) (Row, error) {
	var raw map[string]json.RawMessage
	if e := json.Unmarshal([]byte(data), &raw); e != nil {
		return nil, e
	}
	row := Row{}
	for _, column := range table.Columns {
		value, e := decodeUndoColumn(column, raw[column.Name])
		if e != nil {
			return nil, e
		}
		row[property(column)] = value
	}
	return row, nil
}

// Bind each column separately. PostgreSQL accepts U+0000 in json, but all
// json[b]_populate_record helpers decode the entire row and reject that value.
// Text-format parameters keep integer precision and work with both drivers.
func (s *Store) columnParameter(column schema.Column, value any, args *[]any) (string, error) {
	typ, e := schema.SQLType(column.Type)
	if e != nil {
		base := strings.TrimSuffix(column.Type, "[]")
		found := false
		for _, enum := range s.Schema.Enums {
			if enum.Name == base {
				typ = schema.Quote(s.Namespace) + "." + schema.Quote(base)
				if strings.HasSuffix(column.Type, "[]") {
					typ += "[]"
				}
				found = true
				break
			}
		}
		if !found {
			return "", e
		}
	}
	if value != nil && strings.HasSuffix(column.Type, "[]") {
		array := reflect.ValueOf(value)
		if array.Kind() != reflect.Array && array.Kind() != reflect.Slice {
			return "", fmt.Errorf("%s requires array", column.Name)
		}
		base := column
		base.Type = strings.TrimSuffix(column.Type, "[]")
		parts := make([]string, array.Len())
		for i := range parts {
			parts[i], e = s.columnParameter(base, array.Index(i).Interface(), args)
			if e != nil {
				return "", e
			}
		}
		return "ARRAY[" + strings.Join(parts, ",") + "]::" + typ, nil
	}
	var parameter any
	if value != nil {
		mapped, e := driverJSON(column, value)
		if e != nil {
			return "", e
		}
		if mapped != nil {
			if column.Type == "json" || column.Type == "jsonb" {
				raw, e := json.Marshal(mapped)
				if e != nil {
					return "", e
				}
				parameter = string(raw)
			} else {
				parameter = fmt.Sprint(mapped)
			}
		}
	}
	*args = append(*args, parameter)
	return fmt.Sprintf("($%d::text)::%s", len(*args), typ), nil
}

func (s *Store) writeRows(ctx context.Context, tx Transaction, table schema.Table, rows []Row, mode WriteMode) error {
	columns, keys, updates := []string{}, []string{}, []string{}
	for _, column := range table.Columns {
		quoted := schema.Quote(column.Name)
		columns = append(columns, quoted)
		updates = append(updates, quoted+"=excluded."+quoted)
	}
	for _, key := range table.Keys() {
		keys = append(keys, schema.Quote(key))
	}
	for len(rows) > 0 {
		args := []any{}
		values := []string{}
		consumed := 0
		for _, row := range rows {
			start := len(args)
			parts := make([]string, len(table.Columns))
			for i, column := range table.Columns {
				var e error
				parts[i], e = s.columnParameter(column, row[property(column)], &args)
				if e != nil {
					return e
				}
			}
			if len(args) > 60000 {
				if consumed == 0 {
					return fmt.Errorf("one row exceeds the native parameter batch limit")
				}
				args = args[:start]
				break
			}
			values = append(values, "("+strings.Join(parts, ",")+")")
			consumed++
			if len(values) >= 1000 {
				break
			}
		}
		query := "INSERT INTO " + s.table(table.Name) + " (" + strings.Join(columns, ",") + ") VALUES " + strings.Join(values, ",")
		if mode == Upsert {
			query += " ON CONFLICT (" + strings.Join(keys, ",") + ") DO UPDATE SET " + strings.Join(updates, ",")
		}
		if e := tx.Exec(ctx, query, args...); e != nil {
			return e
		}
		rows = rows[consumed:]
	}
	return nil
}
