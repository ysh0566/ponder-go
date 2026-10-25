package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"ponder.local/ponder-go/rpc"
	"sync"

	ponderabi "ponder.local/ponder-go/abi"
)

const aggregate3ABIJSON = `[{"type":"function","name":"aggregate3","stateMutability":"payable","inputs":[{"name":"calls","type":"tuple[]","components":[{"name":"target","type":"address"},{"name":"allowFailure","type":"bool"},{"name":"callData","type":"bytes"}]}],"outputs":[{"name":"returnData","type":"tuple[]","components":[{"name":"success","type":"bool"},{"name":"returnData","type":"bytes"}]}]}]`

var aggregate3ABI = func() ponderabi.ABI {
	a, err := ponderabi.Parse(aggregate3ABIJSON)
	if err != nil {
		panic(err)
	}
	return a
}()

type MulticallOptions struct {
	BlockOptions
	MulticallAddress string
	// Nil defaults to true, as in viem. Wire aggregate3 always allows failures;
	// this option controls decoding and whether the outer action fails.
	AllowFailure       *bool
	RetryEmptyResponse *bool
	// Nil defaults to 1024 calldata bytes. Zero disables chunking.
	BatchSize *int
}
type MulticallResult struct {
	Status string `json:"status"`
	Result any    `json:"result,omitempty"`
	Error  error  `json:"-"`
}
type ContractCallRevertedError struct{ Address, FunctionName, Data string }

func (e *ContractCallRevertedError) Error() string {
	return fmt.Sprintf("contract %s function %s reverted with data %s", e.Address, e.FunctionName, e.Data)
}

type multicallItem struct {
	contract      ContractCall
	selected      ponderabi.Function
	data          string
	encodingError error
}
type multicallWireResult struct {
	success bool
	data    string
}

