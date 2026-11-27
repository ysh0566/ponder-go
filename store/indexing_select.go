package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ysh0566/ponder-go/schema"
)

// SelectFilter compares a physical column with a native value, or tests for NULL.
// An AND or OR group has an empty Column and a nonempty []SelectFilter as Value.
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
	var where strings.Builder
	var args []any
	if s.Ordering == schema.Isolated {
		where.WriteString("t.chain_id = $1")
		args = append(args, db.writer.event.Checkpoint.ChainID)
	}
	path := make(map[*SelectFilter]bool)
	for i := range options.Filters {
		if where.Len() > 0 {
			where.WriteString(" AND ")
		}
		if err := s.writeSelectFilter(&where, table, &options.Filters[i], &args, path); err != nil {
			return nil, err
		}
	}
	query := "SELECT row_to_json(t)::text AS row FROM " + s.table(name) + " AS t"
	if where.Len() > 0 {
		query += " WHERE " + where.String()
	}
	var order []string
	for _, term := range options.Order {
		c, err := selectColumn(table, term.Column)
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

func selectColumn(table schema.Table, name string) (schema.Column, error) {
	i := slices.IndexFunc(table.Columns, func(c schema.Column) bool { return c.Name == name })
	if i < 0 {
		return schema.Column{}, fmt.Errorf("unknown column %s.%s", table.Name, name)
	}
	return table.Columns[i], nil
}

func (s *Store) writeSelectFilter(sql *strings.Builder, table schema.Table, filter *SelectFilter, args *[]any, path map[*SelectFilter]bool) error {
	if filter.Operator == "AND" || filter.Operator == "OR" {
		children, ok := filter.Value.([]SelectFilter)
		if !ok || len(children) == 0 || filter.Column != "" {
			return fmt.Errorf("%s filter requires a nonempty group of predicates", filter.Operator)
		}
		if path[filter] {
			return fmt.Errorf("cyclic filter group")
		}
		path[filter] = true
		defer delete(path, filter)
		sql.WriteByte('(')
		for i := range children {
			if i > 0 {
				sql.WriteString(" " + filter.Operator + " ")
			}
			if err := s.writeSelectFilter(sql, table, &children[i], args, path); err != nil {
				return err
			}
		}
		sql.WriteByte(')')
		return nil
	}
	c, err := selectColumn(table, filter.Column)
	if err != nil {
		return err
	}
	expr := "t." + schema.Quote(c.Name) + " " + filter.Operator
	switch filter.Operator {
	case "IS NULL", "IS NOT NULL":
	case "=", "<>", "<", "<=", ">", ">=":
		value, err := NormalizeColumn(c, filter.Value, false)
		if err != nil {
			return err
		}
		bind, err := s.columnParameter(c, value, args)
		if err != nil {
			return err
		}
		expr += " " + bind
	default:
		return fmt.Errorf("unsupported filter operator %q", filter.Operator)
	}
	sql.WriteString(expr)
	return nil
}
