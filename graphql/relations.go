package graphql

import (
	"fmt"
	"slices"

	gql "github.com/graphql-go/graphql"
	"ponder.local/ponder-go/schema"
)

// Relation describes an entity field independently of database constraints.
// Table, Target, Fields and References use physical database names. A many
// field finds its inverse one field by RelationName or by declaration order.
type Relation struct {
	Table        string   `json:"table"`
	Name         string   `json:"name"`
	Target       string   `json:"target"`
	Many         bool     `json:"many,omitempty"`
	RelationName string   `json:"relationName,omitempty"`
	Fields       []string `json:"fields,omitempty"`
	References   []string `json:"references,omitempty"`
}

type columnMatch struct {
	column schema.Column
	value  any
}

func columnsNamed(table schema.Table, names []string) ([]schema.Column, error) {
	out := make([]schema.Column, len(names))
	for i, name := range names {
		idx := slices.IndexFunc(table.Columns, func(c schema.Column) bool { return c.Name == name })
		if idx < 0 {
			return nil, fmt.Errorf("unknown column %s.%s", table.Name, name)
		}
		out[i] = table.Columns[idx]
	}
	return out, nil
}

func (s *Server) addRelations(collections map[string]*collection, relations []Relation) error {
	for _, r := range relations {
		from, to := collections[r.Table], collections[r.Target]
		if from == nil || to == nil {
			return fmt.Errorf("unknown table in GraphQL relation %s.%s to %s", r.Table, r.Name, r.Target)
		}
		if !schema.Identifier(r.Name) || from.object.Fields()[r.Name] != nil {
			return fmt.Errorf("invalid or duplicate GraphQL relation %s.%s", r.Table, r.Name)
		}
		fields, references := r.Fields, r.References
		if r.Many && len(fields) == 0 && len(references) == 0 {
			for _, inverse := range relations {
				if !inverse.Many && inverse.Table == r.Target && inverse.Target == r.Table && (r.RelationName == "" || r.RelationName == inverse.RelationName) {
					fields, references = inverse.References, inverse.Fields
					break
				}
			}
		}
		if len(fields) == 0 || len(fields) != len(references) {
			return fmt.Errorf("GraphQL relation %s.%s requires matching fields and references", r.Table, r.Name)
		}
		src, err := columnsNamed(from.table, fields)
		if err != nil {
			return err
		}
		dst, err := columnsNamed(to.table, references)
		if err != nil {
			return err
		}
		field := &gql.Field{Type: to.object}
		if r.Many {
			field.Type = to.field.Type.(*gql.NonNull).OfType
			field.Args = to.field.Args
		}
		field.Resolve = func(p gql.ResolveParams) (any, error) {
			parent := p.Source.(map[string]any)
			if r.Many {
				matches := make([]columnMatch, len(dst))
				for i, c := range dst {
					matches[i] = columnMatch{c, parent[src[i].Name]}
				}
				return s.readPage(p, to, matches)
			}
			values := make([]any, len(src))
			for i, c := range src {
				values[i] = parent[c.Name]
			}
			return s.loadOne(p.Context, to.table, dst, values)
		}
		from.object.AddFieldConfig(r.Name, field)
	}
	return nil
}
