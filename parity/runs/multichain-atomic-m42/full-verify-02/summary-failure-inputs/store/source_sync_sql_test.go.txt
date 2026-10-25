package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
)

var sourceLiveRelations = map[string]string{"intervals": "intervals", "factories": "factories", "factoryAddresses": "factory_addresses", "blocks": "blocks", "logs": "logs", "transactions": "transactions", "transactionReceipts": "transaction_receipts", "traces": "traces", "rpcRequestResults": "rpc_request_results"}
var sourceLiveMutation = regexp.MustCompile(`(?is)^\s*(?:insert\s+into|update|delete\s+from|truncate(?:\s+table)?)\s+"([^"]+)"\."([^"]+)"`)

// Only genuine pinned-test setup DML is accepted here. Source production SQL
// runs through the native implementation instead of being replayed.
func sourceLiveSeedSQL(ctx context.Context, c *ProtocolCache, sql string, args []any) error {
	match := sourceLiveMutation.FindStringSubmatch(sql)
	if match == nil || match[1] != "ponder_rpc_cache_1" {
		return fmt.Errorf("setup DML must target source sync schema")
	}
	allowed := false
	for _, table := range sourceLiveRelations {
		allowed = allowed || table == match[2]
	}
	if !allowed {
		return fmt.Errorf("setup DML relation is outside owned source tables")
	}
	// Rewrite identifier tokens only; preserve SQL string literal contents.
	var out strings.Builder
	for i := 0; i < len(sql); {
		switch sql[i] {
		case '\'':
			start := i
			i++
			closed := false
			for i < len(sql) {
				if sql[i] == '\'' {
					i++
					if i < len(sql) && sql[i] == '\'' {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return fmt.Errorf("unterminated setup SQL literal")
			}
			out.WriteString(sql[start:i])
		case '"':
			start := i
			i++
			var name strings.Builder
			closed := false
			for i < len(sql) {
				if sql[i] == '"' {
					i++
					if i < len(sql) && sql[i] == '"' {
						name.WriteByte('"')
						i++
						continue
					}
					closed = true
					break
				}
				name.WriteByte(sql[i])
				i++
			}
			if !closed {
				return fmt.Errorf("unterminated setup SQL identifier")
			}
			if name.String() == "ponder_rpc_cache_1" {
				out.WriteString(schema.Quote(c.namespace))
			} else {
				out.WriteString(sql[start:i])
			}
		case ';':
			return fmt.Errorf("multiple setup SQL statements are forbidden")
		default:
			out.WriteByte(sql[i])
			i++
		}
	}
	// Every qualified relation must stay in this owned sync schema or use a
	// source table alias/excluded pseudo-relation. Setup cannot reach business DBs.
	qualified := regexp.MustCompile(`"([^"]+)"\s*\.\s*"([^"]+)"`).FindAllStringSubmatch(out.String(), -1)
	for _, q := range qualified {
		if q[1] == c.namespace || q[1] == "excluded" {
			continue
		}
		alias := false
		for _, table := range sourceLiveRelations {
			alias = alias || q[1] == table
		}
		if !alias {
			return fmt.Errorf("setup SQL has nonowned qualifier %q", q[1])
		}
	}
	return c.transaction(ctx, func(tx Transaction) error { return tx.Exec(ctx, out.String(), args...) })
}
func sourceLiveCamel(name string) string {
	parts := strings.Split(name, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
func sourceLiveBigintColumn(table, column string) bool {
	if column == "chainId" {
		return true
	}
	if table == "intervals" || table == "factories" {
		return false
	}
	if table == "factory_addresses" || table == "rpc_request_results" {
		return column == "blockNumber"
	}
	spec, ok := sourceProtocolSpec(table, column)
	return ok && strings.Contains(spec, "#") && column != "transactionIndex" && column != "logIndex" && !(table == "transactions" && column == "nonce")
}
func sourceLivePhysical(ctx context.Context, c *ProtocolCache) (map[string]any, error) {
	out := map[string]any{}
	for name, table := range sourceLiveRelations {
		sql := "SELECT * FROM " + c.table(table)
		if table == "traces" {
			sql = "SELECT *,array_to_json(trace_address)::text AS trace_address FROM " + c.table(table)
		}
		result, e := c.s.Backend.Query(ctx, sql)
		if e != nil {
			return nil, e
		}
		rows := []any{}
		for _, row := range result.Rows {
			converted := map[string]any{}
			for snake, value := range row {
				column := sourceLiveCamel(snake)
				if value != nil && sourceLiveBigintColumn(table, column) {
					n, ok := new(big.Int).SetString(fmt.Sprint(value), 10)
					if !ok {
						return nil, fmt.Errorf("bad physical source bigint %s.%s", table, column)
					}
					value = map[string]any{"$bigint": n.String()}
				}
				if column == "traceAddress" && value != nil {
					var values []any
					if e = json.Unmarshal([]byte(fmt.Sprint(value)), &values); e != nil {
						return nil, e
					}
					value = values
				}
				converted[column] = value
			}
			rows = append(rows, converted)
		}
		out[name] = rows
	}
	return out, nil
}
func sourceLiveCanonical(value any) (string, error) {
	// Physical SQL rowsets are unordered. JSON numbers normalize to the source
	// number domain; string/BigInt/NULL identities stay distinct.
	var normalize func(any) (any, error)
	normalize = func(v any) (any, error) {
		switch x := v.(type) {
		case json.Number:
			n, e := x.Float64()
			return n, e
		case map[string]any:
			out := map[string]any{}
			for k, v := range x {
				n, e := normalize(v)
				if e != nil {
					return nil, e
				}
				out[k] = n
			}
			return out, nil
		case []any:
			out := []any{}
			for _, v := range x {
				n, e := normalize(v)
				if e != nil {
					return nil, e
				}
				out = append(out, n)
			}
			return out, nil
		default:
			return v, nil
		}
	}
	n, e := normalize(value)
	if e != nil {
		return "", e
	}
	raw, e := json.Marshal(n)
	return string(raw), e
}
func sourceLiveComparePhysical(actual map[string]any, expected map[string]any) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("physical source table set differs")
	}
	for name, rows := range actual {
		want, exists := expected[name]
		if !exists {
			return fmt.Errorf("missing expected physical table %s", name)
		}
		a, ok := rows.([]any)
		b, ok2 := want.([]any)
		if !ok || !ok2 || len(a) != len(b) {
			return fmt.Errorf("physical row count differs for %s: native%d source%d", name, len(a), len(b))
		}
		left, right := []string{}, []string{}
		for _, v := range a {
			s, e := sourceLiveCanonical(v)
			if e != nil {
				return e
			}
			left = append(left, s)
		}
		for _, v := range b {
			s, e := sourceLiveCanonical(v)
			if e != nil {
				return e
			}
			right = append(right, s)
		}
		sort.Strings(left)
		sort.Strings(right)
		for i := range left {
			if left[i] != right[i] {
				return fmt.Errorf("physical %s row%d differs: native=%s source=%s", name, i, left[i], right[i])
			}
		}
	}
	return nil
}
func sourceLiveSpan(value any) (SourceInterval, error) {
	list, e := sourceLiveList(value)
	if e != nil || len(list) != 2 {
		return SourceInterval{}, fmt.Errorf("invalid source interval tuple")
	}
	a, e := sourceLiveNumber(list[0])
	if e != nil {
		return SourceInterval{}, e
	}
	b, e := sourceLiveNumber(list[1])
	return SourceInterval{a, b}, e
}
func sourceLiveOptionalBlock(value any) (*uint64, error) {
	if value == nil || filter.IsUndefined(value) {
		return nil, nil
	}
	n, e := sourceLiveUint(value)
	return &n, e
}
func sourceLiveString(value any) (string, error) {
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("source value must be a string: %T", value)
	}
	return s, nil
}
func sourceLiveInt(value any) (int, error) {
	n, e := sourceLiveNumber(value)
	if e != nil {
		return 0, e
	}
	out, err := strconv.Atoi(strconv.FormatFloat(n, 'f', -1, 64))
	return out, err
}
