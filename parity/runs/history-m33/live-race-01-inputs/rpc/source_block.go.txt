package rpc

import (
	"context"
	"fmt"
	"strings"

	"ponder.local/ponder-go/internal/jsvalue"
)

func sourceRPCProperty(object map[string]any, key string) any {
	value, ok := object[key]
	if !ok {
		return jsvalue.Undefined
	}
	return value
}
func sourceRPCText(value any) string {
	if jsvalue.IsUndefined(value) {
		return "undefined"
	}
	if value == nil {
		return "null"
	}
	return fmt.Sprint(value)
}
func sourceRPCTransactions(block map[string]any) ([]map[string]any, error) {
	switch values := block["transactions"].(type) {
	case []map[string]any:
		return values, nil
	case []any:
		result := make([]map[string]any, 0, len(values))
		for _, value := range values {
			object, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid block transaction object")
			}
			result = append(result, object)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("invalid block transactions array")
	}
}

// StandardizeSourceBlock mutates the actual supplied map in source order.
// Block numbers use the native raw RPC quantity representation; arbitrary
// JavaScript BigInt coercions are not established by the current native adapter.
func StandardizeSourceBlock(block map[string]any, request ActionRequest) (map[string]any, error) {
	for _, key := range []string{"hash", "number", "timestamp", "logsBloom", "parentHash"} {
		if jsvalue.IsUndefined(sourceRPCProperty(block, key)) {
			return nil, providerError(fmt.Sprintf("Invalid RPC response: 'block.%s' is a required property", key), request.Method, request.Params)
		}
	}
	zeroHash := "0x" + strings.Repeat("0", 64)
	defaults := []struct {
		key   string
		value any
	}{
		{"miner", "0x" + strings.Repeat("0", 40)}, {"gasUsed", "0x0"}, {"gasLimit", "0x0"}, {"baseFeePerGas", "0x0"}, {"nonce", "0x0"}, {"mixHash", zeroHash}, {"stateRoot", zeroHash}, {"transactionsRoot", zeroHash}, {"sha3Uncles", zeroHash}, {"size", "0x0"}, {"difficulty", "0x0"}, {"totalDifficulty", "0x0"}, {"extraData", "0x"},
	}
	for _, entry := range defaults {
		if jsvalue.IsUndefined(sourceRPCProperty(block, entry.key)) {
			block[entry.key] = entry.value
		}
	}
	for _, key := range []string{"number", "timestamp"} {
		if err := checkQuantity(block, key, "block", request.Method, request.Params, pgBigintMax); err != nil {
			return nil, err
		}
	}
	header := request.Method == "eth_subscribe" || (len(request.Params) > 1 && request.Params[1] == false)
	if header {
		block["transactions"] = jsvalue.Undefined
		return block, nil
	}
	if jsvalue.IsUndefined(sourceRPCProperty(block, "transactions")) {
		return nil, fmt.Errorf("Invalid RPC response: 'block.transactions' is a required property")
	}
	transactions, err := sourceRPCTransactions(block)
	if err != nil {
		return nil, err
	}
	transactions, err = StandardizeTransactions(transactions, request.Method, request.Params)
	if err != nil {
		return nil, err
	}
	block["transactions"] = transactions
	return block, nil
}

func (a *Actions) GetSourceBlockByNumber(ctx context.Context, number string, transactions bool) (map[string]any, error) {
	params := []any{number, transactions}
	var block map[string]any
	if err := a.requester.Call(ctx, "eth_getBlockByNumber", params, &block); err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("Block at number %q could not be found.", number)
	}
	return StandardizeSourceBlock(block, ActionRequest{"eth_getBlockByNumber", params})
}

func ValidateSourceTransactionsAndBlock(block map[string]any, request ActionRequest) error {
	transactions, err := sourceRPCTransactions(block)
	if err != nil {
		return err
	}
	for index, transaction := range transactions {
		if sourceRPCProperty(block, "hash") != sourceRPCProperty(transaction, "blockHash") {
			return validationError(fmt.Sprintf("Inconsistent RPC response data. The transaction at index %d of the 'block.transactions' array has a 'transaction.blockHash' of %s, but the block itself has a 'block.hash' of %s.", index, sourceRPCText(sourceRPCProperty(transaction, "blockHash")), sourceRPCText(sourceRPCProperty(block, "hash"))), request)
		}
		if sourceRPCProperty(block, "number") != sourceRPCProperty(transaction, "blockNumber") {
			n, err := rpcQuantity(transaction["blockNumber"])
			if err != nil {
				return err
			}
			b, err := rpcQuantity(block["number"])
			if err != nil {
				return err
			}
			return validationError(fmt.Sprintf("Inconsistent RPC response data. The transaction at index %d of the 'block.transactions' array has a 'transaction.blockNumber' of %s (%s), but the block itself has a 'block.number' of %s (%s).", index, sourceRPCText(transaction["blockNumber"]), n, sourceRPCText(block["number"]), b), request)
		}
	}
	return nil
}

