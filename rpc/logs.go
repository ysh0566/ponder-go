package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type ProviderError struct {
	Message string
	Meta    []string
}

func (e *ProviderError) Error() string { return e.Message }
func providerError(message, method string, params any) error {
	data, _ := json.MarshalIndent(map[string]any{"method": method, "params": params}, "", "  ")
	return &ProviderError{message, []string{"Please report this error to the RPC operator.", "Request: " + string(data)}}
}
func rpcQuantity(v any) (*big.Int, error) {
	s, ok := v.(string)
	if !ok || !strings.HasPrefix(s, "0x") {
		return nil, fmt.Errorf("invalid RPC quantity %v", v)
	}
	n, ok := new(big.Int).SetString(s[2:], 16)
	if !ok {
		return nil, fmt.Errorf("invalid RPC quantity %v", v)
	}
	return n, nil
}
func checkQuantity(row map[string]any, key, object, method string, params any, max *big.Int) error {
	n, e := rpcQuantity(row[key])
	if e != nil {
		return e
	}
	if n.Cmp(max) > 0 {
		return providerError(fmt.Sprintf("Invalid RPC response: '%s.%s' (%s) is larger than the maximum allowed value (%s).", object, key, n, max), method, params)
	}
	return nil
}

var pgBigintMax = new(big.Int).SetUint64(9223372036854775807)
var pgIntegerMax = big.NewInt(2147483647)

func StandardizeLogs(logs []map[string]any, params any) ([]map[string]any, error) {
	ids := map[string]bool{}
	for _, log := range logs {
		key := fmt.Sprint(log["blockNumber"]) + "_" + fmt.Sprint(log["logIndex"])
		if ids[key] {
			n, _ := rpcQuantity(log["blockNumber"])
			i, _ := rpcQuantity(log["logIndex"])
			return nil, providerError(fmt.Sprintf("Inconsistent RPC response data. The logs array contains two objects with 'blockNumber' %v (%v) and 'logIndex' %v (%v).", log["blockNumber"], n, log["logIndex"], i), "eth_getLogs", params)
		}
		ids[key] = true
		for _, key := range []string{"blockNumber", "logIndex", "blockHash", "address", "topics", "data"} {
			if _, ok := log[key]; !ok {
				return nil, providerError(fmt.Sprintf("Invalid RPC response: 'log.%s' is a required property", key), "eth_getLogs", params)
			}
		}
		if _, ok := log["transactionHash"]; !ok {
			log["transactionHash"] = "0x" + strings.Repeat("0", 64)
		}
		if _, ok := log["transactionIndex"]; !ok {
			log["transactionIndex"] = "0x0"
		}
		if _, ok := log["removed"]; !ok {
			log["removed"] = false
		}
		for _, key := range []string{"blockNumber", "transactionIndex", "logIndex"} {
			max := pgIntegerMax
			if key == "blockNumber" {
				max = pgBigintMax
			}
			if e := checkQuantity(log, key, "log", "eth_getLogs", params, max); e != nil {
				return nil, e
			}
		}
	}
	sort.SliceStable(logs, func(i, j int) bool {
		a, _ := rpcQuantity(logs[i]["blockNumber"])
		b, _ := rpcQuantity(logs[j]["blockNumber"])
		if c := a.Cmp(b); c != 0 {
			return c < 0
		}
		a, _ = rpcQuantity(logs[i]["logIndex"])
		b, _ = rpcQuantity(logs[j]["logIndex"])
		return a.Cmp(b) < 0
	})
	return logs, nil
}

// GetLogs preserves source chunk50, input immutability and numeric result order.
// The native implementation issues chunks sequentially; source uses Promise.all.
// Numeric ranges bypass the immutable RPC cache.
func (c *Client) GetLogs(ctx context.Context, input map[string]any) ([]map[string]any, error) {
	requests := []map[string]any{input}
	var addresses []any
	array := false
	switch xs := input["address"].(type) {
	case []any:
		addresses = xs
		array = true
	case []string:
		array = true
		for _, x := range xs {
			addresses = append(addresses, x)
		}
	}
	if array {
		requests = nil
		for i := 0; i < len(addresses); i += 50 {
			request := map[string]any{}
			for k, v := range input {
				request[k] = v
			}
			request["address"] = append([]any{}, addresses[i:min(i+50, len(addresses))]...)
			requests = append(requests, request)
		}
	}
	result := []map[string]any{}
	for _, request := range requests {
		var logs []map[string]any
		if e := c.Call(ctx, "eth_getLogs", []any{request}, &logs); e != nil {
			return nil, e
		}
		if logs == nil {
			return nil, fmt.Errorf("Received invalid empty eth_getLogs response.")
		}
		result = append(result, logs...)
	}
	return StandardizeLogs(result, []any{input})
}
func StandardizeTransactions(transactions []map[string]any, method string, params any) ([]map[string]any, error) {
	ids := map[string]bool{}
	for _, tx := range transactions {
		key := fmt.Sprint(tx["transactionIndex"])
		if ids[key] {
			n, _ := rpcQuantity(tx["transactionIndex"])
			return nil, providerError(fmt.Sprintf("Inconsistent RPC response data. The 'block.transactions' array contains two objects with a 'transactionIndex' of %v (%v).", tx["transactionIndex"], n), method, params)
		}
		ids[key] = true
		for _, key := range []string{"hash", "transactionIndex", "blockNumber", "blockHash", "from"} {
			if _, ok := tx[key]; !ok {
				return nil, providerError(fmt.Sprintf("Invalid RPC response: 'transaction.%s' is a required property", key), method, params)
			}
		}
		if _, ok := tx["to"]; !ok {
			tx["to"] = nil
		}
		defaults := map[string]any{"input": "0x", "value": "0x0", "nonce": "0x0", "r": "0x0", "s": "0x0", "v": "0x0", "type": "0x0", "gas": "0x0"}
		for key, value := range defaults {
			if _, ok := tx[key]; !ok {
				tx[key] = value
			}
		}
		for _, key := range []string{"blockNumber", "transactionIndex", "nonce"} {
			max := pgIntegerMax
			if key == "blockNumber" {
				max = pgBigintMax
			}
			if e := checkQuantity(tx, key, "transaction", method, params, max); e != nil {
				return nil, e
			}
		}
	}
	sort.SliceStable(transactions, func(i, j int) bool {
		a, _ := rpcQuantity(transactions[i]["transactionIndex"])
		b, _ := rpcQuantity(transactions[j]["transactionIndex"])
		return a.Cmp(b) < 0
	})
	return transactions, nil
}
