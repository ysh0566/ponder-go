package orm

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/ysh0566/ponder-go/schema"
)

// Reserve the table API first so adding an enum never renames a table binding.
func generateEnums(body *strings.Builder, model schema.Schema) map[string]string {
	used := map[string]bool{"CallbackDatabase": true, "BindCallbackDatabase": true}
	for _, table := range model.Tables {
		name := modelGoName(table.Name)
		for _, suffix := range []string{"", "Row", "Key", "Insert", "Update"} {
			used[name+suffix] = true
		}
		used["New"+name+"Key"] = true
		used["New"+name+"Insert"] = true
	}
	names := make(map[string]string, len(model.Enums))
	for _, enum := range model.Enums {
		base := modelGoName(enum.Name)
		if base == "" || base[0] < 'A' || base[0] > 'Z' {
			base = "Enum" + base
		}
		name := base
		for i := 2; used[name] || used["Parse"+name]; i++ {
			name = fmt.Sprintf("%s%d", base, i)
		}
		names[enum.Name] = name
		used[name], used["Parse"+name] = true, true
	}
	for _, enum := range model.Enums {
		name := names[enum.Name]
		cases := make([]string, len(enum.Values))
		for i, value := range enum.Values {
			base := name + enumCaseName(value)
			cases[i] = base
			for n := 2; used[cases[i]]; n++ {
				cases[i] = fmt.Sprintf("%s%d", base, n)
			}
			used[cases[i]] = true
		}
		writeEnum(body, name, enum, cases)
	}
	return names
}

func enumCaseName(label string) string {
	parts := strings.FieldsFunc(label, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(parts) == 0 {
		return "Value"
	}
	for i, part := range parts {
		if part == strings.ToUpper(part) {
			part = strings.ToLower(part)
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, "")
}

func writeEnum(body *strings.Builder, name string, enum schema.Enum, cases []string) {
	// A distinct private field per enum also prevents conversions between enums.
	field := "index" + name
	fmt.Fprintf(body, "// %s represents the database enum %q. Its zero value represents %q.\n", name, enum.Name, enum.Values[0])
	fmt.Fprintf(body, "type %s struct { %s uint }\n", name, field)
	for i, constructor := range cases {
		fmt.Fprintf(body, "func %s() %s { return %s{%s: %d} }\n", constructor, name, name, field, i)
	}
	fmt.Fprintf(body, "func (v %s) String() string { return [...]string{", name)
	for _, value := range enum.Values {
		fmt.Fprintf(body, "%q,", value)
	}
	fmt.Fprintf(body, "}[v.%s] }\n", field)
	fmt.Fprintf(body, "func Parse%s(label string) (%s, error) { switch label {\n", name, name)
	for i, value := range enum.Values {
		fmt.Fprintf(body, "case %q: return %s(), nil\n", value, cases[i])
	}
	fmt.Fprintf(body, "}; return %s{}, fmt.Errorf(%q,label) }\n", name, "invalid "+enum.Name+" label %q")
	fmt.Fprintf(body, "func (v %s) MarshalText() ([]byte,error) { return []byte(v.String()),nil }\n", name)
	fmt.Fprintf(body, "func (v *%s) UnmarshalText(b []byte) error { next, err := Parse%s(string(b)); if err == nil { *v = next }; return err }\n", name, name)
}
