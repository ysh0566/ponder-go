package indexing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// BlockOptions shares Ponder's event-relative and immutable block selection.
type BlockOptions = BalanceOptions

type ReadContractOptions struct {
	BlockOptions
	// Nil means retry, matching the source default. False disables empty-data retries.
	RetryEmptyResponse *bool
}

func (c *Client) blockTag(ctx context.Context, options []BlockOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(options) > 1 {
		return "", fmt.Errorf("action accepts at most one options object")
	}
	c.set.mu.RLock()
	defer c.set.mu.RUnlock()
	if c.set.closed {
		return "", fmt.Errorf("indexing client is closed")
	}
	var opt BlockOptions
	if len(options) == 1 {
		opt = options[0]
	}
	if opt.Cache == "immutable" {
		return "latest", nil
	}
	n := opt.BlockNumber
	if n == nil {
		var value any
		event := c.set.event
		if event["type"] == "setup" {
			value = event["block"]
		} else if data, ok := event["event"].(map[string]any); ok {
			if block, ok := data["block"].(map[string]any); ok {
				value = block["number"]
			}
		}
		var err error
		n, err = clientInteger(value)
		if err != nil {
			return "", fmt.Errorf("action requires event block: %w", err)
		}
	}
	if n.Sign() < 0 {
		return "", fmt.Errorf("negative action block number")
	}
	return "0x" + n.Text(16), nil
}

// cacheKey retains the established adapter envelope. JSON map keys are sorted
// recursively by encoding/json, and request values are lowercased just like the
// source cache key. The fixed method spelling in the envelope is an adapter
// protocol detail; the source persistent adapter applies its own canonical hash.
func (c *Client) cacheKey(method string, params any) (string, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	key, err := json.Marshal([]any{c.chainID, method, json.RawMessage(strings.ToLower(string(raw)))})
	return string(key), err
}
func cacheableResponse(raw json.RawMessage) bool {
	return string(raw) != "null" && string(raw) != `"0x"` && len(raw) > 0
}
func (c *Client) cached(ctx context.Context, method string, params any) (json.RawMessage, bool, error) {
	if !c.cacheEnabled || c.cache == nil {
		return nil, false, nil
	}
	key, err := c.cacheKey(method, params)
	if err != nil {
		return nil, false, err
	}
	return c.cache.CacheGet(ctx, key)
}
func (c *Client) cacheResponse(ctx context.Context, method string, params any, raw json.RawMessage) {
	if !c.cacheEnabled || c.cache == nil || !cacheableResponse(raw) {
		return
	}
	key, err := c.cacheKey(method, params)
	if err != nil {
		return
	}
	c.set.putAsync(ctx, c.cache, key, raw)
}
func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, hit, err := c.cached(ctx, method, params)
	if err != nil || hit {
		return raw, err
	}
	if err = c.requester.Call(ctx, method, params, &raw); err != nil {
		return nil, err
	}
	c.cacheResponse(ctx, method, params, raw)
	return raw, nil
}
func decodeHexResponse(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if !strings.HasPrefix(value, "0x") {
		return "", fmt.Errorf("invalid RPC hex data %q", value)
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return "", fmt.Errorf("invalid RPC hex data: %w", err)
	}
	return value, nil
}

// GetCode returns nil for an absent contract (the source action returns undefined).
func (c *Client) GetCode(ctx context.Context, address string, options ...BlockOptions) (*string, error) {
	tag, err := c.blockTag(ctx, options)
	if err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, "eth_getCode", []any{address, tag})
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	value, err := decodeHexResponse(raw)
	if err != nil {
		return nil, err
	}
	if value == "0x" {
		return nil, nil
	}
	return &value, nil
}
func (c *Client) GetStorageAt(ctx context.Context, address, slot string, options ...BlockOptions) (string, error) {
	tag, err := c.blockTag(ctx, options)
	if err != nil {
		return "", err
	}
	raw, err := c.request(ctx, "eth_getStorageAt", []any{address, slot, tag})
	if err != nil {
		return "", err
	}
	return decodeHexResponse(raw)
}

func retryEmpty[T any](ctx context.Context, enabled *bool, action func() (T, error)) (T, error) {
	return retryEmptyWithWait(ctx, enabled, action, func(ctx context.Context, duration time.Duration) error {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	})
}

// The wait dependency keeps the retry policy separately testable; production
// always uses the real cancellable timer above, not a modified retry schedule.
func retryEmptyWithWait[T any](ctx context.Context, enabled *bool, action func() (T, error), wait func(context.Context, time.Duration) error) (T, error) {
	for attempt := 0; ; attempt++ {
		result, err := action()
		var zero *ContractFunctionZeroDataError
		if err == nil || (!errors.As(err, &zero) && !strings.Contains(err.Error(), "returned no data")) || attempt == 9 || (enabled != nil && !*enabled) {
			return result, err
		}
		if err := wait(ctx, 125*time.Millisecond*time.Duration(1<<attempt)); err != nil {
			var empty T
			return empty, err
		}
	}
}
func (c *Client) ReadContract(ctx context.Context, contract ContractCall, options ...ReadContractOptions) (any, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("readContract accepts at most one options object")
	}
	var opt ReadContractOptions
	if len(options) == 1 {
		opt = options[0]
	}
	data, selected, err := encodeContractCall(contract)
	if err != nil {
		return nil, err
	}
	return retryEmpty(ctx, opt.RetryEmptyResponse, func() (any, error) {
		tag, err := c.blockTag(ctx, []BlockOptions{opt.BlockOptions})
		if err != nil {
			return nil, err
		}
		raw, err := c.request(ctx, "eth_call", []any{Object{"to": contract.Address, "data": data}, tag})
		if err != nil {
			return nil, err
		}
		value, err := decodeHexResponse(raw)
		if err != nil {
			return nil, err
		}
		return decodeContractResult(selected, value)
	})
}
