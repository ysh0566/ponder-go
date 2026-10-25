package orm

import (
	"fmt"
	"go/format"
	"go/token"
	"strings"

	"ponder.local/ponder-go/schema"
)

type generatedColumn struct {
	column                        schema.Column
	property, field, typ, decoder string
	nullable, key, required       bool
	encode                        string
}

func modelGoName(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' })
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
func typedColumn(column schema.Column, enums []schema.Enum) (typ, decoder, encoder string, e error) {
	switch column.Type {
	case "bigint":
		return "big.Int", "orm.DecodeBigInt", "orm.EncodeBigInt(%s)", nil
	case "int8":
		if column.Mode == "bigint" {
			return "big.Int", "orm.DecodeBigInt", "orm.EncodeBigInt(%s)", nil
		}
		fallthrough
	case "integer", "smallint":
		return "int64", "orm.DecodeInt64", "%s", nil
	case "text", "hex", "address", "numeric", "uuid", "date":
		return "string", "orm.DecodeString", "%s", nil
	case "boolean":
		return "bool", "orm.DecodeBool", "%s", nil
	case "real", "doublePrecision":
		return "float64", "orm.DecodeFloat64", "%s", nil
	case "timestamp":
		return "time.Time", "orm.DecodeTime", "orm.EncodeTime(%s)", nil
	}
	for _, enum := range enums {
		if enum.Name == column.Type {
			return "string", "orm.DecodeString", "%s", nil
		}
	}
	return "", "", "", fmt.Errorf("typed model codegen for %s is not implemented", column.Type)
}
func (c generatedColumn) fieldType() string {
	if c.nullable {
		return "orm.Nullable[" + c.typ + "]"
	}
	return c.typ
}
func (c generatedColumn) encoded(value string) string {
	if c.nullable {
		return fmt.Sprintf("func() any { if %s.Valid() { return %s }; return nil }()", value, fmt.Sprintf(c.encode, value+".Value()"))
	}
	return fmt.Sprintf(c.encode, value)
}

// GenerateModels emits scalar typed model/key/insert/update APIs. Unsupported
// container/custom column codecs are rejected explicitly, never replaced by any.
// Constructors encode required argument presence; Go zero values remain legal
// Go syntax and are checked at runtime before a dynamic store call.
func GenerateModels(packageName string, model schema.Schema) ([]byte, error) {
	if !token.IsIdentifier(packageName) || token.Lookup(packageName).IsKeyword() {
		return nil, fmt.Errorf("invalid Go package name")
	}
	if e := model.Validate(schema.Multichain); e != nil {
		return nil, e
	}
	var body strings.Builder
	imports := map[string]bool{"encoding/json": true, "fmt": true, "ponder.local/ponder-go/orm": true, "ponder.local/ponder-go/store": true}
	names := map[string]bool{}
	for _, table := range model.Tables {
		name := modelGoName(table.Name)
		if !token.IsIdentifier(name) || names[name] {
			return nil, fmt.Errorf("colliding or invalid generated model name %s", name)
		}
		names[name] = true
		keys := map[string]bool{}
		for _, key := range table.Keys() {
			keys[key] = true
		}
		columns := []generatedColumn{}
		fields := map[string]bool{}
		for _, column := range table.Columns {
			property := column.Property
			if property == "" {
				property = column.Name
			}
			field := modelGoName(property)
			if !token.IsIdentifier(field) || fields[field] {
				return nil, fmt.Errorf("colliding or invalid generated field %s.%s", name, field)
			}
			fields[field] = true
			typ, decoder, encoder, e := typedColumn(column, model.Enums)
			if e != nil {
				return nil, e
			}
			if typ == "big.Int" {
				imports["math/big"] = true
			}
			if typ == "time.Time" {
				imports["time"] = true
			}
			columns = append(columns, generatedColumn{column: column, property: property, field: field, typ: typ, decoder: decoder, encode: encoder, key: keys[column.Name], nullable: !column.NotNull && !keys[column.Name], required: keys[column.Name] || (column.NotNull && !column.HasDefault && column.Default == nil && column.DefaultFn == nil && column.OnUpdateFn == nil)})
		}
		fmt.Fprintf(&body, "type %sRow struct {\n", name)
		for _, column := range columns {
			fmt.Fprintf(&body, "%s %s `json:%q`\n", column.field, column.fieldType(), column.property)
		}
		body.WriteString("}\n")
		fmt.Fprintf(&body, "func(row %sRow) MarshalJSON()([]byte,error){ return json.Marshal(store.Row{", name)
		for _, column := range columns {
			fmt.Fprintf(&body, "%q:%s,", column.property, column.encoded("row."+column.field))
		}
		body.WriteString("}) }\n")
		fmt.Fprintf(&body, "type %sKey struct { values store.Row }\ntype %sInsert struct { values store.Row }\n", name, name)
		for _, kind := range []string{"Key", "Insert"} {
			params, values := []string{}, []string{}
			for _, column := range columns {
				if (kind == "Key" && column.key) || (kind == "Insert" && column.required) {
					param := fmt.Sprintf("v%d", len(params))
					params = append(params, param+" "+column.fieldType())
					values = append(values, fmt.Sprintf("%q:%s", column.property, column.encoded(param)))
				}
			}
			fmt.Fprintf(&body, "func New%s%s(%s) %s%s { return %s%s{ values:store.Row{%s} } }\n", name, kind, strings.Join(params, ","), name, kind, name, kind, strings.Join(values, ","))
		}
		for _, column := range columns {
			if !column.required {
				fmt.Fprintf(&body, "func (input %sInsert) With%s(value %s) %sInsert { input.values=orm.CopyValues(input.values); input.values[%q]=%s; return input }\n", name, column.field, column.fieldType(), name, column.property, column.encoded("value"))
			}
		}
		fmt.Fprintf(&body, "type %sUpdate struct {\n", name)
		for _, column := range columns {
			if !column.key {
				fmt.Fprintf(&body, "%s orm.Change[%s]\n", column.field, column.fieldType())
			}
		}
		body.WriteString("}\n")
		fmt.Fprintf(&body, "var %s = orm.Define[%sKey,%sInsert,%sUpdate,%sRow](%q,\n", name, name, name, name, name, table.Name)
		for _, kind := range []string{"Key", "Insert"} {
			fmt.Fprintf(&body, "func(input %s%s)(store.Row,error){ if input.values==nil { return nil,fmt.Errorf(%q) };", name, kind, "use New"+name+kind+" to provide required fields")
			for _, column := range columns {
				if (kind == "Key" && column.key) || (kind == "Insert" && column.required) {
					fmt.Fprintf(&body, "if _,ok:=input.values[%q];!ok { return nil,fmt.Errorf(%q) };", column.property, "missing required "+table.Name+"."+column.property)
				}
			}
			body.WriteString("return orm.CopyValues(input.values),nil },\n")
		}
		fmt.Fprintf(&body, "func(input %sUpdate)(store.Row,error){ row:=store.Row{};", name)
		for _, column := range columns {
			if !column.key {
				fmt.Fprintf(&body, "if input.%s.Changed() { row[%q]=%s };", column.field, column.property, column.encoded("input."+column.field+".Value()"))
			}
		}
		body.WriteString("return row,nil },\n")
		fmt.Fprintf(&body, "func(row store.Row)(%sRow,error){ var out %sRow;", name, name)
		for _, column := range columns {
			if column.nullable {
				fmt.Fprintf(&body, "if row[%q]!=nil {", column.property)
			}
			fmt.Fprintf(&body, "{value,e:=%s(row[%q]);if e!=nil{return out,fmt.Errorf(%q,e)};", column.decoder, column.property, table.Name+"."+column.property+": %w")
			value := "value"
			if column.nullable {
				value = "orm.Some(value)"
			}
			fmt.Fprintf(&body, "out.%s=%s };", column.field, value)
			if column.nullable {
				body.WriteString("};")
			}
		}
		body.WriteString("return out,nil },\n)\n")
	}
	if len(model.Tables) == 0 {
		return nil, fmt.Errorf("typed model codegen requires an onchain table")
	}
	var source strings.Builder
	fmt.Fprintf(&source, "// Code generated by ponder-go typed codegen; DO NOT EDIT.\npackage %s\nimport (\n", packageName)
	for _, importName := range []string{"encoding/json", "fmt", "math/big", "time", "ponder.local/ponder-go/orm", "ponder.local/ponder-go/store"} {
		if imports[importName] {
			fmt.Fprintf(&source, "%q\n", importName)
		}
	}
	source.WriteString(")\n")
	source.WriteString(body.String())
	return format.Source([]byte(source.String()))
}
