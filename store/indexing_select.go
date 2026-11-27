package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ysh0566/ponder-go/schema"
)

// SelectFilter compares a physical column with a native value, or tests for NULL.
type SelectFilter struct {
	Column, Operator string
	Value            any
}

type SelectOrder struct {
	Column     string
	Descending bool
}

type SelectOptions struct {
	Filters []SelectFilter
	Order   []SelectOrder
	Limit   *int
	Offset  int
}

// SelectRows applies filters, sorting and pagination in the callback transaction.
func (db *IndexingStore) SelectRows(ctx context.Context, name string, options SelectOptions) ([]Row, error) {
	if err := db.active(); err != nil {
		return nil, err
	}
	s := db.writer.s
	table, err := s.model(name)
	if err != nil {
		return nil, err
	}
	if options.Offset < 0 || (options.Limit != nil && *options.Limit < 0) {
		return nil, fmt.Errorf("limit and offset must be nonnegative")
	}
	column := func(name string) (schema.Column, error) {
		i := slices.IndexFunc(table.Columns, func(c schema.Column) bool { return c.Name == name })
		if i < 0 {
			return schema.Column{}, fmt.Errorf("unknown column %s.%s", table.Name, name)
		}
		return table.Columns[i], nil
	}

	var args []any
	var conditions []string
	if s.Ordering == schema.Isolated {
		conditions = append(conditions, "t.chain_id = $1")
		args = append(args, db.writer.event.Checkpoint.ChainID)
	}
	for _, filter := range options.Filters {
		c, err := column(filter.Column)
		if err != nil {
			return nil, err
		}
		expr := "t." + schema.Quote(c.Name) + " " + filter.Operator
		switch filter.Operator {
		case "IS NULL", "IS NOT NULL":
		case "=", "<>", "<", "<=", ">", ">=":
			value, err := NormalizeColumn(c, filter.Value, false)
			if err != nil {
				return nil, err
			}
			bind, err := s.columnParameter(c, value, &args)
			if err != nil {
				return nil, err
			}
			expr += " " + bind
		default:
			return nil, fmt.Errorf("unsupported filter operator %q", filter.Operator)
		}
		conditions = append(conditions, expr)
	}
	query := "SELECT row_to_json(t)::text AS row FROM " + s.table(name) + " AS t"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	var order []string
	for _, term := range options.Order {
		c, err := column(term.Column)
		if err != nil {
			return nil, err
		}
		expr := "t." + schema.Quote(c.Name)
		if term.Descending {
			expr += " DESC"
		}
		order = append(order, expr)
	}
	if len(order) > 0 {
		query += " ORDER BY " + strings.Join(order, ", ")
	}
	if options.Limit != nil {
		args = append(args, *options.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if options.Offset > 0 {
		args = append(args, options.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	return db.selectModelRows(ctx, table, query, args)
}
