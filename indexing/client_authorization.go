package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// EIP-7702 authorization fields as received in an RPC transaction object.
type rpcAuthorization struct {
	Address string
	ChainID string
	Nonce   string
	YParity string
	R       string
	S       string
}

func (a rpcAuthorization) authority() (string, error) {
	delegate, ok := contractAddress(a.Address)
	if !ok {
		return "", fmt.Errorf("invalid authorization delegate %q", a.Address)
	}
	chain, err := hexutil.DecodeBig(a.ChainID)
	if err != nil {
		return "", err
	}
	nonce, err := hexutil.DecodeUint64(a.Nonce)
	if err != nil {
		return "", err
	}
	parity, err := hexutil.DecodeUint64(a.YParity)
	if err != nil || parity > 1 {
		return "", fmt.Errorf("invalid authorization yParity %q", a.YParity)
	}
	payload, err := rlp.EncodeToBytes([]any{chain, delegate, nonce})
	if err != nil {
		return "", err
	}
	var signature [65]byte
	for i, part := range []string{a.R, a.S} {
		hex, prefixed := strings.CutPrefix(part, "0x")
		n, ok := new(big.Int).SetString(hex, 16)
		if !prefixed || !ok || n.Sign() <= 0 || len(hex) > 64 || strings.HasPrefix(hex, "+") {
			return "", fmt.Errorf("invalid authorization signature")
		}
		n.FillBytes(signature[i*32 : (i+1)*32])
	}
	signature[64] = byte(parity)
	key, err := crypto.SigToPub(crypto.Keccak256([]byte{0x05}, payload), signature[:])
	if err != nil {
		return "", err
	}
	return crypto.PubkeyToAddress(*key).Hex(), nil
}

func (c *Client) estimateAuthorizedGas(ctx context.Context, transaction Object, options []CallOptions) (*big.Int, error) {
	ctx, finish, err := c.set.managedRoots.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	encoded, err := json.Marshal(transaction["authorizationList"])
	if err != nil {
		return nil, err
	}
	var list []rpcAuthorization
	if err := json.Unmarshal(encoded, &list); err != nil {
		return nil, fmt.Errorf("authorizationList: %w", err)
	}
	if len(list) == 0 {
		return c.estimateRPCGas(ctx, transaction, options)
	}
	if transaction["to"] == nil || transaction["to"] == "" {
		address, err := list[0].authority()
		if err != nil {
			return nil, fmt.Errorf("cannot infer transaction target from authorization: %w", err)
		}
		transaction = maps.Clone(transaction)
		transaction["to"] = address
	}
	estimate, err := c.estimateRPCGas(ctx, transaction, options)
	if err != nil {
		return nil, err
	}
	balance, err := c.rpcRequest(ctx, "eth_getBalance", []any{transaction["from"], "latest"}, false)
	if err != nil {
		return nil, err
	}
	amount, err := decodeRPCQuantity(balance, "authorization sender balance")
	if err != nil {
		return nil, err
	}
	// viem adds twice a separate estimate for each delegation. If the
	// provider rejects that secondary request it uses 100,000 gas for it.
	for _, auth := range list {
		request := Object{"to": auth.Address, "value": hexutil.EncodeBig(amount)}
		for _, field := range []string{"from", "data"} {
			if v, ok := transaction[field]; ok {
				request[field] = v
			}
		}
		supplement, err := c.estimateRPCGas(ctx, request, options)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			supplement = big.NewInt(100_000)
		}
		estimate.Add(estimate, supplement.Lsh(supplement, 1))
	}
	return estimate, nil
}
