package rpc

import (
	"context"
	"fmt"
	"iter"
	"math"
	"sort"
	"strings"
	"sync"
)

// Requester is the action boundary used by the original source's RPC mocks.
type Requester interface {
	Call(context.Context, string, any, any) error
}
type ActionLogger interface {
	Debug(map[string]any)
	Warn(map[string]any)
}
type ActionOptions struct {
	EthGetLogsBlockRange *uint64
	Logger               ActionLogger
}
type logsMetadata struct {
	estimated uint64
	confirmed *uint64
}
type Actions struct {
	requester            Requester
	mu                   sync.Mutex
	logs                 *logsMetadata
	withoutBlockReceipts bool
}

func NewActions(requester Requester) *Actions {
	return &Actions{requester: requester, logs: &logsMetadata{estimated: 500}}
}
func (c *Client) Actions() *Actions {
	base := c
	for base.transport != nil {
		base = base.transport
	}
	base.mu.Lock()
	defer base.mu.Unlock()
	if base.actions == nil {
		base.actions = NewActions(base)
	}
	return base.actions
}

type LogsPage struct {
	Logs               []map[string]any
	FromBlock, ToBlock uint64
}

// GetLogsWithPagination is lazy: consumer early termination performs no further
// RPCs. Learning belongs to the RPC object, including across sync modes.
func (a *Actions) GetLogsWithPagination(ctx context.Context, input map[string]any, options *ActionOptions) iter.Seq2[LogsPage, error] {
	return func(yield func(LogsPage, error) bool) {
		fail := func(err error) { yield(LogsPage{}, err) }
		from, err := rpcQuantity(input["fromBlock"])
		if err != nil {
			fail(err)
			return
		}
		end, err := rpcQuantity(input["toBlock"])
		if err != nil {
			fail(err)
			return
		}
		if !from.IsUint64() || !end.IsUint64() {
			fail(fmt.Errorf("pagination block number exceeds uint64"))
			return
		}
		cursor, endBlock := from.Uint64(), end.Uint64()
		address, topics := input["address"], input["topics"]
		for cursor <= endBlock {
			if err = ctx.Err(); err != nil {
				fail(err)
				return
			}
			a.mu.Lock()
			metadata := a.logs
			size := metadata.estimated
			if metadata.confirmed != nil {
				size = *metadata.confirmed
			}
			if options != nil && options.EthGetLogsBlockRange != nil {
				size = *options.EthGetLogsBlockRange
			}
			a.mu.Unlock()
			if size == 0 {
				fail(fmt.Errorf("eth_getLogs block range must be positive"))
				return
			}
			to := endBlock
			if size-1 < endBlock-cursor {
				to = cursor + size - 1
			}
			query := map[string]any{"fromBlock": fmt.Sprintf("0x%x", cursor), "toBlock": fmt.Sprintf("0x%x", to)}
			if _, ok := input["address"]; ok {
				query["address"] = address
			}
			if _, ok := input["topics"]; ok {
				query["topics"] = topics
			}
			logs, err := getLogs(ctx, a.requester, query)
			if err != nil {
				if options != nil && options.EthGetLogsBlockRange != nil {
					fail(err)
					return
				}
				plan, planError := GetLogsRetryHelper(query, err)
				if planError != nil {
					fail(planError)
					return
				}
				if !plan.ShouldRetry {
					fail(err)
					return
				}
				start, _ := rpcQuantity(plan.Ranges[0].FromBlock)
				finish, _ := rpcQuantity(plan.Ranges[0].ToBlock)
				size = finish.Uint64() - start.Uint64() + 1
				if options != nil && options.Logger != nil {
					options.Logger.Debug(map[string]any{"msg": "Updated eth_getLogs range", "range": size})
				}
				next := &logsMetadata{estimated: size}
				if plan.IsSuggestedRange {
					n := size
					next.confirmed = &n
				}
				a.mu.Lock()
				a.logs = next
				a.mu.Unlock()
				continue
			}
			a.mu.Lock()
			if (options == nil || options.EthGetLogsBlockRange == nil) && metadata.confirmed == nil {
				grown := math.Floor(float64(metadata.estimated)*1.05 + 0.5)
				if grown >= float64(math.MaxUint64) {
					metadata.estimated = math.MaxUint64
				} else {
					metadata.estimated = uint64(grown)
				}
			}
			a.mu.Unlock()
			if !yield(LogsPage{logs, cursor, to}, nil) || to == endBlock {
				return
			}
			cursor = to + 1
		}
	}
}
func (c *Client) GetLogsWithPagination(ctx context.Context, input map[string]any, options *ActionOptions) iter.Seq2[LogsPage, error] {
	return c.Actions().GetLogsWithPagination(ctx, input, options)
}

type ActionRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}
type ReceiptResponse struct {
	Receipts []map[string]any
	Request  ActionRequest
}

type TransactionReceiptNotFoundError struct{ Hash string }

func (e *TransactionReceiptNotFoundError) Error() string {
	return fmt.Sprintf("Transaction receipt with hash %q could not be found. The Transaction may not be processed on a block yet.", e.Hash)
}

func (a *Actions) GetTransactionReceipt(ctx context.Context, hash string) (map[string]any, error) {
	var receipt map[string]any
	if err := a.requester.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, &TransactionReceiptNotFoundError{Hash: hash}
	}
	rows, err := StandardizeTransactionReceipts([]map[string]any{receipt}, ActionRequest{"eth_getTransactionReceipt", []any{hash}})
	if err != nil {
		return nil, err
	}
	return rows[0], nil
}

func (a *Actions) GetBlockReceipts(ctx context.Context, blockHash string) ([]map[string]any, error) {
	var receipts []map[string]any
	if err := a.requester.Call(ctx, "eth_getBlockReceipts", []any{blockHash}, &receipts); err != nil {
		return nil, err
	}
	if receipts == nil {
		return nil, fmt.Errorf("Received invalid empty eth_getBlockReceipts response.")
	}
	return StandardizeTransactionReceipts(receipts, ActionRequest{"eth_getBlockReceipts", []any{blockHash}})
}

func (c *Client) GetTransactionReceipt(ctx context.Context, hash string) (map[string]any, error) {
	return c.Actions().GetTransactionReceipt(ctx, hash)
}

func (c *Client) GetBlockReceipts(ctx context.Context, blockHash string) ([]map[string]any, error) {
	return c.Actions().GetBlockReceipts(ctx, blockHash)
}

// GetTransactionReceipts retains the original full block receipt response and
// request provenance. On any block-receipt failure, capability fallback persists
// for this RPC object; another RPC starts independently.
func (a *Actions) GetTransactionReceipts(ctx context.Context, blockHash string, transactionHashes []string, options *ActionOptions) ([]ReceiptResponse, error) {
	if len(transactionHashes) == 0 {
		return []ReceiptResponse{}, nil
	}
	a.mu.Lock()
	fallback := a.withoutBlockReceipts
	a.mu.Unlock()
	if !fallback {
		receipts, err := a.GetBlockReceipts(ctx, blockHash)
		if err == nil {
			return []ReceiptResponse{{receipts, ActionRequest{"eth_getBlockReceipts", []any{blockHash}}}}, nil
		}
		if options != nil && options.Logger != nil {
			options.Logger.Warn(map[string]any{"msg": "Caught eth_getBlockReceipts error, switching to eth_getTransactionReceipt method", "error": err})
		}
		a.mu.Lock()
		a.withoutBlockReceipts = true
		a.mu.Unlock()
	}
	responses := []ReceiptResponse{}
	seen := map[string]bool{}
	for _, hash := range transactionHashes {
		if seen[hash] {
			continue
		}
		seen[hash] = true
		receipt, err := a.GetTransactionReceipt(ctx, hash)
		if err != nil {
			return nil, err
		}
		responses = append(responses, ReceiptResponse{[]map[string]any{receipt}, ActionRequest{"eth_getTransactionReceipt", []any{hash}}})
	}
	sort.SliceStable(responses, func(i, j int) bool {
		a, _ := rpcQuantity(responses[i].Receipts[0]["transactionIndex"])
		b, _ := rpcQuantity(responses[j].Receipts[0]["transactionIndex"])
		return a.Cmp(b) < 0
	})
	return responses, nil
}
func (c *Client) GetTransactionReceipts(ctx context.Context, blockHash string, hashes []string, options *ActionOptions) ([]ReceiptResponse, error) {
	return c.Actions().GetTransactionReceipts(ctx, blockHash, hashes, options)
}

