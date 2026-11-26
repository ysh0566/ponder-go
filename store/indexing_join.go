package store

import (
	"context"
	"fmt"
	"slices"
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
	if err := db.active(); err != nil {
		return nil, err
	}
	if (kind != InnerJoin && kind != LeftJoin) || len(on) == 0 {
		return nil, fmt.Errorf("join requires INNER or LEFT and at least one equality predicate")
	}
	s := db.writer.s
	left, err := s.model(leftTable)
	if err != nil {
		return nil, err
	}
	right, err := s.model(rightTable)
	if err != nil {
		return nil, err
	}
	var predicates []string
	for _, pair := range on {
		if !slices.ContainsFunc(left.Columns, func(c schema.Column) bool { return c.Name == pair.Left }) {
			return nil, fmt.Errorf("unknown join column %s.%s", left.Name, pair.Left)
		}
		if !slices.ContainsFunc(right.Columns, func(c schema.Column) bool { return c.Name == pair.Right }) {
			return nil, fmt.Errorf("unknown join column %s.%s", right.Name, pair.Right)
		}
		predicates = append(predicates, "l."+schema.Quote(pair.Left)+" = r."+schema.Quote(pair.Right))
	}
	query := "SELECT row_to_json(l.*)::text AS left_row, row_to_json(r.*)::text AS right_row FROM " +
		s.table(left.Name) + " AS l " + string(kind) + " JOIN " + s.table(right.Name) + " AS r ON " + strings.Join(predicates, " AND ")
	var args []any
	if s.Ordering == schema.Isolated {
		query += " AND r.chain_id = l.chain_id WHERE l.chain_id = $1"
		args = append(args, db.writer.event.Checkpoint.ChainID)
	}
	result, err := db.SQL(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := make([]JoinedRow, len(result.Rows))
	for i, row := range result.Rows {
		out[i].Left, err = decodeModelRow(left, row["left_row"])
		if err != nil {
			return nil, err
		}
		if row["right_row"] != nil {
			out[i].Right, err = decodeModelRow(right, row["right_row"])
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
