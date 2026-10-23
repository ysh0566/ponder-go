package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/schema"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func containsBigInt(value any, seen map[uintptr]bool) bool {
	if value == nil {
		return false
	}
	if _, ok := value.(*big.Int); ok {
		return true
	}
	if _, ok := value.(big.Int); ok {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return false
		}
		ptr := uintptr(v.UnsafePointer())
		if seen[ptr] {
			return false
		}
		seen[ptr] = true
		defer delete(seen, ptr)
		for _, key := range v.MapKeys() {
			if containsBigInt(v.MapIndex(key).Interface(), seen) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				return false
			}
			ptr := v.Pointer()
			if seen[ptr] {
				return false
			}
			seen[ptr] = true
			defer delete(seen, ptr)
		}
		for i := 0; i < v.Len(); i++ {
			if containsBigInt(v.Index(i).Interface(), seen) {
				return true
			}
		}
	}
	return false
}

// NormalizeColumn mirrors the pinned column mapper roundtrip. Undefined invokes
// defaults; explicit nil remains SQL NULL. JSON deliberately rejects big.Int.
func NormalizeColumn(column schema.Column, value any, isUpdate bool) (any, error) {
	if isUndefined(value) {
		if isUpdate && column.OnUpdateFn != nil {
			return cloneValue(column.OnUpdateFn()), nil
		}
		if column.HasDefault || column.Default != nil {
			return cloneValue(column.Default), nil
		}
		if column.DefaultFn != nil {
			return cloneValue(column.DefaultFn()), nil
		}
		if column.OnUpdateFn != nil {
			return cloneValue(column.OnUpdateFn()), nil
		}
		return nil, nil
	}
	if value == nil {
		return nil, nil
	}
	if column.MapToDriverValue != nil || column.MapFromDriverValue != nil {
		mapped := value
		var e error
		if column.MapToDriverValue != nil {
			mapped, e = column.MapToDriverValue(mapped)
			if e != nil {
				return nil, e
			}
		}
		if column.MapFromDriverValue != nil {
			mapped, e = column.MapFromDriverValue(mapped)
			if e != nil {
				return nil, e
			}
		}
		return cloneValue(mapped), nil
	}
	v := reflect.ValueOf(value)
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Slice || v.Kind() == reflect.Map) && v.IsNil() {
		return nil, nil
	}
	if strings.HasSuffix(column.Type, "[]") {
		if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
			return nil, fmt.Errorf("%s requires an array", column.Name)
		}
		base := column
		base.Type = strings.TrimSuffix(base.Type, "[]")
		values := make([]any, v.Len())
		for i := range values {
			next, e := NormalizeColumn(base, v.Index(i).Interface(), isUpdate)
			if e != nil {
				return nil, e
			}
			values[i] = next
		}
		return typedArray(base, values), nil
	}
	switch column.Type {
	case "point", "line":
		return geometryValues(column, value)
	case "real", "doublePrecision":
		return strconv.ParseFloat(fmt.Sprint(value), 64)
	case "hex", "address":
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s requires hex text", column.Name)
		}
		if len(s)%2 != 0 && strings.HasPrefix(s, "0x") {
			s = "0x0" + s[2:]
		}
		return strings.ToLower(s), nil
	case "text":
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s requires text", column.Name)
		}
		return strings.ReplaceAll(s, "\x00", ""), nil
	case "json", "jsonb":
		if containsBigInt(value, map[uintptr]bool{}) {
			return nil, &BigIntSerializationError{NonRetryableUserError{"Do not know how to serialize a BigInt"}}
		}
		raw, e := json.Marshal(value)
		if e != nil {
			return nil, e
		}
		var normalized any
		e = json.Unmarshal(raw, &normalized)
		return normalized, e
	case "bigint":
		n, ok := new(big.Int).SetString(fmt.Sprint(value), 10)
		if !ok {
			return nil, fmt.Errorf("invalid bigint value for %s", column.Name)
		}
		return n, nil
	case "int8":
		if column.Mode == "bigint" {
			n, ok := new(big.Int).SetString(fmt.Sprint(value), 10)
			if !ok {
				return nil, fmt.Errorf("invalid int8")
			}
			return n, nil
		}
		fallthrough
	case "integer", "smallint":
		n, e := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return n, e
	case "timestamp":
		var timestamp time.Time
		switch value := value.(type) {
		case time.Time:
			timestamp = value
		case *time.Time:
			timestamp = *value
		default:
			return nil, fmt.Errorf("%s requires time.Time", column.Name)
		}
		timestamp = timestamp.UTC()
		return &timestamp, nil
	case "bytes":
		data, ok := value.([]byte)
		if !ok {
			return nil, fmt.Errorf("%s requires []byte", column.Name)
		}
		return append([]byte{}, data...), nil
	}
	return cloneValue(value), nil
}

func typedArray(base schema.Column, values []any) any {
	for _, v := range values {
		if v == nil {
			return values
		}
	}
	var typ reflect.Type
	switch base.Type {
	case "integer", "smallint":
		typ = reflect.TypeOf(int64(0))
	case "int8":
		if base.Mode == "bigint" {
			typ = reflect.TypeOf((*big.Int)(nil))
		} else {
			typ = reflect.TypeOf(int64(0))
		}
	case "bigint":
		typ = reflect.TypeOf((*big.Int)(nil))
	case "text", "hex", "address", "varchar", "char", "numeric":
		typ = reflect.TypeOf("")
	case "boolean":
		typ = reflect.TypeOf(false)
	case "timestamp":
		typ = reflect.TypeOf((*time.Time)(nil))
	}
	if typ == nil {
		return values
	}
	out := reflect.MakeSlice(reflect.SliceOf(typ), len(values), len(values))
	for i, value := range values {
		rv := reflect.ValueOf(value)
		if !rv.Type().AssignableTo(typ) {
			return values
		}
		out.Index(i).Set(rv)
	}
	return out.Interface()
}

