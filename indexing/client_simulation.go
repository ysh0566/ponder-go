package indexing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// CallOptions selects the block and transient state used for EVM execution.
// BlockOverrides holds RPC-formatted block fields and applies to eth_call.
type CallOptions struct {
	BlockOptions
	CCIPRead       CCIPReadOptions
	StateOverride  StateOverride
	BlockOverrides Object
	Deployless     *DeploylessCall
}

// Call executes a JSON-RPC transaction object at the callback block. The
// result is hexadecimal data, or nil when the EVM returns no bytes.
func (c *Client) Call(ctx context.Context, transaction Object, options ...CallOptions) (*string, error) {
	raw, err := c.executionRequest(ctx, "eth_call", transaction, options)
	if err != nil {
		return nil, err
	}
	data, err := decodeHexResponse(raw)
	if err != nil || data == "0x" {
		return nil, err
	}
	return &data, nil
}

// EstimateGas returns the gas estimate at the selected block, including the
// EIP-7702 authorization adjustment used by the pinned upstream client.
// Transaction quantities use the same hex encoding as Client.Request.
func (c *Client) EstimateGas(ctx context.Context, transaction Object, options ...CallOptions) (*big.Int, error) {
	if transaction["authorizationList"] != nil {
		return c.estimateAuthorizedGas(ctx, transaction, options)
	}
	return c.estimateRPCGas(ctx, transaction, options)
}

func (c *Client) estimateRPCGas(ctx context.Context, transaction Object, options []CallOptions) (*big.Int, error) {
	raw, err := c.executionRequest(ctx, "eth_estimateGas", transaction, options)
	if err != nil {
		return nil, err
	}
	return decodeRPCQuantity(raw, "gas estimate")
}

// Validate the shared transaction fields without changing any chain-specific
// fields the caller supplied. JSON marshaling also accepts native hex types.
func validateCallTransaction(tx Object) error {
	fields := Object{}
	for _, field := range []string{"from", "to", "gasPrice", "maxFeePerGas", "maxPriorityFeePerGas"} {
		if value := tx[field]; value != nil {
			fields[field] = value
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var parsed struct {
		From, To                                     *common.Address
		GasPrice, MaxFeePerGas, MaxPriorityFeePerGas *hexutil.Big
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("invalid call transaction: %w", err)
	}
	if parsed.GasPrice != nil && (parsed.MaxFeePerGas != nil || parsed.MaxPriorityFeePerGas != nil) {
		return fmt.Errorf("gasPrice cannot be combined with dynamic fee fields")
	}
	if parsed.MaxFeePerGas != nil && parsed.MaxPriorityFeePerGas != nil && parsed.MaxPriorityFeePerGas.ToInt().Cmp(parsed.MaxFeePerGas.ToInt()) > 0 {
		return fmt.Errorf("maxPriorityFeePerGas exceeds maxFeePerGas")
	}
	return nil
}

func (c *Client) executionRequest(ctx context.Context, method string, transaction Object, options []CallOptions) (json.RawMessage, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("%s accepts at most one options object", method)
	}
	var opt CallOptions
	if len(options) == 1 {
		opt = options[0]
	}
	tag, err := c.blockTag(ctx, []BlockOptions{opt.BlockOptions})
	if err != nil {
		return nil, err
	}
	if method == "eth_estimateGas" && (opt.BlockOverrides != nil || opt.Deployless != nil) {
		return nil, fmt.Errorf("eth_estimateGas accepts account state overrides only")
	}
	if err := validateCallTransaction(transaction); err != nil {
		return nil, err
	}
	override, err := opt.StateOverride.rpcParams()
	if err != nil {
		return nil, err
	}
	if transaction == nil {
		transaction = Object{}
	}
	if opt.Deployless != nil {
		transaction, err = opt.Deployless.transaction(transaction)
		if err != nil {
			return nil, err
		}
	}
	params := []any{transaction, tag}
	if opt.BlockOverrides != nil && override == nil {
		override = Object{}
	}
	if override != nil {
		params = append(params, override)
	}
	if opt.BlockOverrides != nil {
		params = append(params, opt.BlockOverrides)
	}
	// Explicit overrides have their own execution environment. A numeric
	// height can also change meaning when the callback is replayed on a fork.
	if method == "eth_call" {
		return c.readEVM(ctx, transaction, params, override == nil && cacheStateAt(tag), opt.CCIPRead)
	}
	if override == nil && cacheStateAt(tag) {
		return c.request(ctx, method, params)
	}
	return c.rpcRequest(ctx, method, params, false)
}

type SimulateContractOptions struct {
	CallOptions
	// Transaction supplies RPC fields such as from, value, gas and accessList.
	// The contract and its ABI supply the target and calldata.
	Transaction        Object
	DataSuffix         string
	RetryEmptyResponse *bool
}

// ContractSimulation includes the decoded ABI result and the encoded
// transaction suitable for another Call or EstimateGas.
type ContractSimulation struct {
	Result  any
	Request Object
}

// SimulateContract executes an ABI function locally on the node. It never
// broadcasts the transaction. Empty output for a function that declares a
// result follows the same retry policy as ReadContract.
func (c *Client) SimulateContract(ctx context.Context, contract ContractCall, options ...SimulateContractOptions) (*ContractSimulation, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("SimulateContract accepts at most one options object")
	}
	var opt SimulateContractOptions
	if len(options) == 1 {
		opt = options[0]
	}
	byCode := opt.Deployless != nil && opt.Deployless.Code != ""
	if _, ok := contractAddress(contract.Address); !ok && !(byCode && contract.Address == "") {
		return nil, fmt.Errorf("invalid simulation target %q", contract.Address)
	}
	data, f, err := encodeContractCall(contract)
	if err != nil {
		return nil, err
	}
	if opt.DataSuffix != "" {
		suffix, ok := strings.CutPrefix(opt.DataSuffix, "0x")
		if !ok {
			return nil, fmt.Errorf("data suffix must be 0x-prefixed")
		}
		if _, err := hex.DecodeString(suffix); err != nil {
			return nil, fmt.Errorf("invalid data suffix: %w", err)
		}
		data += suffix
	}
	tx := Object{"data": data}
	if contract.Address != "" {
		tx["to"] = contract.Address
	}
	for field := range opt.Transaction {
		switch field {
		case "to", "data", "input":
			return nil, fmt.Errorf("ABI contract supplies the simulation transaction field %q", field)
		}
	}
	maps.Copy(tx, opt.Transaction)
	if opt.Deployless != nil {
		tx, err = opt.Deployless.transaction(tx)
		if err != nil {
			return nil, err
		}
		opt.Deployless = nil
	}
	return retryClientAction(ctx, c, opt.RetryEmptyResponse, func(ctx context.Context) (*ContractSimulation, error) {
		result, err := c.Call(ctx, tx, opt.CallOptions)
		if err != nil {
			return nil, err
		}
		output := "0x"
		if result != nil {
			output = *result
		}
		value, err := decodeContractResult(f, output)
		if err != nil {
			return nil, err
		}
		return &ContractSimulation{Result: value, Request: tx}, nil
	})
}
