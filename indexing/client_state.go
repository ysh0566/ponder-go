package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// FeeHistory holds gas usage and fees in wei. Reward is optional when no
// priority fee percentiles were requested.
type FeeHistory struct {
	OldestBlock   *big.Int
	BaseFeePerGas []*big.Int
	GasUsedRatio  []float64
	Reward        [][]*big.Int
}

// GetFeeHistory reads the requested range ending at the callback's block,
// or at the block selected by options.
func (c *Client) GetFeeHistory(ctx context.Context, blockCount uint64, rewardPercentiles []float64, options ...BlockOptions) (*FeeHistory, error) {
	tag, err := c.blockTag(ctx, options)
	if err != nil {
		return nil, err
	}
	if rewardPercentiles == nil {
		rewardPercentiles = []float64{}
	}
	params := []any{hexutil.EncodeUint64(blockCount), tag, rewardPercentiles}
	raw, err := c.rpcRequest(ctx, "eth_feeHistory", params, cacheStateAt(tag))
	if err != nil {
		return nil, err
	}
	var result struct {
		OldestBlock   *hexutil.Big
		BaseFeePerGas []hexutil.Big
		GasUsedRatio  []float64
		Reward        [][]hexutil.Big
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.OldestBlock == nil || result.BaseFeePerGas == nil || result.GasUsedRatio == nil {
		return nil, fmt.Errorf("eth_feeHistory returned incomplete history")
	}
	history := &FeeHistory{
		OldestBlock:   result.OldestBlock.ToInt(),
		BaseFeePerGas: feeQuantities(result.BaseFeePerGas),
		GasUsedRatio:  result.GasUsedRatio,
	}
	if result.Reward != nil {
		history.Reward = make([][]*big.Int, len(result.Reward))
		for i, row := range result.Reward {
			history.Reward[i] = feeQuantities(row)
		}
	}
	return history, nil
}

func feeQuantities(values []hexutil.Big) []*big.Int {
	result := make([]*big.Int, len(values))
	for i := range values {
		result[i] = values[i].ToInt()
	}
	return result
}

// blockTag returns "latest" only when the caller explicitly requests immutable
// caching. Other non-genesis heights may be replaced by a reorg.
func cacheStateAt(tag string) bool {
	return tag == "0x0" || tag == "latest"
}

// AccountProof contains the EIP-1186 account fields and the encoded Merkle
// proof nodes returned by the RPC provider.
type AccountProof struct {
	Address      string
	Balance      *big.Int
	Nonce        uint64
	CodeHash     string
	StorageHash  string
	AccountProof []string
	StorageProof []StorageProof
}

// StorageProof holds the value and encoded proof nodes for one storage slot.
type StorageProof struct {
	Key   string
	Value *big.Int
	Proof []string
}

// GetProof reads an account proof and proofs for the requested storage keys
// at the callback's block, or at the block selected by options.
func (c *Client) GetProof(ctx context.Context, address string, storageKeys []string, options ...BlockOptions) (*AccountProof, error) {
	tag, err := c.blockTag(ctx, options)
	if err != nil {
		return nil, err
	}
	if storageKeys == nil {
		storageKeys = []string{}
	}
	raw, err := c.rpcRequest(ctx, "eth_getProof", []any{address, storageKeys, tag}, cacheStateAt(tag))
	if err != nil {
		return nil, err
	}
	var result struct {
		Address      string
		Balance      *hexutil.Big
		Nonce        *hexutil.Uint64
		CodeHash     string
		StorageHash  string
		AccountProof []string
		StorageProof []struct {
			Key   string
			Value *hexutil.Big
			Proof []string
		}
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Balance == nil || result.Nonce == nil || result.Address == "" || result.CodeHash == "" || result.StorageHash == "" || result.AccountProof == nil || result.StorageProof == nil {
		return nil, fmt.Errorf("eth_getProof returned incomplete account proof")
	}
	proof := &AccountProof{
		Address:      result.Address,
		Balance:      result.Balance.ToInt(),
		Nonce:        uint64(*result.Nonce),
		CodeHash:     result.CodeHash,
		StorageHash:  result.StorageHash,
		AccountProof: result.AccountProof,
		StorageProof: make([]StorageProof, len(result.StorageProof)),
	}
	for i, slot := range result.StorageProof {
		if slot.Key == "" || slot.Value == nil || slot.Proof == nil {
			return nil, fmt.Errorf("eth_getProof returned incomplete storage proof at index %d", i)
		}
		proof.StorageProof[i] = StorageProof{Key: slot.Key, Value: slot.Value.ToInt(), Proof: slot.Proof}
	}
	return proof, nil
}
