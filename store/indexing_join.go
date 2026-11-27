package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ysh0566/ponder-go/schema"
)

type JoinKind string

const (
	InnerJoin JoinKind = "INNER"
	LeftJoin  JoinKind = "LEFT"
)

// JoinColumns names the physical column on each side of an equality predicate.
type JoinColumns struct{ Left, Right string }

// JoinedRow has a nil Right when a left join finds no match.
type JoinedRow struct{ Left, Right Row }

// Join reads onchain tables inside the current event transaction. In Isolated
// mode both sides belong to the event's chain, including unmatched left rows.
func (db *IndexingStore) Join(ctx context.Context, kind JoinKind, leftTable, rightTable string, on []JoinColumns) ([]JoinedRow, error) {
	return db.JoinRows(ctx, kind, leftTable, rightTable, on, SelectOptions{})
}

// JoinRows applies WHERE, ORDER BY, LIMIT and OFFSET to joined rows. Filter
// and order column names use "left." or "right." followed by the physical name.
func (db *IndexingStore) JoinRows(ctx context.Context, kind JoinKind, leftTable, rightTable string, on []JoinColumns, options SelectOptions) ([]JoinedRow, error) {
	rows, err := db.JoinSequence(ctx, leftTable, []JoinStep{{Kind: kind, Table: rightTable, On: on}}, options)
	if err != nil {
		return nil, err
	}
	out := make([]JoinedRow, len(rows))
	for i, row := range rows {
		out[i] = JoinedRow{Left: row[0], Right: row[1]}
	}
	return out, nil
}

// JoinStep adds a table to the preceding result. The right column is always a
// physical name. The left column is a physical name for the first step, then
// a path through the nested result (for example "right.id" or "left.left.id").
type JoinStep struct {
	Kind  JoinKind
	Table string
	On    []JoinColumns
}

// resolveJoinColumn follows the nested Joined shape. Each left descends into
// the preceding result. A right selects the most recently joined table.
func resolveJoinColumn(tables []schema.Table, path string) (schema.Column, string, error) {
	n := len(tables)
	name := path
	for n > 1 {
		side, rest, ok := strings.Cut(name, ".")
		if !ok || (side != "left" && side != "right") {
			return schema.Column{}, "", fmt.Errorf("invalid join column path %q", path)
		}
		name = rest
		if side == "right" {
			break
		}
		n--
	}
	return qualifiedSelectColumn(tables[n-1], "t"+strconv.Itoa(n-1), name)
}

// JoinSequence executes a left-associative chain of joins in the current
// callback. Each row contains one entry per table, in declaration order.
// Unmatched sides of LEFT JOIN are nil.
func (db *IndexingStore) JoinSequence(ctx context.Context, root string, steps []JoinStep, options SelectOptions) ([][]Row, error) {
	if err := db.active(); err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("join requires at least one step")
	}
	s := db.writer.s
	first, err := s.model(root)
	if err != nil {
		return nil, err
	}
	tables := []schema.Table{first}
	projections := []string{"row_to_json(t0.*)::text AS t0"}
	var from strings.Builder
	from.WriteString(s.table(first.Name) + " AS t0")
	for i, step := range steps {
		if (step.Kind != InnerJoin && step.Kind != LeftJoin) || len(step.On) == 0 {
			return nil, fmt.Errorf("join requires INNER or LEFT and at least one equality predicate")
		}
		right, err := s.model(step.Table)
		if err != nil {
			return nil, err
		}
		alias := "t" + strconv.Itoa(i+1)
		var predicates []string
		for _, pair := range step.On {
			_, lhs, err := resolveJoinColumn(tables, pair.Left)
			if err != nil {
				return nil, err
			}
			_, rhs, err := qualifiedSelectColumn(right, alias, pair.Right)
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, lhs+" = "+rhs)
		}
		if s.Ordering == schema.Isolated {
			predicates = append(predicates, alias+".chain_id = t0.chain_id")
		}
		fmt.Fprintf(&from, " %s JOIN %s AS %s ON %s", step.Kind, s.table(right.Name), alias, strings.Join(predicates, " AND "))
		projections = append(projections, "row_to_json("+alias+".*)::text AS "+alias)
		tables = append(tables, right)
	}
	query := "SELECT " + strings.Join(projections, ", ") + " FROM " + from.String()
	query, args, err := db.applySelectOptions(query, options, "t0", func(name string) (schema.Column, string, error) {
		return resolveJoinColumn(tables, name)
	})
	if err != nil {
		return nil, err
	}
	result, err := db.SQL(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := make([][]Row, len(result.Rows))
	for i, row := range result.Rows {
		out[i] = make([]Row, len(tables))
		for j, table := range tables {
			raw := row["t"+strconv.Itoa(j)]
			if raw != nil {
				out[i][j], err = decodeModelRow(table, raw)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}
