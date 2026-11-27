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
	return t.JoinRows(ctx, kind, left, right, on, store.SelectOptions{})
}

type joinRowsExecutor interface {
	JoinRows(context.Context, store.JoinKind, string, string, []store.JoinColumns, store.SelectOptions) ([]store.JoinedRow, error)
}

// JoinRows lets a generated callback table execute a join with query clauses.
func (t Table[K, I, U, R]) JoinRows(ctx context.Context, kind store.JoinKind, left, right string, on []store.JoinColumns, options store.SelectOptions) ([]store.JoinedRow, error) {
	if t.definition.name != left {
		return nil, fmt.Errorf("join uses %s as its left table, bound table is %s", left, t.definition.name)
	}
	db, ok := t.db.(JoinExecutor)
	if !ok {
		return nil, fmt.Errorf("the bound database does not support joins")
	}
	return readJoinRows(ctx, db, kind, left, right, on, options)
}

// Joined holds one pair of model rows.
type Joined[L, R any] struct {
	Left  L
	Right R
}

// JoinSide identifies one input to a joined result, including a self join.
type JoinSide[Model, Result any] struct {
	table, scope, prefix string
}

// JoinColumn binds a generated column to the left or right input of a query.
func JoinColumn[Model, Result, V any](side JoinSide[Model, Result], c Column[Model, V]) Column[Result, V] {
	if side.table == "" || side.table != c.table {
		return Column[Result, V]{}
	}
	return Column[Result, V]{table: side.scope, name: side.prefix + c.name, encode: c.encode}
}

// JoinQuery provides the typed result and the two sides used by JoinColumn.
// T is R for inner joins and *R for left joins.
type JoinQuery[L, R, T any] struct {
	Query[Joined[L, T]]
	Left  JoinSide[L, Joined[L, T]]
	Right JoinSide[R, Joined[L, T]]
}

// InnerJoin returns matching pairs. Additional predicates are combined with AND.
func InnerJoin[L, R any](ctx context.Context, db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) ([]Joined[L, R], error) {
	return QueryInnerJoin(db, on, and...).All(ctx)
}

// QueryInnerJoin starts a query over matching pairs.
func QueryInnerJoin[L, R any](db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) JoinQuery[L, R, R] {
	return buildJoinQuery(db, store.InnerJoin, on, and, on.decodeRight)
}

// LeftJoin preserves unmatched left rows and returns a nil Right for them.
func LeftJoin[L, R any](ctx context.Context, db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) ([]Joined[L, *R], error) {
	return QueryLeftJoin(db, on, and...).All(ctx)
}

// QueryLeftJoin starts a query that preserves unmatched left rows.
func QueryLeftJoin[L, R any](db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) JoinQuery[L, R, *R] {
	return buildJoinQuery(db, store.LeftJoin, on, and, func(row store.Row) (*R, error) {
		if row == nil {
			return nil, nil
		}
		value, err := on.decodeRight(row)
		return &value, err
	})
}

func buildJoinQuery[L, R, T any](db JoinExecutor, kind store.JoinKind, on JoinCondition[L, R], and []JoinCondition[L, R], decodeRight Decoder[T]) JoinQuery[L, R, T] {
	// Spaces cannot occur in physical model names. The scope distinguishes both
	// inputs even when different definitions use the same Go row type.
	scope := on.left + " JOIN " + on.right
	q := JoinQuery[L, R, T]{
		Query: Query[Joined[L, T]]{table: scope},
		Left:  JoinSide[L, Joined[L, T]]{on.left, scope, "left."},
		Right: JoinSide[R, Joined[L, T]]{on.right, scope, "right."},
	}
	if on.decodeLeft == nil || on.decodeRight == nil {
		q.err = fmt.Errorf("join requires generated columns on both sides")
		return q
	}
	pairs := []store.JoinColumns{on.columns}
	for _, extra := range and {
		if extra.left != on.left || extra.right != on.right {
			q.err = fmt.Errorf("all join predicates must refer to %s and %s in that order", on.left, on.right)
			return q
		}
		pairs = append(pairs, extra.columns)
	}
	q.read = func(ctx context.Context, options store.SelectOptions) ([]Joined[L, T], error) {
		rows, err := readJoinRows(ctx, db, kind, on.left, on.right, pairs, options)
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
	return q
}

// Keep the original executor contract for queries without additional clauses.
func readJoinRows(ctx context.Context, db JoinExecutor, kind store.JoinKind, left, right string, pairs []store.JoinColumns, options store.SelectOptions) ([]store.JoinedRow, error) {
	if db == nil {
		return nil, fmt.Errorf("join requires a bound database")
	}
	if len(options.Filters) == 0 && len(options.Order) == 0 && options.Limit == nil && options.Offset == 0 {
		return db.Join(ctx, kind, left, right, pairs)
	}
	executor, ok := db.(joinRowsExecutor)
	if !ok {
		return nil, fmt.Errorf("the bound database does not support join query clauses")
	}
	return executor.JoinRows(ctx, kind, left, right, pairs, options)
}
