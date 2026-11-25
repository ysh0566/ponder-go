package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/ysh0566/ponder-go/rpc"
)

// TransactionBlock selects a block for transaction lookups or counts.
// An empty selector reads the current head. A block hash can select a block
// for a transaction lookup or block transaction count.
type TransactionBlock struct {
	BlockNumber *big.Int
	BlockHash   string
	BlockTag    string
}

func (b TransactionBlock) selector() (string, error) {
	switch {
	case b.BlockNumber != nil:
		if b.BlockNumber.Sign() < 0 || b.BlockHash != "" || b.BlockTag != "" {
			return "", fmt.Errorf("invalid or conflicting transaction block selectors")
		}
		return "0x" + b.BlockNumber.Text(16), nil
	case b.BlockHash != "":
		if b.BlockTag != "" {
			return "", fmt.Errorf("a block hash and block tag cannot be combined")
		}
		return b.BlockHash, nil
	case b.BlockTag == "":
		return "latest", nil
	case b.BlockTag == "latest" || b.BlockTag == "earliest" || b.BlockTag == "pending" || b.BlockTag == "safe" || b.BlockTag == "finalized":
		return b.BlockTag, nil
	default:
		return "", fmt.Errorf("invalid block tag %q", b.BlockTag)
	}
}

// A height above genesis can name a different block after a reorg.
func (b TransactionBlock) immutable() bool {
	return b.BlockHash != "" || b.BlockTag == "earliest" || (b.BlockNumber != nil && b.BlockNumber.Sign() == 0)
}

func optionalTransactionBlock(blocks []TransactionBlock) (TransactionBlock, error) {
	if len(blocks) > 1 {
		return TransactionBlock{}, fmt.Errorf("specify at most one transaction block")
	}
	if len(blocks) == 1 {
		return blocks[0], nil
	}
	return TransactionBlock{}, nil
}

func transactionCount(raw json.RawMessage) (uint64, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	if len(value) < 3 || value[:2] != "0x" {
		return 0, fmt.Errorf("invalid transaction count %q", value)
	}
	return strconv.ParseUint(value[2:], 16, 64)
}

// GetTransactionCount reads the account nonce at the selected block.
func (c *Client) GetTransactionCount(ctx context.Context, address string, blocks ...TransactionBlock) (uint64, error) {
	b, err := optionalTransactionBlock(blocks)
	if err != nil {
		return 0, err
	}
	if b.BlockHash != "" {
		return 0, fmt.Errorf("account transaction count requires a block number or tag")
	}
	selector, err := b.selector()
	if err != nil {
		return 0, err
	}
	raw, err := c.rpcRequest(ctx, "eth_getTransactionCount", []any{address, selector}, b.immutable())
	if err != nil {
		return 0, err
	}
	return transactionCount(raw)
}

// GetBlockTransactionCount returns the number of transactions in a block.
func (c *Client) GetBlockTransactionCount(ctx context.Context, blocks ...TransactionBlock) (uint64, error) {
	b, err := optionalTransactionBlock(blocks)
	if err != nil {
		return 0, err
	}
	selector, err := b.selector()
	if err != nil {
		return 0, err
	}
	method := "eth_getBlockTransactionCountByNumber"
	if b.BlockHash != "" {
		method = "eth_getBlockTransactionCountByHash"
	}
	raw, err := c.rpcRequest(ctx, method, []any{selector}, b.immutable())
	if err != nil {
		return 0, err
	}
	if string(raw) == "null" {
		return 0, rpc.ErrBlockNotFound
	}
	return transactionCount(raw)
}

// ErrTransactionNotFound means the selected transaction is absent at the node.
var ErrTransactionNotFound = errors.New("RPC transaction not found")

// TransactionLookup selects a transaction by hash or by index within a block.
type TransactionLookup struct {
	Hash               string
	Block              TransactionBlock
	Index              *uint64
	RetryEmptyResponse *bool
}