// Multicall uses real aggregate3 RPC requests, with persistent per-child cache
// lookup and miss-only batching. Duplicates retain their original positions.
// It requires an explicit deployment address; chain deployment discovery,
// account/state overrides and the source's prefetch/LRU are not implemented.
func (c *Client) Multicall(ctx context.Context, contracts []ContractCall, options MulticallOptions) ([]any, error) {
	if options.MulticallAddress == "" {
		return nil, fmt.Errorf("multicallAddress is required")
	}
	allowFailure := options.AllowFailure == nil || *options.AllowFailure
	batchSize := 1024
	if options.BatchSize != nil {
		batchSize = *options.BatchSize
	}
	if batchSize < 0 {
		return nil, fmt.Errorf("negative multicall batch size")
	}
	items := make([]multicallItem, len(contracts))
	chunks := [][]multicallItem{}
	chunk := []multicallItem{}
	size := 0
	for i, contract := range contracts {
		data, selected, err := encodeContractCall(contract)
		if err != nil && !allowFailure {
			return nil, err
		}
		if err != nil {
			data = "0x"
		}
		item := multicallItem{contract, selected, data, err}
		items[i] = item
		length := (len(data) - 2) / 2
		if batchSize > 0 && size+length > batchSize && len(chunk) > 0 {
			chunks = append(chunks, chunk)
			chunk = nil
			size = 0
		}
		chunk = append(chunk, item)
		size += length
	}
	if len(chunk) > 0 {
		chunks = append(chunks, chunk)
	}
	return retryEmpty(ctx, options.RetryEmptyResponse, func() ([]any, error) {
		tag, err := c.blockTag(ctx, []BlockOptions{options.BlockOptions})
		if err != nil {
			return nil, err
		}
		type settledChunk struct {
			wire []multicallWireResult
			err  error
		}
		settled := make([]settledChunk, len(chunks))
		var pending sync.WaitGroup
		for index, chunk := range chunks {
			// Prepare and admit each transport operation in source chunk order. A real
			// AsyncRequester binds the RPC before returning; synchronous-only custom
			// requesters preserve settlement but have weaker invocation ordering.
			await := c.beginMulticallChunk(ctx, chunk, options.MulticallAddress, tag)
			pending.Add(1)
			go func(index int) { defer pending.Done(); settled[index].wire, settled[index].err = await() }(index)
		}
		pending.Wait() // source Promise.allSettled: later effects survive early errors.
		results := make([]any, 0, len(items))
		for index, chunk := range chunks {
			wire, err := settled[index].wire, settled[index].err
			if err != nil {
				if !allowFailure {
					return nil, err
				}
				for range chunk {
					results = append(results, MulticallResult{Status: "failure", Error: err})
				}
				continue
			}
			for i, item := range chunk {
				var value any
				var failure error
				if item.encodingError != nil {
					failure = &ContractFunctionZeroDataError{FunctionName: item.contract.FunctionName}
				}
				if failure == nil && !wire[i].success {
					failure = &ContractCallRevertedError{item.contract.Address, item.contract.FunctionName, wire[i].data}
				}
				if failure == nil {
					value, failure = decodeContractResult(item.selected, wire[i].data)
				}
				if failure != nil {
					if !allowFailure {
						return nil, failure
					}
					results = append(results, MulticallResult{Status: "failure", Error: failure})
				} else if allowFailure {
					results = append(results, MulticallResult{Status: "success", Result: value})
				} else {
					results = append(results, value)
				}
			}
		}
		return results, nil
	})
}
func (c *Client) beginMulticallChunk(ctx context.Context, items []multicallItem, address, tag string) func() ([]multicallWireResult, error) {
	fail := func(err error) func() ([]multicallWireResult, error) {
		return func() ([]multicallWireResult, error) { return nil, err }
	}
	results := make([]multicallWireResult, len(items))
	misses := []int{}
	calls := []any{}
	params := make([][]any, len(items))
	for i, item := range items {
		params[i] = []any{Object{"to": item.contract.Address, "data": item.data}, tag}
		raw, hit, err := c.cached(ctx, "eth_call", params[i])
		if err != nil {
			return fail(err)
		}
		if hit {
			data, err := decodeHexResponse(raw)
			if err != nil {
				return fail(err)
			}
			results[i] = multicallWireResult{true, data}
			continue
		}
		misses = append(misses, i)
		calls = append(calls, Object{"target": item.contract.Address, "allowFailure": true, "callData": item.data})
	}
	if len(misses) == 0 {
		return func() ([]multicallWireResult, error) { return results, nil }
	}
	data, selected, err := encodeContractCall(ContractCall{ABI: aggregate3ABI, Address: address, FunctionName: "aggregate3", Args: []any{calls}})
	if err != nil {
		return fail(err)
	}
	var raw json.RawMessage
	callParams := []any{Object{"to": address, "data": data}, tag}
	var completion <-chan error
	if requester, ok := c.requester.(rpc.AsyncRequester); ok {
		completion = requester.BeginCall(ctx, "eth_call", callParams, &raw)
	} else {
		future := make(chan error, 1)
		completion = future
		go func() { future <- c.requester.Call(ctx, "eth_call", callParams, &raw); close(future) }()
	}
	return func() ([]multicallWireResult, error) {
		if err := <-completion; err != nil {
			return nil, err
		}
		hex, err := decodeHexResponse(raw)
		if err != nil {
			return nil, err
		}
		decoded, err := decodeContractResult(selected, hex)
		if err != nil {
			return nil, err
		}
		rows, ok := decoded.([]any)
		if !ok || len(rows) != len(misses) {
			return nil, fmt.Errorf("multicall results mismatch")
		}
		for j, index := range misses {
			row, ok := rows[j].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid aggregate3 result tuple")
			}
			success, ok := row["success"].(bool)
			if !ok {
				return nil, fmt.Errorf("invalid aggregate3 success")
			}
			value, ok := row["returnData"].(string)
			if !ok {
				return nil, fmt.Errorf("invalid aggregate3 returnData")
			}
			results[index] = multicallWireResult{success, value}
			if success {
				encoded, _ := json.Marshal(value)
				c.cacheResponse(ctx, "eth_call", params[index], encoded)
			}
		}
		return results, nil
	}
}
