package graphql

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ysh0566/ponder-go/schema"
	gql "github.com/ysh0566/ponder-go/third_party/graphqlgo"
	"github.com/ysh0566/ponder-go/third_party/graphqlgo/language/ast"
)

type collection struct {
	table   schema.Table
	object  *gql.Object
	field   *gql.Field
	filters map[string]condition
	view    bool
}

func newPageInfo(view bool) *gql.Object {
	fields := gql.Fields{
		"hasNextPage":     &gql.Field{Type: gql.NewNonNull(gql.Boolean)},
		"hasPreviousPage": &gql.Field{Type: gql.NewNonNull(gql.Boolean)},
	}
	name := "ViewPageInfo"
	if !view {
		name = "PageInfo"
		fields["startCursor"] = &gql.Field{Type: gql.String}
		fields["endCursor"] = &gql.Field{Type: gql.String}
	}
	return gql.NewObject(gql.ObjectConfig{Name: name, Fields: fields})
}

func (s *Server) tablePage(table schema.Table, entity *gql.Object, info *gql.Object, enums map[string]*gql.Enum, view bool) (*collection, error) {
	filter, ops, err := tableFilter(entity.Name(), table.Columns, enums)
	if err != nil {
		return nil, err
	}
	page := gql.NewObject(gql.ObjectConfig{Name: entity.Name() + "Page", Fields: gql.Fields{
		"items":      &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(entity)))},
		"pageInfo":   &gql.Field{Type: gql.NewNonNull(info)},
		"totalCount": &gql.Field{Type: gql.NewNonNull(gql.Int)},
	}})
	args := gql.FieldConfigArgument{"where": &gql.ArgumentConfig{Type: filter}}
	for _, key := range []string{"orderBy", "orderDirection"} {
		args[key] = &gql.ArgumentConfig{Type: gql.String}
	}
	if !view {
		args["before"] = &gql.ArgumentConfig{Type: gql.String}
		args["after"] = &gql.ArgumentConfig{Type: gql.String}
	}
	for _, key := range []string{"limit", "offset"} {
		args[key] = &gql.ArgumentConfig{Type: gql.Int}
	}
	c := &collection{table: table, object: entity, filters: ops, view: view}
	c.field = &gql.Field{Type: gql.NewNonNull(page), Args: args, Resolve: func(p gql.ResolveParams) (any, error) { return s.readPage(p, c, nil) }}
	return c, nil
}

func wantsCount(info gql.ResolveInfo) bool {
	seen := map[string]bool{}
	var includes func(*ast.SelectionSet) bool
	includes = func(set *ast.SelectionSet) bool {
		if set == nil {
			return false
		}
		for _, selection := range set.Selections {
			switch n := selection.(type) {
			case *ast.Field:
				if n.Name.Value == "totalCount" {
					return true
				}
			case *ast.InlineFragment:
				if includes(n.SelectionSet) {
					return true
				}
			case *ast.FragmentSpread:
				name := n.Name.Value
				if !seen[name] {
					seen[name] = true
					if f, ok := info.Fragments[name].(*ast.FragmentDefinition); ok && includes(f.SelectionSet) {
						return true
					}
				}
			}
		}
		return false
	}
	for _, f := range info.FieldASTs {
		if includes(f.SelectionSet) {
			return true
		}
	}
	return false
}