func NormalizeUpdateSet(table schema.Table, values Row) (Row, error) {
	out := Row{}
	for name, value := range values {
		out[name] = value
		if isUndefined(value) {
			continue
		}
		for _, column := range table.Columns {
			if property(column) == name {
				normalized, e := NormalizeColumn(column, value, true)
				if e != nil {
					return nil, e
				}
				out[name] = normalized
				break
			}
		}
	}
	return out, nil
}

func driverJSON(column schema.Column, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	if column.MapToDriverValue != nil {
		mapped, e := column.MapToDriverValue(value)
		if e != nil {
			return nil, e
		}
		switch mapped := mapped.(type) {
		case *big.Int:
			if mapped == nil {
				return nil, nil
			}
			return json.Number(mapped.String()), nil
		case []byte:
			return "\\x" + hex.EncodeToString(mapped), nil
		}
		if column.Type == "json" || column.Type == "jsonb" {
			if text, ok := mapped.(string); ok {
				var value any
				decoder := json.NewDecoder(strings.NewReader(text))
				decoder.UseNumber()
				if e := decoder.Decode(&value); e != nil {
					return nil, e
				}
				return value, nil
			}
		}
		return mapped, nil
	}
	if strings.HasSuffix(column.Type, "[]") {
		base := column
		base.Type = strings.TrimSuffix(column.Type, "[]")
		v := reflect.ValueOf(value)
		out := make([]any, v.Len())
		for i := range out {
			next, e := driverJSON(base, v.Index(i).Interface())
			if e != nil {
				return nil, e
			}
			out[i] = next
		}
		return out, nil
	}
	switch column.Type {
	case "point", "line":
		values, e := geometryValues(column, value)
		if e != nil {
			return nil, e
		}
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = strconv.FormatFloat(value, 'g', -1, 64)
		}
		if column.Type == "point" {
			return "(" + strings.Join(parts, ",") + ")", nil
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	case "bigint":
		return json.Number(value.(*big.Int).String()), nil
	case "int8":
		if column.Mode == "bigint" {
			return json.Number(value.(*big.Int).String()), nil
		}
	case "bytes":
		return "\\x" + hex.EncodeToString(value.([]byte)), nil
	case "timestamp":
		return value.(*time.Time).UTC().Format(time.RFC3339Nano), nil
	}
	return value, nil
}

func decodeColumn(column schema.Column, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	if column.MapFromDriverValue != nil {
		return column.MapFromDriverValue(value)
	}
	column.MapToDriverValue = nil
	if strings.HasSuffix(column.Type, "[]") {
		base := column
		base.Type = strings.TrimSuffix(column.Type, "[]")
		values, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("stored array is invalid")
		}
		out := make([]any, len(values))
		for i, v := range values {
			next, e := decodeColumn(base, v)
			if e != nil {
				return nil, e
			}
			out[i] = next
		}
		return typedArray(base, out), nil
	}
	switch column.Type {
	case "hex", "address", "text", "varchar", "char":
		return value, nil
	case "point", "line":
		text := strings.Trim(fmt.Sprint(value), "(){}")
		parts := strings.Split(text, ",")
		values := make([]float64, len(parts))
		for i, part := range parts {
			v, e := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if e != nil {
				return nil, e
			}
			values[i] = v
		}
		return geometryValues(column, values)
	case "bytes":
		return hex.DecodeString(strings.TrimPrefix(fmt.Sprint(value), "\\x"))
	case "timestamp":
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
			if v, e := time.Parse(layout, fmt.Sprint(value)); e == nil {
				return &v, nil
			}
		}
		return nil, fmt.Errorf("stored timestamp is invalid: %v", value)
	case "numeric":
		return fmt.Sprint(value), nil
	}
	return NormalizeColumn(column, value, false)
}

func geometryValues(column schema.Column, value any) ([]float64, error) {
	v := reflect.ValueOf(value)
	size := 2
	if column.Type == "line" {
		size = 3
	}
	if (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) || v.Len() != size {
		return nil, fmt.Errorf("%s requires %d coordinates", column.Type, size)
	}
	values := make([]float64, size)
	for i := range values {
		number, e := strconv.ParseFloat(fmt.Sprint(v.Index(i).Interface()), 64)
		if e != nil {
			return nil, e
		}
		values[i] = number
	}
	return values, nil
}

func encodeRows(table schema.Table, rows []Row) (string, error) {
	physical := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		encoded := map[string]any{}
		for _, column := range table.Columns {
			value, e := driverJSON(column, row[property(column)])
			if e != nil {
				return "", e
			}
			encoded[column.Name] = value
		}
		physical = append(physical, encoded)
	}
	raw, e := json.Marshal(physical)
	return string(raw), e
}

func decodeSnapshot(table schema.Table, data string) (Row, error) {
	var row Row
	decoder := json.NewDecoder(bytes.NewBufferString(data))
	decoder.UseNumber()
	if e := decoder.Decode(&row); e != nil {
		return nil, e
	}
	for _, column := range table.Columns {
		value, e := decodeColumn(column, row[column.Name])
		if e != nil {
			return nil, e
		}
		row[column.Name] = value
	}
	return row, nil
}
