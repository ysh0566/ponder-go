package orm

import (
	"context"
	"fmt"

	"github.com/ysh0566/ponder-go/store"
)

// Column identifies a generated model column. V is its value type before NULL
// wrapping, so a nullable foreign key can be compared with a required key.
type Column[R, V any] struct {
	table, name string
	decode      Decoder[R]
	encode      func(V) any
}

// ColumnOf binds a physical column name to a model for generated code.
func ColumnOf[V, K, I, U, R any](d Definition[K, I, U, R], name string) Column[R, V] {
	return Column[R, V]{table: d.name, name: name, decode: d.decode}
}

type JoinCondition[L, R any] struct {
	left, right string
	columns     store.JoinColumns
	decodeLeft  Decoder[L]
	decodeRight Decoder[R]
}

// Equal compares generated columns with the same Go value type.
func Equal[L, R, V any](left Column[L, V], right Column[R, V]) JoinCondition[L, R] {
	return JoinCondition[L, R]{left: left.table, right: right.table,
		columns:    store.JoinColumns{Left: left.name, Right: right.name},
		decodeLeft: left.decode, decodeRight: right.decode}
}

// JoinExecutor is provided by the atomic and independent-storage callback databases.
type JoinExecutor interface {
	Join(context.Context, store.JoinKind, string, string, []store.JoinColumns) ([]store.JoinedRow, error)
}

// Join lets generated callback databases supply their bound left table as the
// executor. Existing custom CRUD executors can optionally implement JoinExecutor.
func (t Table[K, I, U, R]) Join(ctx context.Context, kind store.JoinKind, left, right string, on []store.JoinColumns) ([]store.JoinedRow, error) {
	if t.definition.name != left {
		return nil, fmt.Errorf("join uses %s as its left table, bound table is %s", left, t.definition.name)
	}
	db, ok := t.db.(JoinExecutor)
	if !ok {
		return nil, fmt.Errorf("the bound database does not support joins")
	}
	return db.Join(ctx, kind, left, right, on)
}

// Joined holds one pair of model rows.
type Joined[L, R any] struct {
	Left  L
	Right R
}

// InnerJoin returns matching pairs. Additional predicates are combined with AND.
func InnerJoin[L, R any](ctx context.Context, db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) ([]Joined[L, R], error) {
	return selectJoin(ctx, db, store.InnerJoin, on, and, on.decodeRight)
}

// LeftJoin preserves unmatched left rows and returns a nil Right for them.
func LeftJoin[L, R any](ctx context.Context, db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) ([]Joined[L, *R], error) {
	return selectJoin(ctx, db, store.LeftJoin, on, and, func(row store.Row) (*R, error) {
		if row == nil {
			return nil, nil
		}
		value, err := on.decodeRight(row)
		return &value, err
	})
}

func selectJoin[L, R, T any](ctx context.Context, db JoinExecutor, kind store.JoinKind, on JoinCondition[L, R], and []JoinCondition[L, R], decodeRight Decoder[T]) ([]Joined[L, T], error) {
	if on.decodeLeft == nil || on.decodeRight == nil {
		return nil, fmt.Errorf("join requires generated columns on both sides")
	}
	pairs := []store.JoinColumns{on.columns}
	for _, extra := range and {
		if extra.left != on.left || extra.right != on.right {
			return nil, fmt.Errorf("all join predicates must refer to %s and %s in that order", on.left, on.right)
		}
		pairs = append(pairs, extra.columns)
	}
	rows, err := db.Join(ctx, kind, on.left, on.right, pairs)
	if err != nil {
		return nil, err
	}
	out := make([]Joined[L, T], len(rows))
	for i, row := range rows {
		out[i].Left, err = on.decodeLeft(row.Left)
		if err != nil {
			return nil, err
		}
		out[i].Right, err = decodeRight(row.Right)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
