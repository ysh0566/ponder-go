package indexing

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/rpc"
)

//go:embed ens_reverse.json
var ensABIData string
var ensReverseABI = sync.OnceValues(func() (abi.ABI, error) { return abi.Parse(ensABIData) })

type ENSNameOptions struct {
	BlockOptions
	UniversalResolverAddress string
	CoinType                 *big.Int
	GatewayURLs              []string
	Strict                   bool
}

// GetEnsName reverse-resolves an address at the indexing event's block using
// the chain's configured ENS Universal Resolver, or an explicit override.
func (c *Client) GetEnsName(ctx context.Context, address string, options ...ENSNameOptions) (*string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("getEnsName accepts at most one options object")
	}
	opts := ENSNameOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	resolver := opts.UniversalResolverAddress
	if resolver == "" {
		resolver, _ = c.ensResolver["address"].(string)
		if resolver == "" {
			return nil, fmt.Errorf("ENS universal resolver is unavailable for chain %v", c.chainID)
		}
		if created := c.ensResolver["blockCreated"]; created != nil {
			tag, err := c.blockTag(ctx, []BlockOptions{opts.BlockOptions})
			if err != nil {
				return nil, err
			}
			if tag != "latest" {
				n, err := clientInteger(created)
				if err != nil {
					return nil, fmt.Errorf("invalid ENS deployment block: %w", err)
				}
				block, err := clientInteger(tag)
				if err != nil {
					return nil, err
				}
				if block.Cmp(n) < 0 {
					return nil, fmt.Errorf("ENS universal resolver was deployed at block %s", n)
				}
			}
		}
	}
	if _, ok := contractAddress(address); !ok {
		return nil, fmt.Errorf("invalid ENS address %q", address)
	}
	coinType := opts.CoinType
	if coinType == nil {
		coinType = big.NewInt(60)
	}
	if coinType.Sign() < 0 || coinType.BitLen() > 256 {
		return nil, fmt.Errorf("invalid ENS coin type")
	}
	gateways := opts.GatewayURLs
	if gateways == nil {
		gateways = []string{"x-batch-gateway:true"}
	}
	parsed, err := ensReverseABI()
	if err != nil {
		return nil, err
	}
	retry := false
	args := []any{address, coinType, gateways}
	result, err := c.ReadContract(ctx, ContractCall{ABI: parsed, Address: resolver, FunctionName: "reverseWithGateways", Args: args}, ReadContractOptions{BlockOptions: opts.BlockOptions, RetryEmptyResponse: &retry})
	if err != nil {
		if !opts.Strict && isNullENSError(err, parsed) {
			return nil, nil
		}
		return nil, err
	}
	values, ok := result.([]any)
	if !ok || len(values) != 3 {
		return nil, fmt.Errorf("invalid ENS reverse lookup result")
	}
	name, ok := values[0].(string)
	if !ok {
		return nil, fmt.Errorf("invalid ENS reverse lookup name")
	}
	if name == "" {
		return nil, nil
	}
	return &name, nil
}

func isNullENSError(err error, parsed abi.ABI) bool {
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) {
		return false
	}
	var data string
	if json.Unmarshal(rpcErr.Data, &data) != nil {
		return false
	}
	if len(data) < 10 {
		return false
	}
	for _, name := range []string{"HttpError", "ResolverError", "ResolverNotContract", "ResolverNotFound", "ReverseAddressMismatch", "UnsupportedResolverProfile"} {
		if item, ok := parsed.Errors[name]; ok && strings.EqualFold(item.ID.Hex()[:10], data[:10]) {
			bytes, err := hex.DecodeString(data[2:])
			if err != nil {
				return false
			}
			_, err = item.Unpack(bytes)
			return err == nil
		}
	}
	return false
}
