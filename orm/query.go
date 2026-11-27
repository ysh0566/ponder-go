package orm

import (
	"context"
	"fmt"
	"slices"

	"github.com/ysh0566/ponder-go/store"
)

// WithEncoder supplies the same native value conversion used by generated inserts.
func (c Column[R, V]) WithEncoder(encode func(V) any) Column[R, V] {
	c.encode = encode
	return c
}

type Predicate[R any] struct {
	table  string
	filter store.SelectFilter
	err    error
}

func (c Column[R, V]) compare(op string, v V) Predicate[R] {
	if c.encode == nil {
		return Predicate[R]{table: c.table, err: fmt.Errorf("regenerate the typed model to provide a value encoder for %s.%s", c.table, c.name)}
	}
	value, err := resolveColumnValue(c.table, c.name, c.encode(v))
	return Predicate[R]{c.table, store.SelectFilter{Column: c.name, Operator: op, Value: value}, err}
}

func (c Column[R, V]) Eq(v V) Predicate[R]  { return c.compare("=", v) }
func (c Column[R, V]) Ne(v V) Predicate[R]  { return c.compare("<>", v) }
func (c Column[R, V]) Lt(v V) Predicate[R]  { return c.compare("<", v) }
func (c Column[R, V]) Lte(v V) Predicate[R] { return c.compare("<=", v) }
func (c Column[R, V]) Gt(v V) Predicate[R]  { return c.compare(">", v) }
func (c Column[R, V]) Gte(v V) Predicate[R] { return c.compare(">=", v) }

func (c Column[R, V]) IsNull() Predicate[R] {
	return Predicate[R]{table: c.table, filter: store.SelectFilter{Column: c.name, Operator: "IS NULL"}}
}
func (c Column[R, V]) IsNotNull() Predicate[R] {
	return Predicate[R]{table: c.table, filter: store.SelectFilter{Column: c.name, Operator: "IS NOT NULL"}}
}

type Order[R any] struct {
	table string
	term  store.SelectOrder
}

func (c Column[R, V]) Asc() Order[R] {
	return Order[R]{c.table, store.SelectOrder{Column: c.name}}
}
func (c Column[R, V]) Desc() Order[R] {
	return Order[R]{c.table, store.SelectOrder{Column: c.name, Descending: true}}
}

type selectExecutor interface {
	SelectRows(context.Context, string, store.SelectOptions) ([]store.Row, error)
}

// Query is a reusable single-table SELECT. Each method returns a new value.
type Query[R any] struct {
	table   string
	db      selectExecutor
	decode  Decoder[R]
	options store.SelectOptions
	err     error
}

// Query starts a typed SELECT over this bound table.
func (t Table[K, I, U, R]) Query() Query[R] {
	db, _ := t.db.(selectExecutor)
	return Query[R]{table: t.definition.name, db: db, decode: t.definition.decode}
}

// Where combines predicates with AND, including predicates from earlier calls.
func (q Query[R]) Where(filters ...Predicate[R]) Query[R] {
	if q.err != nil {
		return q
	}
	q.options.Filters = slices.Clip(q.options.Filters)
	for _, p := range filters {
		if p.table != q.table {
			q.err = fmt.Errorf("filter column must belong to %s", q.table)
			return q
		}
		if p.err != nil {
			q.err = p.err
			return q
		}
		q.options.Filters = append(q.options.Filters, p.filter)
	}
	return q
}

// OrderBy appends sort keys in descending order of priority.
func (q Query[R]) OrderBy(terms ...Order[R]) Query[R] {
	if q.err != nil {
		return q
	}
	q.options.Order = slices.Clip(q.options.Order)
	for _, order := range terms {
		if order.table != q.table {
			q.err = fmt.Errorf("sort column must belong to %s", q.table)
			return q
		}
		q.options.Order = append(q.options.Order, order.term)
	}
	return q
}

func (q Query[R]) Limit(n int) Query[R] {
	q.options.Limit = &n
	return q
}
func (q Query[R]) Offset(n int) Query[R] {
	q.options.Offset = n
	return q
}

// All executes the SELECT and decodes its rows with the bound model.
func (q Query[R]) All(ctx context.Context) ([]R, error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.db == nil {
		return nil, fmt.Errorf("the bound database does not support model queries")
	}
	rows, err := q.db.SelectRows(ctx, q.table, q.options)
	if err != nil {
		return nil, err
	}
	return decodeRows(q.decode, rows)
}
