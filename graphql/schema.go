// Package graphql exposes the indexing database as a read-only GraphQL service.
package graphql

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	gql "github.com/ysh0566/ponder-go/third_party/graphqlgo"
	"github.com/ysh0566/ponder-go/third_party/graphqlgo/language/ast"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type Options struct {
	Relations           []Relation
	Namespace           string
	MaxOperationTokens  int
	MaxOperationDepth   int
	MaxOperationAliases int
}

type Server struct {
	database  *store.Store
	namespace string
	schema    gql.Schema
	options   Options
}

// graphql-go lazily initializes shared introspection types during schema construction.
var buildSchemaMu sync.Mutex

// New builds a handler that can be mounted at any path. Namespace defaults to the store's writer schema.
func New(database *store.Store, opts Options) (*Server, error) {
	if database == nil {
		return nil, fmt.Errorf("GraphQL requires a database")
	}
	if opts.Namespace == "" {
		opts.Namespace = database.Namespace
	}
	if !schema.Identifier(opts.Namespace) || len(opts.Namespace) > 63 {
		return nil, fmt.Errorf("invalid GraphQL namespace %q", opts.Namespace)
	}
	if opts.MaxOperationTokens < 0 || opts.MaxOperationDepth < 0 || opts.MaxOperationAliases < 0 {
		return nil, fmt.Errorf("negative GraphQL operation limit")
	}
	if opts.MaxOperationTokens == 0 {
		opts.MaxOperationTokens = 1000
	}
	if opts.MaxOperationDepth == 0 {
		opts.MaxOperationDepth = 100
	}
	if opts.MaxOperationAliases == 0 {
		opts.MaxOperationAliases = 30
	}
	s := &Server{database: database, namespace: opts.Namespace, options: opts}
	meta := gql.NewObject(gql.ObjectConfig{Name: "Meta", Fields: gql.Fields{"status": &gql.Field{Type: jsonScalar}}})
	fields := gql.Fields{"_meta": &gql.Field{Type: meta, Resolve: s.metadata}}
	enums := map[string]*gql.Enum{}
	for _, e := range database.Schema.Enums {
		values := gql.EnumValueConfigMap{}
		for _, value := range e.Values {
			values[value] = &gql.EnumValueConfig{Value: value}
		}
		name := e.ExportName
		if name == "" {
			name = e.Name
		}
		enums[e.Name] = gql.NewEnum(gql.EnumConfig{Name: name, Values: values})
	}
	collections := map[string]*collection{}
	pageInfo := newPageInfo(false)
	for _, table := range database.Schema.Tables {
		name := table.ExportName
		if name == "" {
			name = table.Name
		}
		if !schema.Identifier(name) {
			return nil, fmt.Errorf("invalid GraphQL table name %q", name)
		}
		key := strings.ToLower(name[:1]) + name[1:]
		if fields[key] != nil || fields[key+"s"] != nil {
			return nil, fmt.Errorf("duplicate GraphQL field %q", key)
		}
		object, err := entityObject(name, table.Columns, enums)
		if err != nil {
			return nil, err
		}
		field, err := s.entity(table, object, enums)
		if err != nil {
			return nil, err
		}
		fields[key] = field
		page, err := s.tablePage(table, object, pageInfo, enums, false)
		if err != nil {
			return nil, err
		}
		fields[key+"s"] = page.field
		collections[table.Name] = page
	}
	viewInfo := newPageInfo(true)
	for _, view := range database.Schema.Views {
		name := view.ExportName
		if name == "" {
			name = view.Name
		}
		if !schema.Identifier(name) {
			return nil, fmt.Errorf("invalid GraphQL view name %q", name)
		}
		key := strings.ToLower(name[:1]) + name[1:] + "s"
		if fields[key] != nil {
			return nil, fmt.Errorf("duplicate GraphQL field %q", key)
		}
		table := schema.Table{Name: view.Name, Columns: viewColumns(view)}
		object, err := entityObject(name, table.Columns, enums)
		if err != nil {
			return nil, err
		}
		page, err := s.tablePage(table, object, viewInfo, enums, true)
		if err != nil {
			return nil, err
		}
		fields[key] = page.field
	}
	if err := s.addRelations(collections, opts.Relations); err != nil {
		return nil, err
	}
	buildSchemaMu.Lock()
	defer buildSchemaMu.Unlock()
	var err error
	s.schema, err = gql.NewSchema(gql.SchemaConfig{Query: gql.NewObject(gql.ObjectConfig{Name: "Query", Fields: fields})})
	if err != nil {
		return nil, err
	}
	// Both enum lookup maps must be initialized before concurrent requests use them.
	for _, typ := range s.schema.TypeMap() {
		if enum, ok := typ.(*gql.Enum); ok && len(enum.Values()) > 0 {
			first := enum.Values()[0]
			enum.ParseValue(first.Name)
			enum.Serialize(first.Value)
		}
	}
	return s, nil
}

