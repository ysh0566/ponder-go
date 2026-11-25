package schema

import "strings"

// SourceColumn binds a source property to a physical column. A supplied SQL
// name is preserved. Omitted SQL names use Drizzle's snake_case word boundaries.
func SourceColumn(property string, column Column) Column {
	column.Property = property
	if column.Name == "" {
		column.Name = sourceSnakeCase(property)
	}
	return column
}

func sourceSnakeCase(s string) string {
	s = strings.NewReplacer("'", "", "’", "").Replace(s)
	lower := func(b byte) bool { return b >= 'a' && b <= 'z' }
	upper := func(b byte) bool { return b >= 'A' && b <= 'Z' }
	word := func(b byte) bool { return lower(b) || b >= '0' && b <= '9' }
	var words []string
	for i := 0; i < len(s); {
		start := i
		switch {
		case word(s[i]):
			for i < len(s) && word(s[i]) {
				i++
			}
		case upper(s[i]):
			for i < len(s) && upper(s[i]) {
				i++
			}
			if i < len(s) && lower(s[i]) {
				if i-start > 1 {
					i--
				} else {
					for i < len(s) && word(s[i]) {
						i++
					}
				}
			}
		default:
			i++
			continue
		}
		words = append(words, strings.ToLower(s[start:i]))
	}
	return strings.Join(words, "_")
}

type PrimaryKeyColumn struct {
	JS  string `json:"js"`
	SQL string `json:"sql"`
}

// PrimaryKeyColumns returns property and SQL names in primary-key declaration order.
func (t Table) PrimaryKeyColumns() []PrimaryKeyColumn {
	keys := t.Keys()
	out := make([]PrimaryKeyColumn, 0, len(keys))
	for _, name := range keys {
		property := name
		for _, column := range t.Columns {
			if column.Name == name {
				if column.Property != "" {
					property = column.Property
				}
				break
			}
		}
		out = append(out, PrimaryKeyColumn{JS: property, SQL: name})
	}
	return out
}
