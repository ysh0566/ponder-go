// Package schema describes native Go onchain tables and validates Ponder restrictions.
package schema

import (
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
	Name       string `json:"name"`
	Property   string `json:"property,omitempty"`
	Type       string `json:"type"`
	Primary    bool   `json:"primary,omitempty"`
	NotNull    bool   `json:"notNull,omitempty"`
	Unique     bool   `json:"unique,omitempty"`
	Generated  bool   `json:"generated,omitempty"`
	Identity   bool   `json:"identity,omitempty"`
	Default    any    `json:"default,omitempty"`
	RawDefault bool   `json:"rawDefault,omitempty"`
}
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}
type Table struct {
	Name        string   `json:"name"`
	Columns     []Column `json:"columns"`
	PrimaryKey  []string `json:"primaryKey,omitempty"`
	Indexes     []Index  `json:"indexes,omitempty"`
	ForeignKeys bool     `json:"foreignKeys,omitempty"`
	Checks      bool     `json:"checks,omitempty"`
	Unique      bool     `json:"unique,omitempty"`
}
type Schema struct {
	Tables []Table `json:"tables"`
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
	for _, t := range s.Tables {
		fail := func(msg string) error { return fmt.Errorf("Schema validation failed: '%s' %s", t.Name, msg) }
		if !Identifier(t.Name) || len(t.Name) > 45 {
			return fail("invalid table name (maximum 45 characters).")
		}
		if strings.HasPrefix(t.Name, "_ponder_") {
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
			if _, e := SQLType(c.Type); e != nil {
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
		if t.ForeignKeys {
			return fail("foreign key constraints are unsupported.")
		}
		if t.Checks {
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
	return nil
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
