package rpc

import (
	"context"
	"errors"
	"fmt"
)

type CallTrace struct {
	TransactionHash string         `json:"transactionHash"`
	Trace           map[string]any `json:"trace"`
}

// FlattenCallTraces preserves original sibling positions and excludes failed
// frames with their entire subtree. It does not assign matched-event indexes.
func FlattenCallTraces(input []map[string]any) ([]CallTrace, error) {
	result := []CallTrace{}
	var walk func(map[string]any, string, []int) error
	walk = func(frame map[string]any, hash string, path []int) error {
		if _, failed := frame["error"]; failed {
			return nil
		}
		for _, key := range []string{"type", "from", "input"} {
			if _, ok := frame[key]; !ok {
				return fmt.Errorf("Invalid RPC response: 'trace.%s' is a required property", key)
			}
		}
		out := frame
		out["traceAddress"] = append([]int{}, path...)
		for _, key := range []string{"gas", "gasUsed"} {
			if _, ok := out[key]; !ok {
				out[key] = "0x0"
			}
		}
		result = append(result, CallTrace{hash, out})
		if raw, ok := frame["calls"]; ok {
			calls, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("invalid trace calls")
			}
			for i, value := range calls {
				child, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid child trace")
				}
				next := append(append([]int{}, path...), i)
				if e := walk(child, hash, next); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for _, original := range input {
		entry := cloneTraceValue(original).(map[string]any)
		hash, ok := entry["txHash"].(string)
		if !ok {
			return nil, fmt.Errorf("Invalid RPC response: 'trace.transactionHash' is a required property")
		}
		frame, ok := entry["result"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid call tracer result")
		}
		if e := walk(frame, hash, []int{}); e != nil {
			return nil, e
		}
	}
	return result, nil
}

// TraceBlock tries the source block-hash method first. For older providers it
// has an explicit native fallback to hash-addressed per-transaction callTracer.
// This fallback is supplementary compatibility, never original oracle credit.
func (c *Client) TraceBlock(ctx context.Context, hash string, transactions []map[string]any) ([]CallTrace, error) {
	var response []map[string]any
	e := c.CallCached(ctx, "debug_traceBlockByHash", []any{hash, map[string]any{"tracer": "callTracer"}}, &response)
	if e == nil {
		if response == nil {
			return nil, fmt.Errorf("Received invalid empty debug_traceBlockByHash response.")
		}
		return FlattenCallTraces(response)
	}
	var remote *Error
	if !errors.As(e, &remote) || remote.Code != -32601 {
		return nil, e
	}
	response = []map[string]any{}
	for _, tx := range transactions {
		txHash, ok := tx["hash"].(string)
		if !ok {
			return nil, fmt.Errorf("transaction hash required for trace fallback")
		}
		if blockHash, ok := tx["blockHash"].(string); ok && blockHash != hash {
			return nil, fmt.Errorf("trace fallback transaction belongs to a different block")
		}
		var frame map[string]any
		if e = c.CallCached(ctx, "debug_traceTransaction", []any{txHash, map[string]any{"tracer": "callTracer"}}, &frame); e != nil {
			return nil, e
		}
		if frame == nil {
			return nil, fmt.Errorf("Received invalid empty debug_traceTransaction response.")
		}
		response = append(response, map[string]any{"txHash": txHash, "result": frame})
	}
	return FlattenCallTraces(response)
}

func cloneTraceValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		o := map[string]any{}
		for k, v := range x {
			o[k] = cloneTraceValue(v)
		}
		return o
	case []any:
		o := make([]any, len(x))
		for i, v := range x {
			o[i] = cloneTraceValue(v)
		}
		return o
	case []int:
		return append([]int{}, x...)
	}
	return v
}

// TraceBlockByNumber mirrors the source action and intentionally bypasses the
// immutable response cache: a numeric block reference may be replaced by reorg.
func (c *Client) TraceBlockByNumber(ctx context.Context, number string) ([]CallTrace, error) {
	var response []map[string]any
	if e := c.Call(ctx, "debug_traceBlockByNumber", []any{number, map[string]any{"tracer": "callTracer"}}, &response); e != nil {
		return nil, e
	}
	if response == nil {
		return nil, fmt.Errorf("Received invalid empty debug_traceBlockByNumber response.")
	}
	return FlattenCallTraces(response)
}
