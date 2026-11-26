package orm

import (
	"fmt"
	"go/format"
	"go/token"
	"strings"

	"github.com/ysh0566/ponder-go/schema"
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
func typedColumn(column schema.Column, enums map[string]string) (typ, decoder, encoder string, e error) {
	if codec := column.GoCodec; codec != nil {
		typ, binding := codec.Type, codec.Name
		if strings.HasSuffix(column.Type, "[]") {
			typ, binding = "[]orm.Nullable["+typ+"]", "orm.ArrayColumnCodec("+binding+")"
		}
		return typ, binding + ".Read", binding + ".Snapshot(%s)", nil
	}
	if element, ok := strings.CutSuffix(column.Type, "[]"); ok {
		column.Type = element
		typ, decode, encode, err := typedColumn(column, enums)
		if err != nil {
			return "", "", "", err
		}
		return "[]orm.Nullable[" + typ + "]", "orm.ArrayDecoder(" + decode + ")", "orm.EncodeArray(%s, func(v " + typ + ") any { return " + fmt.Sprintf(encode, "v") + " })", nil
	}
	if name, ok := enums[column.Type]; ok {
		return name, "orm.EnumDecoder(Parse" + name + ")", "%s.String()", nil
	}
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
	case "text", "varchar", "char", "hex", "address", "numeric", "uuid", "date", "cidr", "inet", "interval", "macaddr", "macaddr8", "time":
		return "string", "orm.DecodeString", "%s", nil
	case "boolean":
		return "bool", "orm.DecodeBool", "%s", nil
	case "real", "doublePrecision":
		return "float64", "orm.DecodeFloat64", "%s", nil
	case "bytes":
		return "[]byte", "orm.DecodeBytes", "orm.CloneBytes(%s)", nil
	case "json", "jsonb":
		return "json.RawMessage", "orm.DecodeJSON", "orm.CloneBytes(%s)", nil
	case "point":
		return "[2]float64", "orm.DecodePoint", "%s", nil
	case "line":
		return "[3]float64", "orm.DecodeLine", "%s", nil
	case "timestamp":
		return "time.Time", "orm.DecodeTime", "orm.EncodeTime(%s)", nil
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

// GenerateModels emits typed model, key, insert and update APIs.
// Constructors encode required argument presence; Go zero values remain legal
// Go syntax and are checked at runtime before a dynamic store call.
func GenerateModels(packageName string, model schema.Schema) ([]byte, error) {
	return generateModels(packageName, model, false)
}

// GenerateCallbackDatabase includes a schema-specific database that binds all
// generated tables to the transaction-scoped indexing context.
func GenerateCallbackDatabase(packageName string, model schema.Schema) ([]byte, error) {
	return generateModels(packageName, model, true)
}

func generateModels(packageName string, model schema.Schema, callbackDB bool) ([]byte, error) {
	if !token.IsIdentifier(packageName) || token.Lookup(packageName).IsKeyword() {
		return nil, fmt.Errorf("invalid Go package name")
	}
	if e := model.Validate(schema.Multichain); e != nil {
		return nil, e
	}
	var body strings.Builder
	imports := map[string]bool{"encoding/json": true, "fmt": true, "github.com/ysh0566/ponder-go/orm": true, "github.com/ysh0566/ponder-go/store": true}
	used := map[string]bool{}
	enumNames, err := generateEnums(&body, model, used)
	if err != nil {
		return nil, err
	}
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
		hasCodec := false
		fields := map[string]bool{}
		for _, column := range table.Columns {
			hasCodec = hasCodec || column.GoCodec != nil
			property := column.Property
			if property == "" {
				property = column.Name
			}
			field := modelGoName(property)
			if !token.IsIdentifier(field) || fields[field] {
				return nil, fmt.Errorf("colliding or invalid generated field %s.%s", name, field)
			}
			fields[field] = true
			typ, decoder, encoder, e := typedColumn(column, enumNames)
			if e != nil {
				return nil, e
			}
			if strings.Contains(typ, "big.Int") {
				imports["math/big"] = true
			}
			if strings.Contains(typ, "time.Time") {
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
			value := "row." + column.field
			if column.column.GoCodec == nil {
				value = column.encoded(value)
			}
			fmt.Fprintf(&body, "%q:%s,", column.property, value)
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
			if hasCodec {
				fmt.Fprintf(&body, "return orm.ResolveColumnValues(%q,input.values) },\n", table.Name)
			} else {
				body.WriteString("return orm.CopyValues(input.values),nil },\n")
			}
		}
		fmt.Fprintf(&body, "func(input %sUpdate)(store.Row,error){ row:=store.Row{};", name)
		for _, column := range columns {
			if !column.key {
				fmt.Fprintf(&body, "if input.%s.Changed() { row[%q]=%s };", column.field, column.property, column.encoded("input."+column.field+".Value()"))
			}
		}
		if hasCodec {
			fmt.Fprintf(&body, "return orm.ResolveColumnValues(%q,row) },\n", table.Name)
		} else {
			body.WriteString("return row,nil },\n")
		}
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
		writeJoinColumns(&body, name, columns, used)
	}
	if len(model.Tables) == 0 {
		return nil, fmt.Errorf("typed model codegen requires an onchain table")
	}
	if callbackDB {
		if names["CallbackDatabase"] || names["BindCallbackDatabase"] {
			return nil, fmt.Errorf("model name collides with callback database")
		}
		imports["github.com/ysh0566/ponder-go/indexing"] = true
		body.WriteString("type CallbackDatabase struct{\n")
		for _, table := range model.Tables {
			name := modelGoName(table.Name)
			fmt.Fprintf(&body, "%s orm.Table[%sKey,%sInsert,%sUpdate,%sRow]\n", name, name, name, name, name)
		}
		body.WriteString("}\nfunc BindCallbackDatabase(db *indexing.Database)CallbackDatabase{return CallbackDatabase{\n")
		for _, table := range model.Tables {
			name := modelGoName(table.Name)
			fmt.Fprintf(&body, "%s:%s.BindExecutor(db),\n", name, name)
		}
		body.WriteString("}}\n")
	}
	var source strings.Builder
	fmt.Fprintf(&source, "// Code generated by ponder-go typed codegen; DO NOT EDIT.\npackage %s\nimport (\n", packageName)
	for _, importName := range []string{"encoding/json", "fmt", "math/big", "time", "github.com/ysh0566/ponder-go/indexing", "github.com/ysh0566/ponder-go/orm", "github.com/ysh0566/ponder-go/store"} {
		if imports[importName] {
			fmt.Fprintf(&source, "%q\n", importName)
		}
	}
	source.WriteString(")\n")
	source.WriteString(body.String())
	return format.Source([]byte(source.String()))
}

// Allocate column bindings after the existing table, enum and application codec
// symbols so regenerating an existing project preserves those public names.
func writeJoinColumns(body *strings.Builder, model string, columns []generatedColumn, used map[string]bool) {
	name := model + "Columns"
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%sColumns%d", model, n)
	}
	used[name] = true
	fmt.Fprintf(body, "var %s = struct {\n", name)
	for _, c := range columns {
		fmt.Fprintf(body, "%s orm.Column[%sRow,%s]\n", c.field, model, c.typ)
	}
	body.WriteString("}{\n")
	for _, c := range columns {
		fmt.Fprintf(body, "%s: orm.ColumnOf[%s](%s,%q),\n", c.field, c.typ, model, c.column.Name)
	}
	body.WriteString("}\n")
}