func decimalScalar(name string, parse func(string) any) *gql.Scalar {
	return gql.NewScalar(gql.ScalarConfig{Name: name, Serialize: func(v any) any { return fmt.Sprint(v) }, ParseValue: func(v any) any { return parse(fmt.Sprint(v)) }, ParseLiteral: func(v ast.Value) any {
		if text, ok := v.(*ast.StringValue); ok {
			return parse(text.Value)
		}
		return nil
	}})
}

var bigIntScalar = decimalScalar("BigInt", func(s string) any {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil
	}
	return v.String()
})
var numericScalar = decimalScalar("Numeric", func(s string) any { return s })
var jsonScalar = gql.NewScalar(gql.ScalarConfig{Name: "JSON", Serialize: func(v any) any { return v }})

func columnType(c schema.Column, enums map[string]*gql.Enum) (gql.Type, error) {
	if base, ok := strings.CutSuffix(c.Type, "[]"); ok {
		c.Type = base
		core, err := columnType(c, enums)
		if err != nil {
			return nil, err
		}
		return gql.NewList(gql.NewNonNull(core)), nil
	}
	if e := enums[c.Type]; e != nil {
		return e, nil
	}
	switch c.Type {
	case "text", "hex", "address", "varchar", "char", "uuid", "timestamp", "date":
		return gql.String, nil
	case "bigint":
		return bigIntScalar, nil
	case "int8":
		if c.Mode == "number" {
			return gql.Int, nil
		}
		return bigIntScalar, nil
	case "numeric":
		return numericScalar, nil
	case "integer":
		return gql.Int, nil
	case "doublePrecision", "real", "smallint":
		return gql.Float, nil
	case "boolean":
		return gql.Boolean, nil
	case "json", "jsonb":
		return jsonScalar, nil
	case "bytes":
		return gql.NewList(gql.NewNonNull(gql.Int)), nil
	default:
		return nil, fmt.Errorf("unsupported GraphQL column type %q", c.Type)
	}
}

func property(c schema.Column) string {
	if c.Property != "" {
		return c.Property
	}
	return c.Name
}

func entityObject(name string, cols []schema.Column, enums map[string]*gql.Enum) (*gql.Object, error) {
	fields := gql.Fields{}
	for _, col := range cols {
		key := property(col)
		if fields[key] != nil {
			return nil, fmt.Errorf("duplicate GraphQL field %s.%s", name, key)
		}
		typ, err := columnType(col, enums)
		if err != nil {
			return nil, err
		}
		if col.NotNull || col.Primary {
			typ = gql.NewNonNull(typ)
		}
		fields[key] = &gql.Field{Type: typ, Resolve: func(p gql.ResolveParams) (any, error) {
			return columnValue(col, p.Source.(map[string]any)[col.Name])
		}}
	}
	return gql.NewObject(gql.ObjectConfig{Name: name, Fields: fields}), nil
}

func viewColumns(view schema.View) []schema.Column {
	out := make([]schema.Column, len(view.Fields))
	for i, f := range view.Fields {
		col := schema.Column{Type: "json"}
		if f.Column != nil {
			col = *f.Column
			col.Primary = false
		}
		col.Name = f.Name
		if f.Property != "" {
			col.Property = f.Property
		}
		out[i] = col
	}
	return out
}

