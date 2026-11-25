package graphql

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	gql "github.com/ysh0566/ponder-go/third_party/graphqlgo"
	"github.com/ysh0566/ponder-go/schema"
)

type condition struct {
	column schema.Column
	suffix string
}

var numericFilters = []string{"_gt", "_lt", "_gte", "_lte"}
var textFilters = []string{"_contains", "_not_contains", "_starts_with", "_not_starts_with", "_ends_with", "_not_ends_with"}

// The input type and its SQL bindings are built together from the model. This
// also preserves field names that themselves contain an operator suffix.
func tableFilter(name string, columns []schema.Column, enums map[string]*gql.Enum) (*gql.InputObject, map[string]condition, error) {
	fields := gql.InputObjectConfigFieldMap{}
	ops := map[string]condition{}
	for _, col := range columns {
		typ, err := columnType(col, enums)
		if err != nil {
			return nil, nil, err
		}
		if typ == jsonScalar {
			continue
		}
		add := func(suffix string, t gql.Input) {
			key := property(col) + suffix
			fields[key] = &gql.InputObjectFieldConfig{Type: t}
			ops[key] = condition{col, suffix}
		}
		if list, ok := typ.(*gql.List); ok {
			base := gql.GetNamed(list).(gql.Input)
			array := gql.NewList(base)
			add("", array)
			add("_not", array)
			add("_has", base)
			add("_not_has", base)
			continue
		}
		add("", typ)
		add("_not", typ)
		many := gql.NewList(typ)
		add("_in", many)
		add("_not_in", many)
		switch typ {
		case gql.String:
			for _, op := range textFilters {
				add(op, typ)
				if col.Type != "hex" && col.Type != "address" {
					add(op+"_nocase", typ)
				}
			}
		case gql.Int, gql.Float, bigIntScalar, numericScalar:
			for _, op := range numericFilters {
				add(op, typ)
			}
		}
	}
	var filter *gql.InputObject
	filter = gql.NewInputObject(gql.InputObjectConfig{Name: name + "Filter", Fields: gql.InputObjectConfigFieldMapThunk(func() gql.InputObjectConfigFieldMap {
		fields["AND"] = &gql.InputObjectFieldConfig{Type: gql.NewList(filter)}
		fields["OR"] = &gql.InputObjectFieldConfig{Type: gql.NewList(filter)}
		return fields
	})})
	return filter, ops, nil
}

type queryParams struct {
	model schema.Schema
	args  []any
}

func (q *queryParams) bind(v any) string {
	q.args = append(q.args, v)
	return fmt.Sprintf("$%d", len(q.args))
}

func (q *queryParams) value(col schema.Column, v any) (string, error) {
	typ, err := q.model.SQLType(col.Type, "")
	if err != nil {
		return "", err
	}
	if items, ok := v.([]any); ok {
		if col.Type == "bytes" {
			b := make([]byte, len(items))
			for i, n := range items {
				num, ok := n.(int)
				if !ok || num < 0 || num > 255 {
					return "", fmt.Errorf("invalid byte value %v", n)
				}
				b[i] = byte(num)
			}
			return "decode(" + q.bind(hex.EncodeToString(b)) + "::text,'hex')", nil
		}
		inner, isArray := strings.CutSuffix(col.Type, "[]")
		if !isArray {
			return "", fmt.Errorf("expected scalar value for %s", property(col))
		}
		col.Type = inner
		values := make([]string, len(items))
		for i, x := range items {
			values[i], err = q.value(col, x)
			if err != nil {
				return "", err
			}
		}
		return "ARRAY[" + strings.Join(values, ",") + "]::" + typ, nil
	}
	if v != nil {
		v = fmt.Sprint(v)
		if col.Type == "hex" || col.Type == "address" {
			v, err = canonicalHex(v.(string))
			if err != nil {
				return "", err
			}
		}
	}
	return "(" + q.bind(v) + "::text)::" + typ, nil
}

func (q *queryParams) where(where map[string]any, fields map[string]condition) ([]string, error) {
	var parts []string
	keys := make([]string, 0, len(where))
	for key := range where {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := where[key]
		if key == "AND" || key == "OR" {
			list, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("invalid %s filter", key)
			}
			var group []string
			for _, child := range list {
				nested, ok := child.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid %s operand", key)
				}
				items, err := q.where(nested, fields)
				if err != nil {
					return nil, err
				}
				group = append(group, items...)
			}
			if len(group) > 0 {
				parts = append(parts, "("+strings.Join(group, " "+key+" ")+")")
			}
			continue
		}
		op, ok := fields[key]
		if !ok {
			return nil, fmt.Errorf("unknown filter %q", key)
		}
		expr, err := q.condition(op, value)
		if err != nil {
			return nil, err
		}
		parts = append(parts, expr)
	}
	return parts, nil
}

var comparisons = map[string]string{"": "=", "_not": "<>", "_gt": ">", "_lt": "<", "_gte": ">=", "_lte": "<="}

func (q *queryParams) condition(op condition, v any) (string, error) {
	col := op.column
	name := schema.Quote(col.Name)
	key := op.suffix
	if v == nil && (key == "" || key == "_not") {
		if key == "_not" {
			return name + " IS NOT NULL", nil
		}
		return name + " IS NULL", nil
	}
	switch key {
	case "_in", "_not_in":
		items, ok := v.([]any)
		if !ok {
			return "", fmt.Errorf("expected a list for %s", key)
		}
		if len(items) == 0 {
			if key == "_in" {
				return "FALSE", nil
			}
			return "TRUE", nil
		}
		values := make([]string, len(items))
		for i, item := range items {
			var err error
			values[i], err = q.value(col, item)
			if err != nil {
				return "", err
			}
		}
		test := " IN ("
		if key == "_not_in" {
			test = " NOT IN ("
		}
		return name + test + strings.Join(values, ",") + ")", nil
	case "_has", "_not_has":
		needle, err := q.value(col, []any{v})
		if err != nil {
			return "", err
		}
		expr := name + " @> " + needle
		if col.Type == "bytes" {
			expr = "position(" + needle + " in " + name + ") > 0"
		}
		if key == "_not_has" {
			expr = "NOT (" + expr + ")"
		}
		return expr, nil
	}
	if sign, ok := comparisons[key]; ok {
		bound, err := q.value(col, v)
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(col.Type, "[]") && (key == "" || key == "_not") {
			expr := "(" + name + " @> " + bound + " AND " + name + " <@ " + bound + ")"
			if key == "_not" {
				expr = "NOT " + expr
			}
			return expr, nil
		}
		return name + " " + sign + " " + bound, nil
	}
	pattern := fmt.Sprint(v)
	switch {
	case strings.Contains(key, "contains"):
		pattern = "%" + pattern + "%"
	case strings.Contains(key, "starts_with"):
		pattern += "%"
	case strings.Contains(key, "ends_with"):
		pattern = "%" + pattern
	default:
		return "", fmt.Errorf("unknown filter operator %q", key)
	}
	operator := "LIKE"
	if strings.HasSuffix(key, "_nocase") {
		operator = "ILIKE"
	}
	if strings.HasPrefix(key, "_not_") {
		operator = "NOT " + operator
	}
	return name + " " + operator + " " + q.bind(pattern) + "::text", nil
}
