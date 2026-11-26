package orm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// CloneBytes retains a named byte slice's marshaling behavior and copies its data.
func CloneBytes[T ~[]byte](v T) T { return T(bytes.Clone(v)) }

func DecodeBytes(value any) ([]byte, error) {
	data, ok := value.([]byte)
	if !ok {
		return nil, fmt.Errorf("expected bytes, got %T", value)
	}
	return bytes.Clone(data), nil
}

func DecodeJSON(value any) (json.RawMessage, error) {
	return json.Marshal(value)
}

// PostgreSQL arrays may contain SQL NULL, including when the column is NOT NULL.
func EncodeArray[T any](values []Nullable[T], encode func(T) any) []any {
	if values == nil {
		return nil
	}
	out := make([]any, len(values))
	for i, v := range values {
		if v.Valid() {
			out[i] = encode(v.Value())
		}
	}
	return out
}

func ArrayDecoder[T any](element func(any) (T, error)) func(any) ([]Nullable[T], error) {
	return func(value any) ([]Nullable[T], error) {
		list := reflect.ValueOf(value)
		if !list.IsValid() || (list.Kind() != reflect.Slice && list.Kind() != reflect.Array) {
			return nil, fmt.Errorf("expected an array, got %T", value)
		}
		out := make([]Nullable[T], list.Len())
		for i := range out {
			v := list.Index(i).Interface()
			if v == nil {
				continue
			}
			decoded, err := element(v)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			out[i] = Some(decoded)
		}
		return out, nil
	}
}

func DecodePoint(value any) ([2]float64, error) {
	var out [2]float64
	err := decodeCoordinates(value, out[:])
	return out, err
}
func DecodeLine(value any) ([3]float64, error) {
	var out [3]float64
	err := decodeCoordinates(value, out[:])
	return out, err
}
func decodeCoordinates(value any, out []float64) error {
	coords, ok := value.([]float64)
	if !ok || len(coords) != len(out) {
		return fmt.Errorf("expected %d coordinates, got %v", len(out), value)
	}
	copy(out, coords)
	return nil
}

// EnumDecoder checks database labels against a generated enum declaration.
func EnumDecoder[T any](parse func(string) (T, error)) func(any) (T, error) {
	return func(value any) (T, error) {
		label, err := DecodeString(value)
		if err != nil {
			var zero T
			return zero, err
		}
		return parse(label)
	}
}
