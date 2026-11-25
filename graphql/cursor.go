package graphql

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ponder.local/ponder-go/schema"
)

// Every sort uses the primary key as a tiebreaker, including composite keys.
func orderColumns(table schema.Table, order string) ([]schema.Column, error) {
	keys := table.Keys()
	if order != "" {
		found := false
		for _, col := range table.Columns {
			if property(col) == order {
				keys = append([]string{col.Name}, keys...)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("Unknown column %q used in orderBy argument", order)
		}
	}
	out := make([]schema.Column, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		for _, col := range table.Columns {
			if col.Name == key {
				out = append(out, col)
				break
			}
		}
	}
	return out, nil
}

// superjson preserves insertion order and annotates Date and bigint values.
// Keep the ordering columns in declaration order in the base64 payload.
func encodeCursor(order []schema.Column, row map[string]any) (string, error) {
	var fields, annotations []string
	for _, col := range order {
		value := row[col.Name]
		annotation := ""
		if value != nil {
			switch {
			case col.Type == "bigint" || col.Type == "int8" && col.Mode != "number":
				annotation = "bigint"
				value = fmt.Sprint(value)
			case col.Type == "numeric":
				value = fmt.Sprint(value)
			case col.Type == "date" && col.Mode == "date":
				day, err := time.Parse(time.DateOnly, fmt.Sprint(value))
				if err != nil {
					return "", err
				}
				annotation = "Date"
				value = day.UTC().Format("2006-01-02T15:04:05.000Z")
			}
		}
		key, _ := json.Marshal(property(col))
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return "", err
		}
		fields = append(fields, string(key)+":"+strings.TrimSuffix(buf.String(), "\n"))
		if annotation != "" {
			annotations = append(annotations, string(key)+":["+fmt.Sprintf("%q", annotation)+"]")
		}
	}
	payload := "{\"json\":{" + strings.Join(fields, ",") + "}"
	if len(annotations) > 0 {
		payload += ",\"meta\":{\"values\":{" + strings.Join(annotations, ",") + "}}"
	}
	return base64.StdEncoding.EncodeToString([]byte(payload + "}")), nil
}

func (q *queryParams) cursorCondition(order []schema.Column, cursor string, descending, before bool) (string, error) {
	data, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return "", fmt.Errorf("invalid page cursor: %w", err)
	}
	var payload struct{ JSON map[string]any }
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return "", fmt.Errorf("invalid page cursor: %w", err)
	}
	names := make([]string, len(order))
	values := make([]string, len(order))
	for i, col := range order {
		v, ok := payload.JSON[property(col)]
		if !ok {
			return "", fmt.Errorf("cursor lacks sort column %q", property(col))
		}
		names[i] = schema.Quote(col.Name)
		values[i], err = q.value(col, v)
		if err != nil {
			return "", err
		}
	}
	op := " >= "
	if descending != before {
		op = " <= "
	}
	return "(" + strings.Join(names, ",") + ")" + op + "(" + strings.Join(values, ",") + ")", nil
}
