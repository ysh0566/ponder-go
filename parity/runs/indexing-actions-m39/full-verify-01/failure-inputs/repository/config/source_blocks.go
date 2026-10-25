package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/finality"
	"ponder.local/ponder-go/rpc"
)

func sourceHeader(ctx context.Context, client *rpc.Client, number string) (*sourceObject, error) {
	var raw json.RawMessage
	if err := client.Call(ctx, "eth_getBlockByNumber", []any{number, false}, &raw); err != nil {
		return nil, err
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, fmt.Errorf("Block at number \"%s\" could not be found.", number)
	}
	parsed, err := ParseSourceConfiguration(raw)
	if err != nil {
		return nil, err
	}
	block := parsed.value
	for _, name := range []string{"hash", "number", "timestamp", "logsBloom", "parentHash"} {
		if filter.IsUndefined(block.get(name)) {
			return nil, fmt.Errorf("Invalid RPC response: 'block.%s' is a required property", name)
		}
	}
	zeroHash := "0x" + strings.Repeat("0", 64)
	defaults := []struct{ name, value string }{
		{"miner", "0x" + strings.Repeat("0", 40)}, {"gasUsed", "0x0"}, {"gasLimit", "0x0"}, {"baseFeePerGas", "0x0"},
		{"nonce", "0x0"}, {"mixHash", zeroHash}, {"stateRoot", zeroHash}, {"transactionsRoot", zeroHash}, {"sha3Uncles", zeroHash},
		{"size", "0x0"}, {"difficulty", "0x0"}, {"totalDifficulty", "0x0"}, {"extraData", "0x"},
	}
	for _, field := range defaults {
		if filter.IsUndefined(block.get(field.name)) {
			block.set(field.name, field.value)
		}
	}
	maximum := new(big.Int).SetUint64(9223372036854775807)
	for _, name := range []string{"number", "timestamp"} {
		value, ok := new(big.Int).SetString(strings.TrimPrefix(sourceString(block.get(name)), "0x"), 16)
		if !ok {
			return nil, fmt.Errorf("invalid block %s quantity", name)
		}
		if value.Cmp(maximum) > 0 {
			return nil, fmt.Errorf("Invalid RPC response: 'block.%s' (%s) is larger than the maximum allowed value (%s).", name, value, maximum)
		}
	}
	block.set("transactions", filter.Undefined)
	return block, nil
}

func sourceFinalized(ctx context.Context, chain *sourceObject, client *rpc.Client, latest *sourceObject) (*sourceObject, error) {
	blocks := map[string]*sourceObject{}
	view := func(block *sourceObject) *finality.Block {
		hash := sourceString(block.get("hash"))
		blocks[hash] = block
		return &finality.Block{Hash: hash, ParentHash: sourceString(block.get("parentHash")), Number: sourceString(block.get("number")), Timestamp: sourceString(block.get("timestamp")), LogsBloom: sourceString(block.get("logsBloom"))}
	}
	window, _ := sourceNumber(chain.get("reorgWindow"))
	options := finality.Chain{ReorgWindow: int(window)}
	if metadata, ok := chain.get("viemChain").(*sourceObject); ok {
		if hint, ok := sourceNumber(metadata.get("blockTime")); ok {
			options.BlockTimeMS = &hint
		}
	}
	result, err := finality.GetFinalizedBlock(ctx, options, func(ctx context.Context, request finality.Request) (*finality.Block, error) {
		block, err := sourceHeader(ctx, client, fmt.Sprintf("0x%x", request.Number))
		if err != nil {
			return nil, err
		}
		return view(block), nil
	}, view(latest), nil)
	if err != nil {
		return nil, err
	}
	return blocks[result.Hash], nil
}

func sourceResolveBlock(value any, latest *sourceObject) (any, error) {
	if filter.IsUndefined(value) {
		return filter.Undefined, nil
	}
	if number, ok := sourceNumber(value); ok && number != number {
		return filter.Undefined, nil
	}
	if text, ok := value.(string); ok && text == "latest" {
		integer, err := strconv.ParseUint(strings.TrimPrefix(sourceString(latest.get("number")), "0x"), 16, 64)
		return float64(integer), err
	}
	return value, nil
}
