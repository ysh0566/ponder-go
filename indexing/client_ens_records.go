package indexing

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ysh0566/ponder-go/abi"
)

const ensRecordsABI = `[
{"type":"function","name":"resolveWithGateways","stateMutability":"view","inputs":[{"type":"bytes"},{"type":"bytes"},{"type":"string[]"}],"outputs":[{"type":"bytes"},{"type":"address"}]},
{"type":"function","name":"addr","stateMutability":"view","inputs":[{"type":"bytes32"}],"outputs":[{"type":"address"}]},
{"type":"function","name":"addr","stateMutability":"view","inputs":[{"type":"bytes32"},{"type":"uint256"}],"outputs":[{"type":"bytes"}]},
{"type":"function","name":"text","stateMutability":"view","inputs":[{"type":"bytes32"},{"type":"string"}],"outputs":[{"type":"string"}]}
]`

var ensRecordABI = sync.OnceValues(func() (abi.ABI, error) { return abi.Parse(ensRecordsABI) })

// GetEnsAddress resolves an ENS-normalized name at the event or selected block.
// It returns a checksummed Ethereum address by default, raw hex for an explicit
// coin type, and nil for a missing record or an all-zero address.
func (c *Client) GetEnsAddress(ctx context.Context, name string, options ...ENSNameOptions) (*string, error) {
	return c.resolveENSRecord(ctx, name, nil, options)
}

// GetEnsText returns a named text record for an ENS-normalized name at the
// event or selected block. Missing and empty text records return nil.
func (c *Client) GetEnsText(ctx context.Context, name, key string, options ...ENSNameOptions) (*string, error) {
	return c.resolveENSRecord(ctx, name, &key, options)
}

func (c *Client) resolveENSRecord(ctx context.Context, name string, textKey *string, options []ENSNameOptions) (*string, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("ENS record lookup accepts at most one options object")
	}
	var opt ENSNameOptions
	if len(options) == 1 {
		opt = options[0]
	}
	tag, err := c.blockTag(ctx, []BlockOptions{opt.BlockOptions})
	if err != nil {
		return nil, err
	}
	resolver, err := c.deploymentAddress(ctx, opt.BlockOptions, opt.UniversalResolverAddress, c.ensResolver, "ENS universal resolver")
	if err != nil || !c.allowsENSName(name) {
		return nil, err
	}
	parsed, err := ensRecordABI()
	if err != nil {
		return nil, err
	}
	inner := ContractCall{ABI: parsed, FunctionName: "addr(bytes32)", Args: []any{ensNamehash(name)}}
	switch {
	case textKey != nil:
		inner.FunctionName = "text"
		inner.Args = append(inner.Args, *textKey)
	case opt.CoinType != nil:
		if opt.CoinType.Sign() < 0 || opt.CoinType.BitLen() > 256 {
			return nil, fmt.Errorf("invalid ENS coin type")
		}
		inner.FunctionName = "addr(bytes32,uint256)"
		inner.Args = append(inner.Args, opt.CoinType)
	}
	innerData, record, err := encodeContractCall(inner)
	if err != nil {
		return nil, err
	}
	args := []any{ensDNSPacket(name), innerData, ensGateways(opt.GatewayURLs)}
	data, outer, err := encodeContractCall(ContractCall{ABI: parsed, FunctionName: "resolveWithGateways", Args: args})
	if err != nil {
		return nil, err
	}
	raw, err := c.rpcRequest(ctx, "eth_call", []any{Object{"to": resolver, "data": data}, tag}, cacheStateAt(tag))
	if err != nil {
		if !opt.Strict {
			errorsABI, parseErr := ensReverseABI()
			if parseErr != nil {
				return nil, parseErr
			}
			if isNullENSError(err, errorsABI) {
				return nil, nil
			}
		}
		return nil, err
	}
	encoded, err := decodeHexResponse(raw)
	if err != nil {
		return nil, err
	}
	result, err := decodeContractResult(outer, encoded)
	if err != nil {
		return nil, err
	}
	fields, ok := result.([]any)
	if !ok || len(fields) != 2 {
		return nil, fmt.Errorf("invalid ENS universal resolver output")
	}
	payload, ok := fields[0].(string)
	if !ok {
		return nil, fmt.Errorf("invalid ENS record data")
	}
	if payload == "0x" {
		return nil, nil
	}
	value, err := decodeContractResult(record, payload)
	if err != nil {
		return nil, err
	}
	s, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("invalid ENS %s record", record.Name)
	}
	if s == "" || (textKey == nil && strings.Trim(strings.TrimPrefix(s, "0x"), "0") == "") {
		return nil, nil
	}
	return &s, nil
}

func ensNamehash(name string) string {
	var node [32]byte
	if name != "" {
		labels := strings.Split(name, ".")
		for i := len(labels) - 1; i >= 0; i-- {
			label := labels[i]
			var hash []byte
			if len(label) == 66 && label[0] == '[' && label[65] == ']' {
				if decoded, err := hex.DecodeString(label[1:65]); err == nil {
					hash = decoded
				}
			}
			if hash == nil {
				hash = crypto.Keccak256([]byte(label))
			}
			node = crypto.Keccak256Hash(node[:], hash)
		}
	}
	return "0x" + hex.EncodeToString(node[:])
}
