package orm

import (
	"context"
	"fmt"
	"slices"

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
	plan  joinPlan[Joined[L, T]]
}

// InnerJoin returns matching pairs. Additional predicates are combined with AND.
func InnerJoin[L, R any](ctx context.Context, db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) ([]Joined[L, R], error) {
	return QueryInnerJoin(db, on, and...).All(ctx)
}

// QueryInnerJoin starts a query over matching pairs.
func QueryInnerJoin[L, R any](db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) JoinQuery[L, R, R] {
	return buildJoinQuery(firstJoinInput(db, on), store.InnerJoin, on, and, on.decodeRight)
}

// LeftJoin preserves unmatched left rows and returns a nil Right for them.
func LeftJoin[L, R any](ctx context.Context, db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) ([]Joined[L, *R], error) {
	return QueryLeftJoin(db, on, and...).All(ctx)
}

// QueryLeftJoin starts a query that preserves unmatched left rows.
func QueryLeftJoin[L, R any](db JoinExecutor, on JoinCondition[L, R], and ...JoinCondition[L, R]) JoinQuery[L, R, *R] {
	return buildJoinQuery(firstJoinInput(db, on), store.LeftJoin, on, and, nullableDecoder(on.decodeRight))
}

// ThenInnerJoin adds a required table. Use JoinColumn on the preceding query
// to identify the existing side of each equality.
func ThenInnerJoin[A, B, T, R any](q JoinQuery[A, B, T], on JoinCondition[Joined[A, T], R], and ...JoinCondition[Joined[A, T], R]) JoinQuery[Joined[A, T], R, R] {
	return buildJoinQuery(q.nextInput(), store.InnerJoin, on, and, on.decodeRight)
}

// ThenLeftJoin adds an optional table. Its Right field is nil on a missing match.
func ThenLeftJoin[A, B, T, R any](q JoinQuery[A, B, T], on JoinCondition[Joined[A, T], R], and ...JoinCondition[Joined[A, T], R]) JoinQuery[Joined[A, T], R, *R] {
	return buildJoinQuery(q.nextInput(), store.LeftJoin, on, and, nullableDecoder(on.decodeRight))
}

func nullableDecoder[R any](decode Decoder[R]) Decoder[*R] {
	return func(row store.Row) (*R, error) {
		if row == nil {
			return nil, nil
		}
		value, err := decode(row)
		return &value, err
	}
}

type joinPlan[R any] struct {
	db          JoinExecutor
	root, scope string
	steps       []store.JoinStep
	decode      func([]store.Row) (R, error)
	err         error
}

func firstJoinInput[L, R any](db JoinExecutor, on JoinCondition[L, R]) joinPlan[L] {
	p := joinPlan[L]{db: db, root: on.left, scope: on.left}
	if on.decodeLeft != nil {
		p.decode = func(rows []store.Row) (L, error) { return on.decodeLeft(rows[0]) }
	}
	return p
}

func (q JoinQuery[L, R, T]) nextInput() joinPlan[Joined[L, T]] {
	p := q.plan
	p.err = q.err
	if p.err == nil && hasJoinOptions(q.options) {
		p.err = fmt.Errorf("add joins before filtering, ordering or paging")
	}
	return p
}

func buildJoinQuery[L, R, T any](input joinPlan[L], kind store.JoinKind, on JoinCondition[L, R], and []JoinCondition[L, R], decodeRight Decoder[T]) JoinQuery[L, R, T] {
	scope := input.scope + " JOIN " + on.right
	q := JoinQuery[L, R, T]{
		Query: Query[Joined[L, T]]{table: scope, err: input.err},
		Left:  JoinSide[L, Joined[L, T]]{input.scope, scope, "left."},
		Right: JoinSide[R, Joined[L, T]]{on.right, scope, "right."},
	}
	if q.err != nil {
		return q
	}
	if input.decode == nil || on.left != input.scope || on.decodeRight == nil {
		q.err = fmt.Errorf("join requires a column from its left input and a generated right column")
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
	steps := append(slices.Clip(input.steps), store.JoinStep{Kind: kind, Table: on.right, On: pairs})
	q.plan = joinPlan[Joined[L, T]]{db: input.db, root: input.root, scope: scope, steps: steps,
		decode: func(rows []store.Row) (pair Joined[L, T], err error) {
			last := len(rows) - 1
			pair.Left, err = input.decode(rows[:last])
			if err == nil {
				pair.Right, err = decodeRight(rows[last])
			}
			return pair, err
		},
	}
	q.read = q.plan.all
	return q
}

func (p joinPlan[R]) all(ctx context.Context, options store.SelectOptions) ([]R, error) {
	// An executor can embed the native store and override its two-table methods.
	// Preserve those overrides even when it also inherits JoinSequence.
	if len(p.steps) == 1 {
		step := p.steps[0]
		rows, err := readJoinRows(ctx, p.db, step.Kind, p.root, step.Table, step.On, options)
		if err != nil {
			return nil, err
		}
		return decodeRows(func(row store.JoinedRow) (R, error) {
			return p.decode([]store.Row{row.Left, row.Right})
		}, rows)
	}
	db, ok := p.db.(sequenceExecutor)
	if !ok {
		return nil, fmt.Errorf("the bound database does not support chained joins")
	}
	rows, err := db.JoinSequence(ctx, p.root, p.steps, options)
	if err != nil {
		return nil, err
	}
	return decodeRows(func(row []store.Row) (R, error) {
		if len(row) != len(p.steps)+1 {
			var zero R
			return zero, fmt.Errorf("join executor returned %d tables, expected %d", len(row), len(p.steps)+1)
		}
		return p.decode(row)
	}, rows)
}

type sequenceExecutor interface {
	JoinSequence(context.Context, string, []store.JoinStep, store.SelectOptions) ([][]store.Row, error)
}

// JoinSequence lets a generated callback table execute a chain of model joins.
func (t Table[K, I, U, R]) JoinSequence(ctx context.Context, root string, steps []store.JoinStep, options store.SelectOptions) ([][]store.Row, error) {
	if root != t.definition.name {
		return nil, fmt.Errorf("join uses %s as its left table, bound table is %s", root, t.definition.name)
	}
	db, ok := t.db.(sequenceExecutor)
	if !ok {
		return nil, fmt.Errorf("the bound database does not support chained joins")
	}
	return db.JoinSequence(ctx, root, steps, options)
}

func hasJoinOptions(options store.SelectOptions) bool {
	return len(options.Filters) != 0 || len(options.Order) != 0 || options.Limit != nil || options.Offset != 0
}

// Keep the original executor contract for queries without additional clauses.
func readJoinRows(ctx context.Context, db JoinExecutor, kind store.JoinKind, left, right string, pairs []store.JoinColumns, options store.SelectOptions) ([]store.JoinedRow, error) {
	if db == nil {
		return nil, fmt.Errorf("join requires a bound database")
	}
	if !hasJoinOptions(options) {
		return db.Join(ctx, kind, left, right, pairs)
	}
	executor, ok := db.(joinRowsExecutor)
	if !ok {
		return nil, fmt.Errorf("the bound database does not support join query clauses")
	}
	return executor.JoinRows(ctx, kind, left, right, pairs, options)
}