func StandardizeTransactionReceipts(receipts []map[string]any, request ActionRequest) ([]map[string]any, error) {
	seen := map[string]bool{}
	for _, receipt := range receipts {
		hash := fmt.Sprint(receipt["transactionHash"])
		if seen[hash] {
			return nil, providerError(fmt.Sprintf("Inconsistent RPC response data. The receipts array contains two objects with a 'transactionHash' of %s.", hash), request.Method, request.Params)
		}
		seen[hash] = true
		for _, key := range []string{"blockHash", "blockNumber", "transactionHash", "transactionIndex", "from", "status"} {
			if _, ok := receipt[key]; !ok {
				return nil, providerError(fmt.Sprintf("Invalid RPC response: 'receipt.%s' is a required property", key), request.Method, request.Params)
			}
		}
		defaults := map[string]any{"to": nil, "logs": []any{}, "logsBloom": "0x" + strings.Repeat("0", 512), "gasUsed": "0x0", "cumulativeGasUsed": "0x0", "effectiveGasPrice": "0x0", "root": "0x" + strings.Repeat("0", 64), "type": "0x0"}
		for key, value := range defaults {
			if _, ok := receipt[key]; !ok {
				receipt[key] = value
			}
		}
		for _, key := range []string{"blockNumber", "transactionIndex"} {
			maximum := pgIntegerMax
			if key == "blockNumber" {
				maximum = pgBigintMax
			}
			if err := checkQuantity(receipt, key, "receipt", request.Method, request.Params, maximum); err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(receipts, func(i, j int) bool {
		a, _ := rpcQuantity(receipts[i]["transactionIndex"])
		b, _ := rpcQuantity(receipts[j]["transactionIndex"])
		return a.Cmp(b) < 0
	})
	return receipts, nil
}

func validationError(message string, requests ...ActionRequest) error {
	error := &ProviderError{Message: message, Meta: []string{"Please report this error to the RPC operator."}}
	for _, request := range requests {
		item := providerError("", request.Method, request.Params).(*ProviderError)
		error.Meta = append(error.Meta, item.Meta[1])
	}
	return error
}
func ValidateLogsAndBlock(logs []map[string]any, block map[string]any, logsRequest, blockRequest ActionRequest) error {
	transactions := map[string]map[string]any{}
	switch xs := block["transactions"].(type) {
	case []map[string]any:
		for _, tx := range xs {
			transactions[fmt.Sprint(tx["transactionIndex"])] = tx
		}
	case []any:
		for _, v := range xs {
			if tx, ok := v.(map[string]any); ok {
				transactions[fmt.Sprint(tx["transactionIndex"])] = tx
			}
		}
	}
	for _, log := range logs {
		index, _ := rpcQuantity(log["logIndex"])
		prefix := fmt.Sprintf("Inconsistent RPC response data. The log with 'logIndex' %v (%v)", log["logIndex"], index)
		if block["hash"] != log["blockHash"] {
			return validationError(fmt.Sprintf("%s has a 'log.blockHash' of %v, but the associated block has a 'block.hash' of %v.", prefix, log["blockHash"], block["hash"]), blockRequest, logsRequest)
		}
		if block["number"] != log["blockNumber"] {
			n, _ := rpcQuantity(log["blockNumber"])
			b, _ := rpcQuantity(block["number"])
			return validationError(fmt.Sprintf("%s has a 'log.blockNumber' of %v (%v), but the associated block has a 'block.number' of %v (%v).", prefix, log["blockNumber"], n, block["number"], b), blockRequest, logsRequest)
		}
		if log["transactionHash"] == "0x"+strings.Repeat("0", 64) {
			continue
		}
		tx, ok := transactions[fmt.Sprint(log["transactionIndex"])]
		n, _ := rpcQuantity(log["transactionIndex"])
		if !ok {
			return validationError(fmt.Sprintf("%s has a 'log.transactionIndex' of %v (%v), but the associated 'block.transactions' array does not contain a transaction matching that 'transactionIndex'.", prefix, log["transactionIndex"], n), blockRequest, logsRequest)
		}
		if tx["hash"] != log["transactionHash"] {
			return validationError(fmt.Sprintf("%s matches a transaction in the associated 'block.transactions' array by 'transactionIndex' %v (%v), but the log has a 'log.transactionHash' of %v while the transaction has a 'transaction.hash' of %v.", prefix, log["transactionIndex"], n, log["transactionHash"], tx["hash"]), blockRequest, logsRequest)
		}
	}
	return nil
}