func ValidateSourceTracesAndBlock(traces []CallTrace, block map[string]any, tracesRequest, blockRequest ActionRequest) error {
	transactions, err := sourceRPCTransactions(block)
	if err != nil {
		return err
	}
	hashes := map[string]bool{}
	for _, transaction := range transactions {
		hashes[sourceRPCText(sourceRPCProperty(transaction, "hash"))] = true
	}
	for index, trace := range traces {
		if !hashes[trace.TransactionHash] {
			return validationError(fmt.Sprintf("Inconsistent RPC response data. The top-level trace at array index %d has a 'transactionHash' of %s, but the associated 'block.transactions' array does not contain a transaction matching that hash.", index, trace.TransactionHash), blockRequest, tracesRequest)
		}
	}
	return nil
}

func ValidateSourceReceiptsAndBlock(receipts []map[string]any, block map[string]any, receiptsRequest, blockRequest ActionRequest) error {
	transactions, err := sourceRPCTransactions(block)
	if err != nil {
		return err
	}
	byIndex := map[string]map[string]any{}
	for _, transaction := range transactions {
		byIndex[sourceRPCText(sourceRPCProperty(transaction, "transactionIndex"))] = transaction
	}
	for index, receipt := range receipts {
		prefix := fmt.Sprintf("Inconsistent RPC response data. The receipt at array index %d", index)
		if sourceRPCProperty(block, "hash") != sourceRPCProperty(receipt, "blockHash") {
			return validationError(fmt.Sprintf("%s has a 'receipt.blockHash' of %s, but the associated block has a 'block.hash' of %s.", prefix, sourceRPCText(sourceRPCProperty(receipt, "blockHash")), sourceRPCText(sourceRPCProperty(block, "hash"))), blockRequest, receiptsRequest)
		}
		if sourceRPCProperty(block, "number") != sourceRPCProperty(receipt, "blockNumber") {
			n, err := rpcQuantity(receipt["blockNumber"])
			if err != nil {
				return err
			}
			b, err := rpcQuantity(block["number"])
			if err != nil {
				return err
			}
			return validationError(fmt.Sprintf("%s has a 'receipt.blockNumber' of %s (%s), but the associated block has a 'block.number' of %s (%s).", prefix, sourceRPCText(receipt["blockNumber"]), n, sourceRPCText(block["number"]), b), blockRequest, receiptsRequest)
		}
		transaction, ok := byIndex[sourceRPCText(sourceRPCProperty(receipt, "transactionIndex"))]
		if !ok {
			n, err := rpcQuantity(receipt["transactionIndex"])
			if err != nil {
				return err
			}
			return validationError(fmt.Sprintf("%s has a 'receipt.transactionIndex' of %s (%s), but the associated 'block.transactions' array does not contain a transaction matching that 'transactionIndex'.", prefix, sourceRPCText(receipt["transactionIndex"]), n), blockRequest, receiptsRequest)
		}
		if sourceRPCProperty(transaction, "hash") != sourceRPCProperty(receipt, "transactionHash") {
			n, err := rpcQuantity(receipt["transactionIndex"])
			if err != nil {
				return err
			}
			return validationError(fmt.Sprintf("%s matches a transaction in the associated 'block.transactions' array by 'transactionIndex' %s (%s), but the receipt has a 'receipt.transactionHash' of %s while the transaction has a 'transaction.hash' of %s.", prefix, sourceRPCText(receipt["transactionIndex"]), n, sourceRPCText(sourceRPCProperty(receipt, "transactionHash")), sourceRPCText(sourceRPCProperty(transaction, "hash"))), blockRequest, receiptsRequest)
		}
	}
	if receiptsRequest.Method == "eth_getBlockReceipts" && len(transactions) != len(receipts) {
		return validationError(fmt.Sprintf("Inconsistent RPC response data. The receipts array has length %d, but the associated 'block.transactions' array has length %d.", len(receipts), len(transactions)), blockRequest, receiptsRequest)
	}
	return nil
}
