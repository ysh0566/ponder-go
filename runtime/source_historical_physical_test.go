package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

var historicalLiveRelations = map[string]string{"intervals": "intervals", "factories": "factories", "factoryAddresses": "factory_addresses", "blocks": "blocks", "logs": "logs", "transactions": "transactions", "transactionReceipts": "transaction_receipts", "traces": "traces", "rpcRequestResults": "rpc_request_results"}

// These are the actual pinned schema's bigint/numeric fromDriver domains.
var historicalLiveBigints = map[string]string{
	"blocks":               " number timestamp gasUsed gasLimit baseFeePerGas size difficulty totalDifficulty ",
	"transactions":         " blockNumber value v gas gasPrice maxFeePerGas maxPriorityFeePerGas ",
	"transaction_receipts": " blockNumber gasUsed cumulativeGasUsed effectiveGasPrice ",
	"logs":                 " blockNumber ", "traces": " blockNumber value gas gasUsed ",
	"factory_addresses": " blockNumber ", "rpc_request_results": " blockNumber ",
}

func historicalLiveCamel(name string) string {
	parts := strings.Split(name, "_")
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}
func historicalLiveComparePhysical(ctx context.Context, backend store.Backend, cache *store.ProtocolCache, expected map[string]any) error {
	if cache == nil {
		return fmt.Errorf("native historical cache missing")
	}
	if len(expected) != len(historicalLiveRelations) {
		return fmt.Errorf("all nine physical relations required")
	}
	for name, table := range historicalLiveRelations {
		query := "SELECT * FROM " + schema.Quote(cache.Namespace()) + "." + schema.Quote(table)
		if table == "traces" {
			query = "SELECT *,array_to_json(trace_address)::text AS trace_address FROM " + schema.Quote(cache.Namespace()) + "." + schema.Quote(table)
		}
		result, err := backend.Query(ctx, query)
		if err != nil {
			return err
		}
		actual := []any{}
		for _, row := range result.Rows {
			converted := map[string]any{}
			for snake, value := range row {
				column := historicalLiveCamel(snake)
				if value != nil && (column == "chainId" || strings.Contains(historicalLiveBigints[table], " "+column+" ")) {
					number, ok := new(big.Int).SetString(fmt.Sprint(value), 10)
					if !ok {
						return fmt.Errorf("invalid physical bigint %s.%s", table, column)
					}
					value = map[string]any{"$bigint": number.String()}
				}
				if column == "traceAddress" && value != nil {
					var array []any
					if err = json.Unmarshal([]byte(fmt.Sprint(value)), &array); err != nil {
						return err
					}
					value = array
				}
				converted[column] = value
			}
			actual = append(actual, converted)
		}
		wanted, ok := expected[name].([]any)
		if !ok || len(actual) != len(wanted) {
			return fmt.Errorf("physical %s row count: native%d source%v", name, len(actual), expected[name])
		}
		canonical := func(rows []any) ([]string, error) {
			result := []string{}
			for _, row := range rows {
				raw, err := json.Marshal(row)
				if err != nil {
					return nil, err
				}
				result = append(result, string(raw))
			}
			sort.Strings(result)
			return result, nil
		}
		left, err := canonical(actual)
		if err != nil {
			return err
		}
		right, err := canonical(wanted)
		if err != nil {
			return err
		}
		for i := range left {
			if left[i] != right[i] {
				return fmt.Errorf("physical %s row%d: native%s source%s", name, i, left[i], right[i])
			}
		}
	}
	return nil
}
