// Package orm provides typed model operations over the journaled indexing store.
// Generated models keep keys, inserts and updates distinct; dynamic store.Row
// remains available for explicit runtime/error compatibility tests.
package orm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"ponder.local/ponder-go/store"
)

type Nullable[T any] struct {
	value T
	valid bool
}

func Some[T any](value T) Nullable[T] { return Nullable[T]{value, true} }
func Null[T any]() Nullable[T]        { return Nullable[T]{} }
func (v Nullable[T]) Valid() bool     { return v.valid }
func (v Nullable[T]) Value() T        { return v.value }

func (v Nullable[T]) MarshalJSON() ([]byte, error) {
	if !v.valid {
		return []byte("null"), nil
	}
	return json.Marshal(&v.value)
}
func (v *Nullable[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*v = Null[T]()
		return nil
	}
	var value T
	if e := json.Unmarshal(data, &value); e != nil {
		return e
	}
	*v = Some(value)
	return nil
}

type Change[T any] struct {
	value   T
	changed bool
}

func Set[T any](value T) Change[T] { return Change[T]{value, true} }
func (v Change[T]) Changed() bool  { return v.changed }
func (v Change[T]) Value() T       { return v.value }

type Encoder[T any] func(T) (store.Row, error)
type Decoder[T any] func(store.Row) (T, error)

// CopyValues protects generated immutable insert builders when adding optional
// fields. Encoders own these private rows; the store deep-copies native values.
func CopyValues(row store.Row) store.Row {
	out := store.Row{}
	for key, value := range row {
		out[key] = value
	}
	return out
}

type Definition[K, I, U, R any] struct {
	name   string
	key    Encoder[K]
	insert Encoder[I]
	update Encoder[U]
	decode Decoder[R]
}

func Define[K, I, U, R any](name string, key Encoder[K], insert Encoder[I], update Encoder[U], decode Decoder[R]) Definition[K, I, U, R] {
	return Definition[K, I, U, R]{name, key, insert, update, decode}
}

type Table[K, I, U, R any] struct {
	definition Definition[K, I, U, R]
	db         *store.IndexingStore
}

func (d Definition[K, I, U, R]) Bind(db *store.IndexingStore) Table[K, I, U, R] {
	return Table[K, I, U, R]{d, db}
}
func (d Definition[K, I, U, R]) Name() string { return d.name }
func (t Table[K, I, U, R]) Find(ctx context.Context, key K) (*R, error) {
	row, e := t.definition.key(key)
	if e != nil {
		return nil, e
	}
	result, e := t.db.Find(ctx, t.definition.name, row)
	if e != nil || result == nil {
		return nil, e
	}
	value, e := t.definition.decode(result)
	return &value, e
}
func (t Table[K, I, U, R]) Insert(ctx context.Context, input I) (R, error) {
	var zero R
	row, e := t.definition.insert(input)
	if e != nil {
		return zero, e
	}
	result, e := t.db.InsertOne(ctx, t.definition.name, row, store.Conflict{})
	if e != nil {
		return zero, e
	}
	return t.definition.decode(result)
}
func (t Table[K, I, U, R]) InsertMany(ctx context.Context, inputs []I) ([]R, error) {
	rows := make([]store.Row, len(inputs))
	for i, input := range inputs {
		row, e := t.definition.insert(input)
		if e != nil {
			return nil, e
		}
		rows[i] = row
	}
	result, e := t.db.InsertMany(ctx, t.definition.name, rows, store.Conflict{})
	if e != nil {
		return nil, e
	}
	values := make([]R, len(result))
	for i, row := range result {
		values[i], e = t.definition.decode(row)
		if e != nil {
			return nil, e
		}
	}
	return values, nil
}
func (t Table[K, I, U, R]) InsertOrIgnore(ctx context.Context, input I) (*R, error) {
	row, e := t.definition.insert(input)
	if e != nil {
		return nil, e
	}
	result, e := t.db.InsertOne(ctx, t.definition.name, row, store.Conflict{Action: store.ConflictNothing})
	if e != nil || result == nil {
		return nil, e
	}
	value, e := t.definition.decode(result)
	return &value, e
}
func (t Table[K, I, U, R]) Upsert(ctx context.Context, input I, set U) (R, error) {
	var zero R
	row, e := t.definition.insert(input)
	if e != nil {
		return zero, e
	}
	patch, e := t.definition.update(set)
	if e != nil {
		return zero, e
	}
	result, e := t.db.InsertOne(ctx, t.definition.name, row, store.Conflict{Action: store.ConflictUpdate, Set: patch})
	if e != nil {
		return zero, e
	}
	return t.definition.decode(result)
}
func (t Table[K, I, U, R]) Update(ctx context.Context, key K, set U) (R, error) {
	var zero R
	row, e := t.definition.key(key)
	if e != nil {
		return zero, e
	}
	patch, e := t.definition.update(set)
	if e != nil {
		return zero, e
	}
	result, e := t.db.Update(ctx, t.definition.name, row, patch)
	if e != nil {
		return zero, e
	}
	return t.definition.decode(result)
}
func (t Table[K, I, U, R]) UpdateFunc(ctx context.Context, key K, update func(R) (U, error)) (R, error) {
	var zero R
	row, e := t.definition.key(key)
	if e != nil {
		return zero, e
	}
	result, e := t.db.UpdateFunc(ctx, t.definition.name, row, func(row store.Row) (store.Row, error) {
		value, e := t.definition.decode(row)
		if e != nil {
			return nil, e
		}
		patch, e := update(value)
		if e != nil {
			return nil, e
		}
		return t.definition.update(patch)
	})
	if e != nil {
		return zero, e
	}
	return t.definition.decode(result)
}
func (t Table[K, I, U, R]) Delete(ctx context.Context, key K) (bool, error) {
	row, e := t.definition.key(key)
	if e != nil {
		return false, e
	}
	return t.db.Delete(ctx, t.definition.name, row)
}
func (t Table[K, I, U, R]) Select(ctx context.Context) ([]R, error) {
	rows, e := t.db.Select(ctx, t.definition.name, nil)
	if e != nil {
		return nil, e
	}
	result := make([]R, len(rows))
	for i, row := range rows {
		result[i], e = t.definition.decode(row)
		if e != nil {
			return nil, e
		}
	}
	return result, nil
}

func EncodeBigInt(value big.Int) any { return new(big.Int).Set(&value) }
func EncodeTime(value time.Time) any { return &value }
func DecodeBigInt(value any) (big.Int, error) {
	if n, ok := value.(*big.Int); ok && n != nil {
		return *new(big.Int).Set(n), nil
	}
	var zero big.Int
	return zero, fmt.Errorf("expected bigint, got %T", value)
}
func DecodeInt64(value any) (int64, error) {
	if value == nil {
		return 0, fmt.Errorf("expected integer, got NULL")
	}
	return strconv.ParseInt(fmt.Sprint(value), 10, 64)
}
func DecodeFloat64(value any) (float64, error) {
	if value == nil {
		return 0, fmt.Errorf("expected number, got NULL")
	}
	return strconv.ParseFloat(fmt.Sprint(value), 64)
}
func DecodeString(value any) (string, error) {
	v, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("expected string, got %T", value)
	}
	return v, nil
}
func DecodeBool(value any) (bool, error) {
	v, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("expected bool, got %T", value)
	}
	return v, nil
}
func DecodeTime(value any) (time.Time, error) {
	if v, ok := value.(*time.Time); ok && v != nil {
		return *v, nil
	}
	return time.Time{}, fmt.Errorf("expected timestamp, got %T", value)
}
