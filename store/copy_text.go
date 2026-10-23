package store

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"ponder.local/ponder-go/schema"
	"reflect"
	"strings"
	"time"
)

func pgArrayText(value any) (string, error) {
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return "", fmt.Errorf("expected PostgreSQL array")
	}
	items := make([]string, v.Len())
	for i := range items {
		element := v.Index(i).Interface()
		if element == nil {
			items[i] = "NULL"
			continue
		}
		if text, ok := element.(string); ok {
			items[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text) + `"`
		} else {
			items[i] = fmt.Sprint(element)
		}
	}
	return "{" + strings.Join(items, ",") + "}", nil
}

// GetCopyText preserves the pinned COPY-text byte and control-character format.
// The native flush path uses PostgreSQL JSON recordsets; this utility remains a
// separately tested format adapter rather than substituting for a database test.
func GetCopyText(table schema.Table, rows []Row) (string, error) {
	lines := make([]string, len(rows))
	for i, row := range rows {
		fields := make([]string, len(table.Columns))
		for j, column := range table.Columns {
			value := row[property(column)]
			if value == nil || isUndefined(value) {
				fields[j] = `\N`
				continue
			}
			if column.MapToDriverValue != nil {
				var e error
				value, e = column.MapToDriverValue(value)
				if e != nil {
					return "", e
				}
			}
			if value == nil {
				fields[j] = `\N`
				continue
			}
			if data, ok := value.([]byte); ok {
				fields[j] = `\\x` + hex.EncodeToString(data)
				continue
			}
			text := fmt.Sprint(value)
			if strings.HasSuffix(column.Type, "[]") {
				var e error
				text, e = pgArrayText(value)
				if e != nil {
					return "", e
				}
			}
			if column.Type == "json" || column.Type == "jsonb" {
				if containsBigInt(value, map[uintptr]bool{}) {
					return "", &BigIntSerializationError{NonRetryableUserError{"Do not know how to serialize a BigInt"}}
				}
				raw, e := json.Marshal(value)
				if e != nil {
					return "", e
				}
				text = string(raw)
			}
			if column.Type == "timestamp" {
				var date time.Time
				switch value := value.(type) {
				case time.Time:
					date = value
				case *time.Time:
					date = *value
				default:
					return "", fmt.Errorf("timestamp requires time.Time")
				}
				text = date.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			var escaped strings.Builder
			for _, ch := range text {
				if strings.ContainsRune("\\\b\f\n\r\t\v", ch) {
					escaped.WriteByte('\\')
				}
				escaped.WriteRune(ch)
			}
			fields[j] = escaped.String()
		}
		lines[i] = strings.Join(fields, "\t")
	}
	return strings.Join(lines, "\n"), nil
}
