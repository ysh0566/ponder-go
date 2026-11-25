package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	ponderabi "github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/rpc"
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
	CCIPRead         CCIPReadOptions
	MulticallAddress string
	Account          string
	StateOverride    StateOverride
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
	contract       ContractCall
	selected       ponderabi.Function
	data           string
	encodingError  error
	requireSuccess bool
}
type multicallWireResult struct {
	success bool
	data    string
}

// Multicall uses real aggregate3 RPC requests, with persistent per-child cache
// lookup and miss-only batching. Duplicates retain their original positions.
// The default deployment comes from the configured chain. Requests with a
// sender or state override keep that context on the entire aggregate call.
func (c *Client) Multicall(ctx context.Context, contracts []ContractCall, options MulticallOptions) ([]any, error) {
	ctx, finish, admissionErr := c.set.managedRoots.begin(ctx)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer finish()
	address, err := c.deploymentAddress(ctx, options.BlockOptions, options.MulticallAddress, c.multicallDeployment, "Multicall3")
	if err != nil {
		return nil, err
	}
	if options.Account != "" {
		if _, ok := contractAddress(options.Account); !ok {
			return nil, fmt.Errorf("invalid multicall account %q", options.Account)
		}
	}
	override, err := options.StateOverride.rpcParams()
	if err != nil {
		return nil, err
	}
	execution := aggregateExecution{address: address, account: options.Account, override: override, ccip: &options.CCIPRead}
	if !execution.custom() {
		if err := c.recordContracts(contracts, options.BlockOptions, true); err != nil {
			return nil, err
		}
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
		item := multicallItem{contract: contract, selected: selected, data: data, encodingError: err}
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
	firstAttempt := true
	return retryEmpty(ctx, options.RetryEmptyResponse, func() ([]any, error) {
		if !firstAttempt && !execution.custom() {
			if err := c.recordContracts(contracts, options.BlockOptions, true); err != nil {
				return nil, err
			}
		}
		firstAttempt = false
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
			await := c.beginMulticallChunk(ctx, chunk, execution, tag)
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

type aggregateExecution struct {
	ccip     *CCIPReadOptions
	address  string
	account  string
	override Object
}

func (e aggregateExecution) custom() bool {
	return e.account != "" || e.override != nil
}

func (e aggregateExecution) params(data, tag string) []any {
	call := Object{"to": e.address, "data": data}
	if e.account != "" {
		call["from"] = e.account
	}
	params := []any{call, tag}
	if e.override != nil {
		params = append(params, e.override)
	}
	return params
}

func (c *Client) beginMulticallChunk(ctx context.Context, items []multicallItem, execution aggregateExecution, tag string) func() ([]multicallWireResult, error) {
	fail := func(err error) func() ([]multicallWireResult, error) {
		return func() ([]multicallWireResult, error) { return nil, err }
	}
	writes := []rpc.CacheEntry{}
	queueWrite := func(params []any, value json.RawMessage, prefetched bool) {
		if !c.cacheEnabled || c.cache == nil {
			return
		}
		if prefetched {
			// The source compares the serialized future against the raw sentinel.
			if string(value) == "0x" {
				return
			}
		} else if !cacheableResponse(value) {
			return
		}
		key, err := c.cacheKey("eth_call", params)
		if err == nil {
			writes = append(writes, rpc.CacheEntry{Key: key, Value: value})
		}
	}
	flush := func() {
		if len(writes) == 0 {
			return
		}
		if batch, ok := c.cache.(rpc.AsyncCacheBatchWriter); ok {
			c.set.admitWrite(ctx, func(child context.Context) func() error {
				return awaitCacheWrite(batch.BeginCachePutMany(child, writes))
			})
			return
		}
		for _, entry := range writes {
			c.set.putAsync(ctx, c.cache, entry.Key, entry.Value)
		}
	}
	batch, batched := c.cache.(rpc.CacheBatchReader)
	batched = batched && c.cacheEnabled && !execution.custom()
	keys := []string{}
	results := make([]multicallWireResult, len(items))
	misses := []int{}

	params := make([][]any, len(items))
	var advance func(int, *prefetchValue) func() ([]multicallWireResult, error)
	advance = func(start int, retained *prefetchValue) func() ([]multicallWireResult, error) {
		for i := start; i < len(items); i++ {
			if execution.custom() {
				misses = append(misses, i)
				continue
			}
			item := items[i]
			params[i] = []any{Object{"to": item.contract.Address, "data": item.data}, tag}
			key, err := c.cacheKey("eth_call", params[i])
			if err != nil {
				return fail(err)
			}
			var prefetched *prefetchValue
			if i == start && retained != nil {
				prefetched = retained
			} else {
				c.set.mu.RLock()
				prefetched = c.prefetched[key]
				c.set.mu.RUnlock()
			}
			if prefetched != nil && prefetched.future {
				select {
				case <-prefetched.done:
				default:
					// Suspend only this chunk. Retain the actual future already read,
					// even if Clear removes its map entry while this action is waiting.
					index, value := i, prefetched
					return func() ([]multicallWireResult, error) {
						select {
						case <-value.done:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						return advance(index, value)()
					}
				}
			}
			raw, hit, err := c.prefetchedValueResult(ctx, key, prefetched, true)
			if err == nil && hit && prefetched != nil && prefetched.future {
				queueWrite(params[i], raw, true)
			}
			if err == nil && !hit && !batched {
				raw, hit, err = c.cached(ctx, "eth_call", params[i])
			}
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
			keys = append(keys, key)
		}
		if batched && len(keys) > 0 {
			values, err := batch.CacheGetMany(ctx, keys)
			if err != nil {
				return fail(err)
			}
			if len(values) != len(keys) {
				return fail(fmt.Errorf("multicall cache result count mismatch"))
			}
			remote := misses[:0]
			for j, i := range misses {
				if values[j] == nil {
					remote = append(remote, i)
					continue
				}
				value, err := decodeHexResponse(values[j])
				if err != nil {
					return fail(err)
				}
				results[i] = multicallWireResult{true, value}
			}
			misses = remote
		}
		if len(misses) == 0 {
			return func() ([]multicallWireResult, error) { flush(); return results, nil }
		}
		calls := make([]any, 0, len(misses))
		for _, i := range misses {
			item := items[i]
			calls = append(calls, Object{"target": item.contract.Address, "allowFailure": !item.requireSuccess, "callData": item.data})
		}
		data, selected, err := encodeContractCall(ContractCall{ABI: aggregate3ABI, Address: execution.address, FunctionName: "aggregate3", Args: []any{calls}})
		if err != nil {
			return fail(err)
		}
		var raw json.RawMessage
		callParams := execution.params(data, tag)
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
				if execution.ccip == nil {
					return nil, err
				}
				raw, err = c.resolveCCIP(ctx, execution.address, err, *execution.ccip)
				if err != nil {
					return nil, err
				}
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
				if success && !execution.custom() {
					encoded, _ := json.Marshal(value)
					queueWrite(params[index], encoded, false)
				}
			}
			flush()
			return results, nil
		}
	}
	return advance(0, nil)
}
