// Package finality implements Ponder's timestamp-based finality search.
package finality

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const DefaultReorgWindow = 180

func IsAsyncExecutionChain(id uint64) bool {
	return id == 143 || id == 10143 || id == 43114 || id == 43113
}

type Block struct {
	Hash       string `json:"hash"`
	ParentHash string `json:"parentHash"`
	Number     string `json:"number"`
	Timestamp  string `json:"timestamp"`
	LogsBloom  string `json:"logsBloom"`
}
type Chain struct {
	ReorgWindow int
	BlockTimeMS *float64
}
type Request struct {
	Number                uint64
	IncludeTransactions   bool
	RetryNullBlockRequest bool
}
type Getter func(context.Context, Request) (*Block, error)

func number(s string) (int64, error) {
	n, e := strconv.ParseInt(strings.TrimPrefix(s, "0x"), 16, 64)
	if e != nil {
		return 0, fmt.Errorf("invalid block quantity %q: %w", s, e)
	}
	return n, nil
}

// GetFinalizedBlock preserves the lowerBound object when finality has not advanced.
// reorgWindow is measured in seconds; blockTime is the upstream millisecond hint.
func GetFinalizedBlock(ctx context.Context, chain Chain, get Getter, latest, lowerBound *Block) (*Block, error) {
	if latest == nil {
		return nil, fmt.Errorf("latest block is required")
	}
	if chain.ReorgWindow == 0 {
		return latest, nil
	}
	timestamp, e := number(latest.Timestamp)
	if e != nil {
		return nil, e
	}
	target := timestamp - int64(chain.ReorgWindow)
	low := int64(0)
	if lowerBound != nil {
		ts, e := number(lowerBound.Timestamp)
		if e != nil {
			return nil, e
		}
		if ts > target {
			return lowerBound, nil
		}
		n, e := number(lowerBound.Number)
		if e != nil {
			return nil, e
		}
		low = n + 1
	}
	high, e := number(latest.Number)
	if e != nil {
		return nil, e
	}
	finalized := lowerBound
	seconds := 1.0
	if chain.BlockTimeMS != nil && *chain.BlockTimeMS > 0 {
		seconds = *chain.BlockTimeMS / 1000
	}
	estimate := math.Ceil(float64(chain.ReorgWindow) / seconds)
	blockNumber := low
	if estimate <= float64(high) {
		blockNumber = max(low, min(high-int64(estimate), high))
	}
	for low <= high {
		block, e := get(ctx, Request{Number: uint64(blockNumber), IncludeTransactions: false, RetryNullBlockRequest: true})
		if e != nil {
			return nil, e
		}
		if block == nil {
			return nil, fmt.Errorf("block %d is unavailable", blockNumber)
		}
		ts, e := number(block.Timestamp)
		if e != nil {
			return nil, e
		}
		if ts <= target {
			finalized = block
			low = blockNumber + 1
		} else {
			high = blockNumber - 1
		}
		if low > high {
			break
		}
		blockNumber = low + (high-low)/2
	}
	if finalized == nil {
		return get(ctx, Request{Number: 0, IncludeTransactions: false, RetryNullBlockRequest: true})
	}
	return finalized, nil
}
