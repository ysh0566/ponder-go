// Package schema describes native Go onchain tables and validates Ponder restrictions.
package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Ordering string

const (
	Multichain Ordering = "multichain"
	Omnichain  Ordering = "omnichain"
	Isolated   Ordering = "experimental_isolated"
)

type Column struct {
	Name                string           `json:"name"`
	Property            string           `json:"property,omitempty"`
	Type                string           `json:"type"`
	Primary             bool             `json:"primary,omitempty"`
	NotNull             bool             `json:"notNull,omitempty"`
	Unique              bool             `json:"unique,omitempty"`
	Generated           bool             `json:"generated,omitempty"`
	GeneratedExpression any              `json:"generatedExpression,omitempty"`
	Identity            bool             `json:"identity,omitempty"`
	IdentityOptions     *IdentityOptions `json:"identityOptions,omitempty"`
	References          string           `json:"references,omitempty"`
	Default             any              `json:"default,omitempty"`
	RawDefault          bool             `json:"rawDefault,omitempty"`
	DefaultFn           func() any       `json:"-"`
	OnUpdateFn          func() any       `json:"-"`
}
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}
type Table struct {
	Name             string            `json:"name"`
	ExportName       string            `json:"exportName,omitempty"`
	Columns          []Column          `json:"columns"`
	PrimaryKey       []string          `json:"primaryKey,omitempty"`
	Indexes          []Index           `json:"indexes,omitempty"`
	ForeignKeys      bool              `json:"foreignKeys,omitempty"`
	Checks           bool              `json:"checks,omitempty"`
	CheckConstraints []CheckConstraint `json:"checkConstraints,omitempty"`
	Unique           bool              `json:"unique,omitempty"`
}
type Schema struct {
	Tables    []Table    `json:"tables"`
	Enums     []Enum     `json:"enums,omitempty"`
	Views     []View     `json:"views,omitempty"`
	Sequences []Sequence `json:"sequences,omitempty"`
}

type RawSQL struct{ Text string }
type IdentityOptions struct {
	StartWith int64 `json:"startWith"`
}
type CheckConstraint struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
}
type Sequence struct {
	Name string `json:"name"`
}
type Enum struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}
type ViewField struct {
	Name       string  `json:"name"`
	Column     *Column `json:"column,omitempty"`
	Expression string  `json:"expression,omitempty"`
	Aliased    bool    `json:"aliased,omitempty"`
}
type View struct {
	Name     string      `json:"name"`
	Fields   []ViewField `json:"fields"`
	Query    string      `json:"query"`
	Existing bool        `json:"existing,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]*$`)