func (s *Server) entity(table schema.Table, entity *gql.Object, enums map[string]*gql.Enum) (*gql.Field, error) {
	name := entity.Name()
	columns := map[string]schema.Column{}
	for _, col := range table.Columns {
		columns[col.Name] = col
	}
	args := gql.FieldConfigArgument{}
	keys := table.PrimaryKeyColumns()
	if len(keys) == 0 {
		return nil, fmt.Errorf("table %s requires a primary key", name)
	}
	for _, key := range keys {
		col, ok := columns[key.SQL]
		if !ok {
			return nil, fmt.Errorf("missing primary key column %q", key.SQL)
		}
		core, err := columnType(col, enums)
		if err != nil {
			return nil, err
		}
		// Drizzle exposes table-level key descriptors as strings for enum columns.
		// A key declared directly on the enum column keeps its enum type.
		if len(table.PrimaryKey) > 0 && enums[col.Type] != nil {
			core = gql.String
		}
		args[key.JS] = &gql.ArgumentConfig{Type: gql.NewNonNull(core)}
	}
	keyCols := make([]schema.Column, len(keys))
	for i, key := range keys {
		keyCols[i] = columns[key.SQL]
	}
	return &gql.Field{Type: entity, Args: args, Resolve: func(p gql.ResolveParams) (any, error) {
		vals := make([]any, len(keys))
		for i, key := range keys {
			vals[i] = p.Args[key.JS]
		}
		return s.loadOne(p.Context, table, keyCols, vals)
	}}, nil
}

func canonicalHex(s string) (string, error) {
	value, ok := strings.CutPrefix(strings.ToLower(s), "0x")
	if !ok {
		return "", fmt.Errorf("invalid hex value")
	}
	if len(value)%2 != 0 {
		value = "0" + value
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", err
	}
	return "0x" + value, nil
}

func columnValue(c schema.Column, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if typ, ok := strings.CutSuffix(c.Type, "[]"); ok {
		items, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("invalid array value for %s", c.Name)
		}
		c.Type = typ
		out := make([]any, len(items))
		for i, item := range items {
			value, err := columnValue(c, item)
			if err != nil {
				return nil, err
			}
			out[i] = value
		}
		return out, nil
	}
	switch c.Type {
	case "bytes":
		raw, err := hex.DecodeString(strings.TrimPrefix(fmt.Sprint(v), `\x`))
		if err != nil {
			return nil, err
		}
		values := make([]int, len(raw))
		for i, b := range raw {
			values[i] = int(b)
		}
		return values, nil
	case "hex", "address":
		return canonicalHex(fmt.Sprint(v))
	case "date":
		if c.Mode == "date" {
			day, err := time.Parse(time.DateOnly, fmt.Sprint(v))
			if err != nil {
				return nil, err
			}
			return strconv.FormatInt(day.UnixMilli(), 10), nil
		}
		return v, nil
	case "bigint", "numeric":
		return fmt.Sprint(v), nil
	case "int8":
		if c.Mode != "number" {
			return fmt.Sprint(v), nil
		}
		fallthrough
	case "integer", "smallint":
		return strconv.ParseInt(fmt.Sprint(v), 10, 64)
	case "doublePrecision", "real":
		return strconv.ParseFloat(fmt.Sprint(v), 64)
	default:
		return v, nil
	}
}

func (s *Server) metadata(p gql.ResolveParams) (any, error) {
	q := "SELECT chain_name, chain_id, latest_checkpoint FROM _ponder_checkpoint ORDER BY chain_id"
	if s.namespace == s.database.Namespace {
		q = "SELECT c.chain_name,c.chain_id,p.checkpoint AS latest_checkpoint FROM _ponder_go_chains c JOIN _ponder_go_progress p USING (chain_id) ORDER BY c.chain_id"
	}
	r, err := s.database.QueryReadOnlyInNamespace(p.Context, s.namespace, q)
	if err != nil {
		return nil, err
	}
	status := map[string]any{}
	for _, row := range r.Rows {
		cp, err := checkpoint.Decode(fmt.Sprint(row["latest_checkpoint"]))
		if err != nil {
			return nil, err
		}
		chain, err := strconv.ParseUint(fmt.Sprint(row["chain_id"]), 10, 64)
		if err != nil {
			return nil, err
		}
		status[fmt.Sprint(row["chain_name"])] = map[string]any{"id": chain, "block": map[string]any{"number": cp.BlockNumber, "timestamp": cp.BlockTimestamp}}
	}
	return map[string]any{"status": status}, nil
}
