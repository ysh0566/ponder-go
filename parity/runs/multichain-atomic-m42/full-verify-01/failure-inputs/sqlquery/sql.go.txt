// Package sqlquery validates PostgreSQL ASTs using the pinned Ponder rule set.
package sqlquery

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	pgquery "github.com/pganalyze/pg_query_go/v5"
	"github.com/pganalyze/pg_query_go/v5/parser"
)

//go:embed rules.json
var ruleData []byte

type field struct {
	Field string `json:"field"`
	Wrap  string `json:"wrap"`
}

var rules struct {
	Allow, Find, Functions []string
	SystemTables           []string `json:"system_tables"`
	Nodes                  map[string][]field
}

func init() {
	if e := json.Unmarshal(ruleData, &rules); e != nil {
		panic(e)
	}
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func Parse(sql string) (map[string]any, error) {
	if sourceStringLength(sql) > 5000 {
		return nil, fmt.Errorf("Invalid query")
	}
	tree, err := ParseStatement(sql)
	var syntax *parser.Error
	if errors.As(err, &syntax) {
		return nil, &SourceParseError{Cause: err}
	}
	return tree, err
}

// SourceParseError preserves the actual source helper's Error(object) message
// while retaining the native PostgreSQL diagnostic as an inspectable cause.
// ParseStatement continues to return the detailed trusted-indexing error.
type SourceParseError struct{ Cause error }

func (e *SourceParseError) Error() string { return "[object Object]" }
func (e *SourceParseError) Unwrap() error { return e.Cause }

// ParseStatement parses trusted indexing SQL without the public HTTP size cap.
func ParseStatement(sql string) (map[string]any, error) {
	text, e := pgquery.ParseToJSON(sql)
	if e != nil {
		return nil, e
	}
	var tree struct {
		Stmts []struct{ Stmt map[string]any }
	}
	if e = json.Unmarshal([]byte(text), &tree); e != nil {
		return nil, e
	}
	if len(tree.Stmts) == 0 {
		return nil, fmt.Errorf("Invalid query")
	}
	if len(tree.Stmts) > 1 {
		return nil, fmt.Errorf("Multiple statements not supported")
	}
	if tree.Stmts[0].Stmt == nil {
		return nil, fmt.Errorf("Invalid query")
	}
	return tree.Stmts[0].Stmt, nil
}

type walkMode uint8

const (
	findRelations walkMode = iota
	validateAllowable
	readonlyAllowable
)

func walk(node map[string]any, mode walkMode, visit func(string, map[string]any)) error {
	for kind, value := range node {
		body, _ := value.(map[string]any)
		if body == nil {
			body = map[string]any{}
		}
		allowed := rules.Find
		if mode != findRelations {
			allowed = rules.Allow
		}
		if !contains(allowed, kind) {
			return fmt.Errorf("%s not supported", kind)
		}
		if mode == validateAllowable {
			switch kind {
			case "RangeVar":
				if body["schemaname"] != nil {
					return fmt.Errorf("Schema name not supported")
				}
				if name, ok := body["relname"].(string); ok && contains(rules.SystemTables, name) {
					return fmt.Errorf("System tables not supported")
				}
			case "FuncCall":
				names, _ := body["funcname"].([]any)
				if len(names) == 0 {
					return fmt.Errorf("Function call not supported")
				}
				for _, v := range names {
					m, _ := v.(map[string]any)
					s, _ := m["String"].(map[string]any)
					name, _ := s["sval"].(string)
					if !contains(rules.Functions, name) {
						return fmt.Errorf("Function call not supported")
					}
				}
			case "WithClause":
				if body["recursive"] == true {
					return fmt.Errorf("Recursive CTEs not supported")
				}
			case "CommonTableExpr":
				if body["ctematerialized"] == "CTEMaterializeAlways" || body["cterecursive"] == true {
					return fmt.Errorf("Invalid CTE")
				}
			case "SelectStmt":
				if body["lockingClause"] != nil {
					return fmt.Errorf("Invalid query")
				}
			}
		}
		if visit != nil {
			visit(kind, body)
		}
		for _, f := range rules.Nodes[kind] {
			child := body[f.Field]
			if child == nil {
				continue
			}
			values, ok := child.([]any)
			if !ok {
				values = []any{child}
			}
			for _, v := range values {
				m, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if f.Wrap != "" {
					m = map[string]any{f.Wrap: m}
				}
				if e := walk(m, mode, visit); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func Validate(sql string) error { return defaultValidator.Validate(sql) }

// IsReadonly checks the source AST allow-list without the public function,
// schema and recursion restrictions. It is distinct from Validate.
func IsReadonly(sql string) bool {
	tree, e := Parse(sql)
	return e == nil && walk(tree, readonlyAllowable, nil) == nil
}

func sourceStringLength(s string) int {
	length := 0
	for _, r := range s {
		length++
		if r > 0xffff {
			length++
		}
	}
	return length
}

// Relations returns a snapshot of the cached ordered relation set.
func Relations(sql string) ([]string, error) {
	set, err := GetSQLQueryRelations(sql)
	if err != nil {
		return nil, err
	}
	return set.Values(), nil
}

// GetSQLQueryRelations retains the source cache's set identity.
func GetSQLQueryRelations(sql string) (*RelationSet, error) {
	return defaultValidator.Relations(sql)
}

func CTEs(sql string) ([]string, error) {
	tree, e := Parse(sql)
	if e != nil {
		return nil, e
	}
	out := []string{}
	e = walk(tree, findRelations, func(kind string, node map[string]any) {
		if kind == "CommonTableExpr" {
			name, _ := node["ctename"].(string)
			out = append(out, name)
		}
	})
	return out, e
}
