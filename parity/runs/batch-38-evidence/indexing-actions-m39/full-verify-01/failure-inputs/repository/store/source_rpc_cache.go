package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ponder.local/ponder-go/rpc"
)

// SourceRPCRawResult retains the actual source runtime request. The source tests
// deliberately pass a string in eth_call's transaction position despite TS errors.
type SourceRPCRawResult struct {
	Request     any
	BlockNumber *uint64
	Result      string
}
type SourceRPCResult struct {
	Request     rpc.SourceRequest
	BlockNumber *uint64
	Result      string
}

func (c *ProtocolCache) InsertSourceRPCRequestResults(ctx context.Context, chain uint64, requests []SourceRPCResult) error {
	raw := make([]SourceRPCRawResult, len(requests))
	for i, r := range requests {
		value, err := rpc.SourceRequestValue(r.Request)
		if err != nil {
			return err
		}
		raw[i] = SourceRPCRawResult{value, r.BlockNumber, r.Result}
	}
	return c.InsertSourceRPCRawRequestResults(ctx, chain, raw)
}
func (c *ProtocolCache) InsertSourceRPCRawRequestResults(ctx context.Context, chain uint64, requests []SourceRPCRawResult) error {
	if len(requests) == 0 {
		return nil
	}
	return c.transaction(ctx, func(tx Transaction) error { return c.insertSourceRPC(ctx, tx, chain, requests) })
}
func (c *ProtocolCache) insertSourceRPC(ctx context.Context, q Querier, chain uint64, requests []SourceRPCRawResult) error {
	query, args, err := c.prepareSourceRPC(chain, requests)
	if err != nil {
		return err
	}
	return q.Exec(ctx, query, args...)
}

