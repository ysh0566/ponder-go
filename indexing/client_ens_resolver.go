package indexing

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ysh0566/ponder-go/abi"
)

const ensFindResolverABI = `[{"type":"function","stateMutability":"view","name":"findResolver","inputs":[{"type":"bytes"}],"outputs":[{"type":"address"},{"type":"bytes32"},{"type":"uint256"}]}]`

var findResolverABI = sync.OnceValues(func() (abi.ABI, error) { return abi.Parse(ensFindResolverABI) })

// ENSResolverOptions selects the historical block and optional Universal Resolver.
type ENSResolverOptions struct {
	BlockOptions
	CCIPRead                 CCIPReadOptions
	UniversalResolverAddress string
}

// GetEnsResolver returns the resolver for an ENS-normalized name at the
// selected block. A successful zero-address result is returned as an address.
func (c *Client) GetEnsResolver(ctx context.Context, name string, options ...ENSResolverOptions) (string, error) {
	if len(options) > 1 {
		return "", fmt.Errorf("GetEnsResolver accepts at most one options object")
	}
	var opt ENSResolverOptions
	if len(options) == 1 {
		opt = options[0]
	}
	tag, err := c.blockTag(ctx, []BlockOptions{opt.BlockOptions})
	if err != nil {
		return "", err
	}
	resolver, err := c.deploymentAddress(ctx, opt.BlockOptions, opt.UniversalResolverAddress, c.ensResolver, "ENS universal resolver")
	if err != nil {
		return "", err
	}
	if !c.allowsENSName(name) {
		return "", fmt.Errorf("name %q does not match the ENS suffixes %v for chain %v", name, c.ensTLDs, c.chainID)
	}
	parsed, err := findResolverABI()
	if err != nil {
		return "", err
	}
	call := ContractCall{ABI: parsed, Address: resolver, FunctionName: "findResolver", Args: []any{ensDNSPacket(name)}}
	data, f, err := encodeContractCall(call)
	if err != nil {
		return "", err
	}
	tx := Object{"to": resolver, "data": data}
	raw, err := c.readEVM(ctx, tx, []any{tx, tag}, cacheStateAt(tag), opt.CCIPRead)
	if err != nil {
		return "", err
	}
	encoded, err := decodeHexResponse(raw)
	if err != nil {
		return "", err
	}
	value, err := decodeContractResult(f, encoded)
	if err != nil {
		return "", err
	}
	tuple, ok := value.([]any)
	if !ok || len(tuple) != 3 {
		return "", fmt.Errorf("invalid ENS findResolver output")
	}
	address, ok := tuple[0].(string)
	if !ok {
		return "", fmt.Errorf("invalid ENS resolver address")
	}
	return address, nil
}

func ensSuffixes(metadata any) []string {
	switch suffixes := metadata.(type) {
	case []string:
		return slices.Clone(suffixes)
	case []any:
		if suffixes == nil {
			return nil
		}
		out := make([]string, 0, len(suffixes))
		for _, suffix := range suffixes {
			if s, ok := suffix.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func (c *Client) allowsENSName(name string) bool {
	if c.ensTLDs == nil {
		return true
	}
	for _, suffix := range c.ensTLDs {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// The Universal Resolver uses a DNS wire name terminated by a zero byte.
// A UTF-8 label exceeding the one-byte length is represented by [labelhash].
func ensDNSPacket(name string) string {
	name = strings.TrimSuffix(strings.TrimPrefix(name, "."), ".")
	var data []byte
	if name != "" {
		for _, label := range strings.Split(name, ".") {
			if len(label) > 255 {
				label = "[" + hex.EncodeToString(crypto.Keccak256([]byte(label))) + "]"
			}
			data = append(data, byte(len(label)))
			data = append(data, label...)
		}
	}
	return "0x" + hex.EncodeToString(append(data, 0))
}