func (s *Server) readPage(p gql.ResolveParams, c *collection, matches []columnMatch) (any, error) {
	table := c.table
	limit, ok := p.Args["limit"].(int)
	if !ok {
		limit = 50
	}
	offset, _ := p.Args["offset"].(int)
	if limit > 1000 {
		return nil, fmt.Errorf("Invalid limit. Got %d, expected <=1000.", limit)
	}
	if limit < 0 || offset < 0 {
		return nil, fmt.Errorf("limit and offset must be non-negative")
	}
	orderBy, _ := p.Args["orderBy"].(string)
	order, err := orderColumns(table, orderBy)
	if err != nil {
		return nil, err
	}
	direction, _ := p.Args["orderDirection"].(string)
	descending := direction != "" && direction != "asc"
	before, hasBefore := p.Args["before"].(string)
	after, hasAfter := p.Args["after"].(string)
	if hasBefore && hasAfter {
		return nil, fmt.Errorf("Cannot specify both before and after cursors.")
	}
	cursor := after
	which := "after"
	if hasBefore {
		cursor = before
		which = "before"
	}
	hasCursor := hasBefore || hasAfter
	if hasCursor && offset > 0 {
		return nil, fmt.Errorf("Cannot specify both %s cursor and offset.", which)
	}
	params := queryParams{model: s.database.Schema}
	where, _ := p.Args["where"].(map[string]any)
	predicates, err := params.where(where, c.filters)
	if err != nil {
		return nil, err
	}
	for _, match := range matches {
		v, err := params.value(match.column, match.value)
		if err != nil {
			return nil, err
		}
		predicates = append(predicates, schema.Quote(match.column.Name)+" = "+v)
	}
	base := "SELECT * FROM " + schema.Quote(s.namespace) + "." + schema.Quote(table.Name)
	if len(predicates) > 0 {
		base += " WHERE " + strings.Join(predicates, " AND ")
	}
	rowsQuery := "SELECT row_to_json(t) FROM filtered AS t"
	take := limit + 1
	if hasCursor {
		expr, err := params.cursorCondition(order, cursor, descending, hasBefore)
		if err != nil {
			return nil, err
		}
		rowsQuery += " WHERE " + expr
		take++
	}
	orderTerms := make([]string, len(order))
	for i, col := range order {
		orderTerms[i] = schema.Quote(col.Name)
		if descending != hasBefore {
			orderTerms[i] += " DESC"
		}
	}
	if len(orderTerms) > 0 {
		rowsQuery += " ORDER BY " + strings.Join(orderTerms, ",")
	}
	rowsQuery += " LIMIT " + strconv.Itoa(take) + " OFFSET " + strconv.Itoa(offset)
	// The count, when selected, and the items share one PostgreSQL snapshot.
	stmt := "WITH filtered AS NOT MATERIALIZED (" + base + ") SELECT array_to_json(ARRAY(" + rowsQuery + "))::text AS items"
	countRequested := wantsCount(p.Info)
	if countRequested {
		stmt += ",(SELECT count(*)::text FROM filtered) AS total"
	}
	result, err := s.database.QueryReadOnlyInNamespace(p.Context, s.namespace, stmt, params.args...)
	if err != nil {
		return nil, err
	}
	row := result.Rows[0]
	var records []map[string]any
	dec := json.NewDecoder(strings.NewReader(fmt.Sprint(row["items"])))
	dec.UseNumber()
	if err := dec.Decode(&records); err != nil {
		return nil, err
	}
	hasPrevious, hasNext := offset > 0, false
	if hasCursor && len(records) > 0 {
		// In traversal order the inclusive boundary is first. A missing anchor
		// consumes the extra trailing row, as in the source query.
		anchor, err := encodeCursor(order, records[0])
		if err != nil {
			return nil, err
		}
		if anchor == cursor {
			records = records[1:]
			hasPrevious = true
		} else {
			records = records[:len(records)-1]
		}
	}
	if len(records) > limit {
		records = records[:limit]
		hasNext = true
	}
	if hasBefore {
		slices.Reverse(records)
		hasNext, hasPrevious = hasPrevious, hasNext
	}
	pageInfo := map[string]any{"hasNextPage": hasNext, "hasPreviousPage": hasPrevious}
	if !c.view && len(records) > 0 && offset == 0 {
		for key, idx := range map[string]int{"startCursor": 0, "endCursor": len(records) - 1} {
			pageInfo[key], err = encodeCursor(order, records[idx])
			if err != nil {
				return nil, err
			}
		}
	}
	page := map[string]any{"items": records, "pageInfo": pageInfo}
	if countRequested {
		count, err := strconv.ParseInt(fmt.Sprint(row["total"]), 10, 64)
		if err != nil {
			return nil, err
		}
		page["totalCount"] = count
	}
	return page, nil
}
