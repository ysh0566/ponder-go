package store

import (
	"encoding/json"
	"strconv"
	"strings"
)

// blockNumber returns the highest block the result depends on. Decimal text
// keeps BIGINT exact through the PGlite bridge. NULL is pruned on any fork.
func (k nativeRPCKey) blockNumber() any {
	var method string
	var params []json.RawMessage
	if json.Unmarshal(k.method, &method) != nil || json.Unmarshal(k.params, &params) != nil {
		return nil
	}
	if method == "eth_getLogs" && len(params) == 1 {
		var filter map[string]json.RawMessage
		if json.Unmarshal(params[0], &filter) != nil || filter["blockHash"] != nil {
			return nil
		}
		from, okFrom := rpcBlockQuantity(filter["fromBlock"])
		to, okTo := rpcBlockQuantity(filter["toBlock"])
		if okFrom && okTo && from <= to {
			return strconv.FormatUint(to, 10)
		}
		return nil
	}
	index := rpcBlockParamIndex(method)
	if index < 0 || index >= len(params) {
		return nil
	}
	selector := params[index]
	// EIP-1898 permits a blockNumber object for these state reads.
	switch method {
	case "eth_getBalance", "eth_getCode", "eth_getTransactionCount", "eth_getStorageAt", "eth_call", "eth_getProof":
		if len(selector) > 0 && selector[0] == '{' {
			var ref map[string]json.RawMessage
			if json.Unmarshal(selector, &ref) != nil || len(ref) != 1 {
				return nil
			}
			selector = ref["blockNumber"]
		}
	}
	if n, ok := rpcBlockQuantity(selector); ok {
		return strconv.FormatUint(n, 10)
	}
	return nil
}

func rpcBlockQuantity(raw json.RawMessage) (uint64, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil || !strings.HasPrefix(s, "0x") || (len(s) > 3 && s[2] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(s[2:], 16, 63)
	return n, err == nil
}