func Identifier(s string) bool { return identifier.MatchString(s) }
func Quote(s string) string    { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func (t Table) Keys() []string {
	if len(t.PrimaryKey) > 0 {
		return t.PrimaryKey
	}
	for _, c := range t.Columns {
		if c.Primary {
			return []string{c.Name}
		}
	}
	return nil
}
func SQLType(t string) (string, error) {
	types := map[string]string{"text": "text", "hex": "text", "address": "text", "bigint": "numeric(78,0)", "int8": "bigint", "integer": "integer", "smallint": "smallint", "boolean": "boolean", "json": "json", "jsonb": "jsonb", "bytes": "bytea", "timestamp": "timestamp", "date": "date", "numeric": "numeric", "real": "real", "doublePrecision": "double precision", "uuid": "uuid"}
	base := strings.TrimSuffix(t, "[]")
	s, ok := types[base]
	if !ok {
		return "", fmt.Errorf("unsupported column type %q", t)
	}
	if strings.HasSuffix(t, "[]") {
		s += "[]"
	}
	return s, nil
}
func (s Schema) Validate(order Ordering) error {
	if order != Multichain && order != Omnichain && order != Isolated {
		return fmt.Errorf("invalid ordering %q", order)
	}
	names, indexes := map[string]bool{}, map[string]bool{}
	enums := map[string]bool{}
	for _, enum := range s.Enums {
		if !Identifier(enum.Name) || len(enum.Values) == 0 || enums[enum.Name] {
			return fmt.Errorf("invalid or duplicate enum %q", enum.Name)
		}
		enums[enum.Name] = true
		seen := map[string]bool{}
		for _, v := range enum.Values {
			if seen[v] {
				return fmt.Errorf("duplicate enum value %q", v)
			}
			seen[v] = true
		}
	}
	for _, t := range s.Tables {
		fail := func(msg string) error { return fmt.Errorf("Schema validation failed: '%s' %s", t.Name, msg) }
		if !Identifier(t.Name) || len(t.Name) > 45 {
			return fail("invalid table name (maximum 45 characters).")
		}
		exportName := t.ExportName
		if exportName == "" {
			exportName = t.Name
		}
		if len(exportName) > 45 {
			return fail("table name cannot be longer than 45 characters.")
		}
		if exportName == "_ponder_meta" || exportName == "_ponder_checkpoint" || strings.HasPrefix(t.Name, "_ponder_go_") {
			return fail("is a reserved table name.")
		}
		if names[t.Name] {
			return fail("table name is used multiple times.")
		}
		names[t.Name] = true
		cols := map[string]Column{}
		primary := 0
		for _, c := range t.Columns {
			if !Identifier(c.Name) {
				return fail("invalid column name.")
			}
			if _, ok := cols[c.Name]; ok {
				return fail("column name is used multiple times.")
			}
			cols[c.Name] = c
			if c.Name == "operation_id" || c.Name == "operation" || c.Name == "checkpoint" {
				return fail("has a reserved column name.")
			}
			if c.Primary {
				primary++
			}
			if strings.Contains(strings.ToLower(c.Type), "serial") {
				return fail("serial columns are unsupported.")
			}
			if c.Unique {
				return fail("unique constraints are unsupported.")
			}
			if c.Generated || c.Identity {
				return fail("generated columns are unsupported.")
			}
			if c.RawDefault {
				return fail("default columns with raw sql are unsupported.")
			}
			if _, ok := c.Default.(RawSQL); ok {
				return fail("default columns with raw sql are unsupported.")
			}
			for _, fn := range []func() any{c.DefaultFn, c.OnUpdateFn} {
				if fn != nil {
					if _, ok := fn().(RawSQL); ok {
						return fail("default columns with raw sql are unsupported.")
					}
				}
			}
			if _, e := s.SQLType(c.Type, ""); e != nil {
				return e
			}
		}
		if primary > 1 || primary > 0 && len(t.PrimaryKey) > 0 {
			return fail("has multiple primary keys.")
		}
		if len(t.Keys()) == 0 {
			return fail("has no primary key. Declare one with .Primary.")
		}
		for _, key := range t.Keys() {
			if _, ok := cols[key]; !ok {
				return fail("primary key references missing column.")
			}
		}
		hasReferences := t.ForeignKeys
		for _, c := range t.Columns {
			hasReferences = hasReferences || c.References != ""
		}
		if hasReferences {
			return fail("foreign key constraints are unsupported.")
		}
		if t.Checks || len(t.CheckConstraints) > 0 {
			return fail("check constraints are unsupported.")
		}
		if t.Unique {
			return fail("unique constraints are unsupported.")
		}
		if order == Isolated {
			c, ok := cols["chain_id"]
			if !ok || c.Property != "chainId" {
				return fail("does not have required 'chainId' column.")
			}
			if c.Type != "integer" && c.Type != "bigint" && c.Type != "int8" && c.Type != "numeric" {
				return fail("chainId column must be an integer or numeric.")
			}
			found := false
			for _, key := range t.Keys() {
				found = found || key == "chain_id"
			}
			if !found {
				return fail("chain_id column is required to be in the primary key when ordering is 'isolated'.")
			}
		}
		for _, i := range t.Indexes {
			if !Identifier(i.Name) || indexes[i.Name] {
				return fail("index name is invalid or used multiple times.")
			}
			indexes[i.Name] = true
			for _, col := range i.Columns {
				if _, ok := cols[col]; !ok {
					return fail("index references missing column.")
				}
			}
		}
	}
	for _, seq := range s.Sequences {
		return fmt.Errorf("Schema validation failed: '%s' is a sequence and sequences are unsupported.", seq.Name)
	}
	viewNames := map[string]bool{}
	for _, v := range s.Views {
		if !Identifier(v.Name) || viewNames[v.Name] {
			return fmt.Errorf("Schema validation failed: view name '%s' is invalid or used multiple times.", v.Name)
		}
		viewNames[v.Name] = true
		if len(v.Fields) == 0 {
			return fmt.Errorf("Schema validation failed: view '%s' has no selected fields.", v.Name)
		}
		if v.Existing {
			return fmt.Errorf("Schema validation failed: view '%s' is an existing view and existing views are unsupported.", v.Name)
		}
		if v.Query == "" {
			return fmt.Errorf("Schema validation failed: view '%s' has no underlying query.", v.Name)
		}
		seen := map[string]bool{}
		for _, field := range v.Fields {
			if !Identifier(field.Name) || seen[field.Name] {
				return fmt.Errorf("invalid or duplicate view column %s.%s", v.Name, field.Name)
			}
			seen[field.Name] = true
			if field.Column == nil && !field.Aliased {
				return fmt.Errorf("Schema validation failed: view '%s.%s' is a non-column selected field.", v.Name, field.Name)
			}
			if c := field.Column; c != nil {
				if c.Unique || c.Generated || c.Identity || c.RawDefault || strings.Contains(strings.ToLower(c.Type), "serial") {
					return fmt.Errorf("unsupported view column %s.%s", v.Name, field.Name)
				}
				if _, e := s.SQLType(c.Type, ""); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func (s Schema) SQLType(t, namespace string) (string, error) {
	base := strings.TrimSuffix(t, "[]")
	for _, enum := range s.Enums {
		if base == enum.Name {
			typ := Quote(enum.Name)
			if namespace != "" {
				typ = Quote(namespace) + "." + typ
			}
			if strings.HasSuffix(t, "[]") {
				typ += "[]"
			}
			return typ, nil
		}
	}
	return SQLType(t)
}
func (s Schema) TableDDL(t Table, namespace string) string {
	parts := []string{}
	for _, c := range t.Columns {
		typ, _ := s.SQLType(c.Type, namespace)
		col := Quote(c.Name) + " " + typ
		if c.NotNull {
			col += " NOT NULL"
		}
		if c.Default != nil {
			value := fmt.Sprint(c.Default)
			if c.Type == "json" || c.Type == "jsonb" {
				raw, _ := json.Marshal(c.Default)
				value = string(raw)
			}
			col += " DEFAULT '" + strings.ReplaceAll(value, "'", "''") + "'"
		}
		parts = append(parts, col)
	}
	keys := []string{}
	for _, k := range t.Keys() {
		keys = append(keys, Quote(k))
	}
	parts = append(parts, "PRIMARY KEY ("+strings.Join(keys, ",")+")")
	return "CREATE TABLE IF NOT EXISTS " + Quote(namespace) + "." + Quote(t.Name) + " (" + strings.Join(parts, ",") + ")"
}
func (t Table) DDL(namespace string) string {
	parts := []string{}
	for _, c := range t.Columns {
		typ, _ := SQLType(c.Type)
		col := Quote(c.Name) + " " + typ
		if c.NotNull {
			col += " NOT NULL"
		}
		parts = append(parts, col)
	}
	keys := []string{}
	for _, k := range t.Keys() {
		keys = append(keys, Quote(k))
	}
	parts = append(parts, "PRIMARY KEY ("+strings.Join(keys, ",")+")")
	return "CREATE TABLE IF NOT EXISTS " + Quote(namespace) + "." + Quote(t.Name) + " (" + strings.Join(parts, ",") + ")"
}
