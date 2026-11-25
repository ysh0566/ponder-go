package indexing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	ponderabi "github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/rpc"
)

// Request runs a raw JSON-RPC request through the indexing client's cache policy.
func (c *Client) Request(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	return c.rpcRequest(ctx, method, params, true)
}

func (c *Client) rpcRequest(ctx context.Context, method string, params []any, cache bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, finish, err := c.set.managedRoots.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	c.set.mu.RLock()
	closed := c.set.closed
	c.set.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("indexing client is closed")
	}
	if !cache {
		return c.sendRPC(ctx, method, params)
	}
	if method == "eth_call" && len(params) > 0 {
		var call struct{ To, Data string }
		raw, err := json.Marshal(params[0])
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		if strings.HasPrefix(call.Data, aggregateSelector) {
			tag := "latest"
			if len(params) > 1 {
				var ok bool
				tag, ok = params[1].(string)
				if !ok {
					return nil, fmt.Errorf("aggregate3 requires a block tag or number")
				}
			}
			return c.rawAggregate(ctx, call.To, tag, call.Data)
		}
	}
	switch method {
	case "eth_getBalance", "eth_getTransactionCount", "eth_getBlockByNumber",
		"eth_getBlockTransactionCountByNumber", "eth_getTransactionByBlockNumberAndIndex", "eth_call",
		"eth_estimateGas", "eth_feeHistory", "eth_getProof",
		"eth_getCode", "eth_getStorageAt", "eth_getUncleByBlockNumberAndIndex",
		"debug_traceBlockByNumber", "eth_getBlockByHash", "eth_getTransactionByHash",
		"eth_getBlockTransactionCountByHash", "eth_getTransactionByBlockHashAndIndex", "eth_getTransactionConfirmations",
		"eth_getTransactionReceipt", "eth_getUncleByBlockHashAndIndex", "eth_getUncleCountByBlockHash",
		"debug_traceBlockByHash", "debug_traceTransaction", "debug_traceCall":
		return c.request(ctx, method, params)
	default:
		return c.sendRPC(ctx, method, params)
	}
}

func (c *Client) sendRPC(ctx context.Context, method string, params any) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.requester.Call(ctx, method, params, &raw)
	return raw, err
}

var aggregateFunction = aggregate3ABI.FunctionEntries()[0]
var aggregateSelector = ponderabi.FunctionSelector(aggregateFunction)

func (c *Client) rawAggregate(ctx context.Context, address, tag, data string) (json.RawMessage, error) {
	args, err := ponderabi.DecodeParameters(aggregateFunction.Inputs, "0x"+data[len(aggregateSelector):])
	if err != nil {
		return nil, err
	}
	calls, ok := args[0].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid aggregate3 calls")
	}
	items := make([]multicallItem, len(calls))
	for i, call := range calls {
		fields, ok := call.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid aggregate3 tuple")
		}
		target, okTarget := fields["target"].(string)
		input, okInput := fields["callData"].(string)
		allow, okAllow := fields["allowFailure"].(bool)
		if !okTarget || !okInput || !okAllow {
			return nil, fmt.Errorf("invalid aggregate3 fields")
		}
		items[i] = multicallItem{contract: ContractCall{Address: target}, data: input, requireSuccess: !allow}
	}
	rows, err := c.beginMulticallChunk(ctx, items, address, tag)()
	if err != nil {
		return nil, err
	}
	out := make([]struct {
		Success    bool
		ReturnData []byte
	}, len(rows))
	for i, row := range rows {
		value, err := hex.DecodeString(strings.TrimPrefix(row.data, "0x"))
		if err != nil {
			return nil, err
		}
		out[i].Success, out[i].ReturnData = row.success, value
	}
	b, err := aggregate3ABI.Methods["aggregate3"].Outputs.Pack(out)
	if err != nil {
		return nil, err
	}
	return json.Marshal("0x" + hex.EncodeToString(b))
}

// GetBlockOptions selects a block by hash or number, and optionally returns full transactions.
type GetBlockOptions struct {
	BlockHash           string
	BlockNumber         *big.Int
	IncludeTransactions bool
	RetryEmptyResponse  *bool
}

// GetBlock returns the block's RPC fields and retries null results using the indexing action policy.
func (c *Client) GetBlock(ctx context.Context, opt GetBlockOptions) (Object, error) {
	if (opt.BlockNumber == nil) == (opt.BlockHash == "") {
		return nil, fmt.Errorf("specify exactly one block number or hash")
	}
	method, selector := "eth_getBlockByHash", opt.BlockHash
	if opt.BlockNumber != nil {
		if opt.BlockNumber.Sign() < 0 {
			return nil, fmt.Errorf("negative block number")
		}
		method, selector = "eth_getBlockByNumber", "0x"+opt.BlockNumber.Text(16)
	}
	return retryClientAction(ctx, c, opt.RetryEmptyResponse, func(ctx context.Context) (Object, error) {
		return c.rpcObject(ctx, method, []any{selector, opt.IncludeTransactions}, true, rpc.ErrBlockNotFound)
	})
}

func (c *Client) rpcObject(ctx context.Context, method string, params []any, cache bool, missing error) (Object, error) {
	raw, err := c.rpcRequest(ctx, method, params, cache)
	if err != nil {
		return nil, err
	}
	var obj Object
	if err = json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, missing
	}
	return obj, nil
}
