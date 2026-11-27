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

	"github.com/ysh0566/ponder-go/store"
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

// DecodeCopy materializes a cached row view using the same generated column
// types and decoders as Find. Mutations to the result are private to the caller.
func (d Definition[K, I, U, R]) DecodeCopy(view *store.RowCopy) (R, error) {
	if view == nil {
		var zero R
		return zero, fmt.Errorf("cannot decode a missing %s row", d.name)
	}
	return d.decode(view.Snapshot())
}

// Executor is the set of journaled operations used by generated models. A
// callback-bound executor keeps transaction admission and error classification.
type Executor interface {
	FindCopy(context.Context, string, store.Row) (*store.RowCopy, error)
	InsertOne(context.Context, string, store.Row, store.Conflict) (store.Row, error)
	InsertMany(context.Context, string, []store.Row, store.Conflict) ([]store.Row, error)
	Update(context.Context, string, store.Row, store.Row) (store.Row, error)
	UpdateFunc(context.Context, string, store.Row, store.RowUpdater) (store.Row, error)
	Delete(context.Context, string, store.Row) (bool, error)
	Select(context.Context, string, store.Row) ([]store.Row, error)
	SQLInsert(context.Context, string, store.Row) (store.Row, error)
}

type Table[K, I, U, R any] struct {
	definition Definition[K, I, U, R]
	db         Executor
}

// SQL exposes eager, typed database operations. Insert uses the exact same
// generated required-field/value types as the buffered operation.
type SQLTable[K, I, U, R any] struct{ table Table[K, I, U, R] }

func (t Table[K, I, U, R]) SQL() SQLTable[K, I, U, R] { return SQLTable[K, I, U, R]{t} }
func (s SQLTable[K, I, U, R]) Insert(ctx context.Context, input I) (R, error) {
	var zero R
	row, e := s.table.definition.insert(input)
	if e != nil {
		return zero, e
	}
	result, e := s.table.db.SQLInsert(ctx, s.table.definition.name, row)
	if e != nil {
		return zero, e
	}
	return s.table.definition.decode(result)
}
func (s SQLTable[K, I, U, R]) Find(ctx context.Context, key K) (*R, error) {
	row, e := s.table.definition.key(key)
	if e != nil {
		return nil, e
	}
	rows, e := s.table.db.Select(ctx, s.table.definition.name, row)
	if e != nil || len(rows) == 0 {
		return nil, e
	}
	value, e := s.table.definition.decode(rows[0])
	return &value, e
}

func (d Definition[K, I, U, R]) Bind(db *store.IndexingStore) Table[K, I, U, R] {
	return d.BindExecutor(db)
}

// BindExecutor binds generated operations to the current callback database.
func (d Definition[K, I, U, R]) BindExecutor(db Executor) Table[K, I, U, R] {
	return Table[K, I, U, R]{d, db}
}
func (d Definition[K, I, U, R]) Name() string { return d.name }
func (t Table[K, I, U, R]) Find(ctx context.Context, key K) (*R, error) {
	row, e := t.definition.key(key)
	if e != nil {
		return nil, e
	}
	result, e := t.db.FindCopy(ctx, t.definition.name, row)
	if e != nil || result == nil {
		return nil, e
	}
	value, e := t.definition.DecodeCopy(result)
	return &value, e
}
func (t Table[K, I, U, R]) Insert(ctx context.Context, input I) (R, error) {
	return t.insertOne(ctx, input, store.Conflict{})
}

func (t Table[K, I, U, R]) insertOne(ctx context.Context, input I, conflict store.Conflict) (R, error) {
	var zero R
	row, e := t.definition.insert(input)
	if e != nil {
		return zero, e
	}
	result, e := t.db.InsertOne(ctx, t.definition.name, row, conflict)
	if e != nil {
		return zero, e
	}
	return t.definition.decode(result)
}
func (t Table[K, I, U, R]) insertMany(ctx context.Context, inputs []I, conflict store.Conflict) ([]store.Row, error) {
	rows := make([]store.Row, len(inputs))
	for i, input := range inputs {
		row, e := t.definition.insert(input)
		if e != nil {
			return nil, e
		}
		rows[i] = row
	}
	return t.db.InsertMany(ctx, t.definition.name, rows, conflict)
}

func (t Table[K, I, U, R]) InsertMany(ctx context.Context, inputs []I) ([]R, error) {
	result, e := t.insertMany(ctx, inputs, store.Conflict{})
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

// InsertManyOrIgnore returns a result for each input in order. A nil entry
// indicates a conflict, including a duplicate earlier in the same batch.
func (t Table[K, I, U, R]) InsertManyOrIgnore(ctx context.Context, inputs []I) ([]*R, error) {
	rows, err := t.insertMany(ctx, inputs, store.Conflict{Action: store.ConflictNothing})
	if err != nil {
		return nil, err
	}
	result := make([]*R, len(rows))
	for i, row := range rows {
		if row == nil {
			continue
		}
		value, err := t.definition.decode(row)
		if err != nil {
			return nil, err
		}
		result[i] = &value
	}
	return result, nil
}

// UpsertFunc calls update with the existing typed row when a key conflicts.
func (t Table[K, I, U, R]) UpsertFunc(ctx context.Context, input I, update func(R) (U, error)) (R, error) {
	if update == nil {
		var zero R
		return zero, fmt.Errorf("upsert requires an update function")
	}
	return t.insertOne(ctx, input, store.Conflict{Action: store.ConflictUpdate, Update: t.definition.rowUpdater(update)})
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
	result, e := t.db.UpdateFunc(ctx, t.definition.name, row, t.definition.rowUpdater(update))
	if e != nil {
		return zero, e
	}
	return t.definition.decode(result)
}
func (d Definition[K, I, U, R]) rowUpdater(update func(R) (U, error)) store.RowUpdater {
	return func(row store.Row) (store.Row, error) {
		if update == nil {
			return nil, fmt.Errorf("update requires a function")
		}
		value, err := d.decode(row)
		if err != nil {
			return nil, err
		}
		patch, err := update(value)
		if err != nil {
			return nil, err
		}
		return d.update(patch)
	}
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
	return decodeRows(t.definition.decode, rows)
}

func decodeRows[R, V any](decode func(V) (R, error), rows []V) ([]R, error) {
	result := make([]R, len(rows))
	for i, row := range rows {
		var e error
		result[i], e = decode(row)
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