func (o TransactionLookup) request() (string, []any, bool, error) {
	if o.Hash != "" {
		if o.Index != nil || o.Block != (TransactionBlock{}) {
			return "", nil, false, fmt.Errorf("transaction hash cannot be combined with a block or index")
		}
		return "eth_getTransactionByHash", []any{o.Hash}, false, nil
	}
	if o.Index == nil {
		return "", nil, false, fmt.Errorf("specify a transaction hash or a block index")
	}
	selector, err := o.Block.selector()
	if err != nil {
		return "", nil, false, err
	}
	method := "eth_getTransactionByBlockNumberAndIndex"
	if o.Block.BlockHash != "" {
		method = "eth_getTransactionByBlockHashAndIndex"
	}
	return method, []any{selector, "0x" + strconv.FormatUint(*o.Index, 16)}, o.Block.immutable(), nil
}

// GetTransaction returns the transaction's JSON-RPC fields, including null
// inclusion fields for a pending transaction, as with GetBlock. Hash lookups
// read the node because the inclusion block may change during a reorg.
func (c *Client) GetTransaction(ctx context.Context, lookup TransactionLookup) (Object, error) {
	method, params, cache, err := lookup.request()
	if err != nil {
		return nil, err
	}
	return retryClientAction(ctx, c, lookup.RetryEmptyResponse, func(ctx context.Context) (Object, error) {
		return c.rpcObject(ctx, method, params, cache, fmt.Errorf("%w: %v", ErrTransactionNotFound, params))
	})
}

type ReceiptOptions struct{ RetryEmptyResponse *bool }

// GetTransactionReceipt reads a mined receipt, including its logs and RPC
// quantities. A missing receipt follows the callback empty-response policy.
func (c *Client) GetTransactionReceipt(ctx context.Context, hash string, options ...ReceiptOptions) (Object, error) {
	if hash == "" || len(options) > 1 {
		return nil, fmt.Errorf("specify a transaction hash and at most one receipt option")
	}
	var opt ReceiptOptions
	if len(options) == 1 {
		opt = options[0]
	}
	return retryClientAction(ctx, c, opt.RetryEmptyResponse, func(ctx context.Context) (Object, error) {
		return c.rpcObject(ctx, "eth_getTransactionReceipt", []any{hash}, false, &rpc.TransactionReceiptNotFoundError{Hash: hash})
	})
}

type ConfirmationOptions struct {
	Hash               string
	Receipt            Object
	RetryEmptyResponse *bool
}

// GetTransactionConfirmations counts inclusively from the inclusion block to
// the live chain head. A pending or genesis transaction returns zero.
func (c *Client) GetTransactionConfirmations(ctx context.Context, opt ConfirmationOptions) (*big.Int, error) {
	if (opt.Hash == "") == (opt.Receipt == nil) {
		return nil, fmt.Errorf("specify exactly one transaction hash or receipt")
	}
	return retryClientAction(ctx, c, opt.RetryEmptyResponse, func(ctx context.Context) (*big.Int, error) {
		raw, err := c.Request(ctx, "eth_blockNumber", []any{})
		if err != nil {
			return nil, err
		}
		head, err := decodeRPCQuantity(raw, "head block")
		if err != nil {
			return nil, err
		}
		obj := opt.Receipt
		if obj == nil {
			obj, err = c.rpcObject(ctx, "eth_getTransactionByHash", []any{opt.Hash}, false, fmt.Errorf("%w: %s", ErrTransactionNotFound, opt.Hash))
			if err != nil {
				return nil, err
			}
		}
		if obj["blockNumber"] == nil {
			return new(big.Int), nil
		}
		block, err := clientInteger(obj["blockNumber"])
		if err != nil {
			return nil, err
		}
		if block.Sign() == 0 {
			return new(big.Int), nil
		}
		head.Sub(head, block)
		return head.Add(head, big.NewInt(1)), nil
	})
}