// prepareSourceRPC constructs the complete immutable SQL row payload before an
// asynchronous adapter write is admitted. It is also the synchronous insert path.
func (c *ProtocolCache) prepareSourceRPC(chain uint64, requests []SourceRPCRawResult) (string, []any, error) {
	values := make([]string, len(requests))
	args := make([]any, 0, len(requests)*4)
	for i, r := range requests {
		hash, e := SourceRPCRequestHash(r.Request)
		if e != nil {
			return "", nil, e
		}
		var block any
		if r.BlockNumber != nil && *r.BlockNumber != 0 {
			block = *r.BlockNumber
		}
		n := len(args)
		values[i] = fmt.Sprintf("($%d,$%d,$%d,$%d)", n+1, n+2, n+3, n+4)
		args = append(args, chain, hash, block, r.Result)
	}
	return "INSERT INTO " + c.table("rpc_request_results") + "(chain_id,request_hash,block_number,result) VALUES" + strings.Join(values, ",") + " ON CONFLICT(chain_id,request_hash) DO NOTHING", args, nil
}
func (c *ProtocolCache) GetSourceRPCRequestResults(ctx context.Context, chain uint64, requests []rpc.SourceRequest) ([]*string, error) {
	raw := make([]any, len(requests))
	for i, r := range requests {
		value, err := rpc.SourceRequestValue(r)
		if err != nil {
			return nil, err
		}
		raw[i] = value
	}
	return c.GetSourceRPCRawRequestResults(ctx, chain, raw)
}
func (c *ProtocolCache) GetSourceRPCRawRequestResults(ctx context.Context, chain uint64, requests []any) ([]*string, error) {
	if len(requests) == 0 {
		return []*string{}, nil
	}
	var out []*string
	err := c.transaction(ctx, func(tx Transaction) error { var e error; out, e = c.getSourceRPC(ctx, tx, chain, requests); return e })
	return out, err
}
func sourceRequestBlock(request any) (*uint64, error) {
	object, ok := request.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("source request must be an object")
	}
	method, _ := object["method"].(string)
	var position int
	switch method {
	case "eth_getBlockByNumber", "eth_getBlockTransactionCountByNumber", "eth_getTransactionByBlockNumberAndIndex", "eth_getUncleByBlockNumberAndIndex", "debug_traceBlockByNumber":
		position = 0
	case "eth_getBalance", "eth_call", "eth_getCode", "eth_estimateGas", "eth_feeHistory", "eth_getTransactionCount":
		position = 1
	case "eth_getProof", "eth_getStorageAt":
		position = 2
	default:
		return nil, nil
	}
	var params []any
	switch p := object["params"].(type) {
	case []any:
		params = p
	case []string:
		params = make([]any, len(p))
		for i, v := range p {
			params[i] = v
		}
	default:
		return nil, fmt.Errorf("source params is not iterable")
	}
	if position >= len(params) {
		return nil, nil
	}
	text, ok := params[position].(string)
	if !ok || !strings.HasPrefix(text, "0x") {
		return nil, nil
	}
	n, e := strconv.ParseUint(text[2:], 16, 64)
	if e != nil {
		return nil, e
	}
	if n > 1<<53-1 {
		return nil, fmt.Errorf("block number exceeds source safe integer")
	}
	return &n, nil
}
func (c *ProtocolCache) getSourceRPC(ctx context.Context, q Querier, chain uint64, requests []any) ([]*string, error) {
	hashes := make([]string, len(requests))
	nonblock := []string{}
	numbers := []uint64{}
	for i, request := range requests {
		n, e := sourceRequestBlock(request)
		if e != nil {
			return nil, e
		}
		hash, e := SourceRPCRequestHash(request)
		if e != nil {
			return nil, e
		}
		hashes[i] = hash
		if n == nil {
			nonblock = append(nonblock, hash)
		} else {
			numbers = append(numbers, *n)
		}
	}
	results := map[string]string{}
	collect := func(query string, args ...any) error {
		rows, e := q.Query(ctx, query, args...)
		if e != nil {
			return e
		}
		for _, r := range rows.Rows {
			results[fmt.Sprint(r["request_hash"])] = fmt.Sprint(r["result"])
		}
		return nil
	}
	byHashes := func(selected []string) error {
		if len(selected) == 0 {
			return nil
		}
		params := make([]any, len(selected)+1)
		params[0] = chain
		slots := make([]string, len(selected))
		for i, hash := range selected {
			params[i+1] = hash
			slots[i] = "$" + strconv.Itoa(i+2)
		}
		return collect("SELECT request_hash,result FROM "+c.table("rpc_request_results")+" WHERE chain_id=$1 AND request_hash IN ("+strings.Join(slots, ",")+")", params...)
	}
	if len(numbers) > 100 {
		low, high := numbers[0], numbers[0]
		for _, n := range numbers {
			low = min(low, n)
			high = max(high, n)
		}
		if e := collect("SELECT request_hash,result FROM "+c.table("rpc_request_results")+" WHERE chain_id=$1 AND block_number >= $2 AND block_number <= $3", chain, low, high); e != nil {
			return nil, e
		}
		if e := byHashes(nonblock); e != nil {
			return nil, e
		}
	} else if e := byHashes(hashes); e != nil {
		return nil, e
	}
	out := make([]*string, len(requests))
	for i, hash := range hashes {
		if value, ok := results[hash]; ok {
			out[i] = &value
		}
	}
	return out, nil
}
func (c *ProtocolCache) PruneSourceRPCRequestResults(ctx context.Context, chain uint64, blocks []uint64) error {
	if len(blocks) == 0 {
		return nil
	}
	return c.transaction(ctx, func(tx Transaction) error { return c.pruneSourceRPC(ctx, tx, chain, blocks) })
}
func (c *ProtocolCache) pruneSourceRPC(ctx context.Context, q Querier, chain uint64, blocks []uint64) error {
	slots := make([]string, len(blocks))
	args := make([]any, len(blocks)+1)
	args[0] = chain
	for i, n := range blocks {
		slots[i] = "$" + strconv.Itoa(i+2)
		args[i+1] = n
	}
	return q.Exec(ctx, "DELETE FROM "+c.table("rpc_request_results")+" WHERE chain_id=$1 AND block_number IN ("+strings.Join(slots, ",")+")", args...)
}
