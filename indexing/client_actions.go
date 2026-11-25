package indexing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ysh0566/ponder-go/rpc"
)

// BlockOptions shares Ponder's event-relative and immutable block selection.
type BlockOptions = BalanceOptions

type ReadContractOptions struct {
	BlockOptions
	CCIPRead CCIPReadOptions
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

// deploymentAddress resolves shared chain metadata at the requested block.
func (c *Client) deploymentAddress(ctx context.Context, opts BlockOptions, explicit string, deployment Object, name string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	address, _ := deployment["address"].(string)
	if address == "" {
		return "", fmt.Errorf("%s is unavailable for chain %v", name, c.chainID)
	}
	created := deployment["blockCreated"]
	if created == nil {
		return address, nil
	}
	tag, err := c.blockTag(ctx, []BlockOptions{opts})
	if err != nil {
		return "", err
	}
	if tag == "latest" {
		return address, nil
	}
	first, err := clientInteger(created)
	if err != nil {
		return "", fmt.Errorf("invalid %s deployment block: %w", name, err)
	}
	block, err := clientInteger(tag)
	if err != nil {
		return "", err
	}
	if block.Cmp(first) < 0 {
		return "", fmt.Errorf("%s was deployed at block %s", name, first)
	}
	return address, nil
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
	ctx, finish, admissionErr := c.set.managedRoots.begin(ctx)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer finish()
	raw, hit, err := c.prefetchedResult(ctx, method, params, false)
	if err != nil || hit {
		return raw, err
	}
	raw, hit, err = c.cached(ctx, method, params)
	if err != nil || hit {
		return raw, err
	}
	raw, err = c.sendRPC(ctx, method, params)
	if err != nil {
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

func retryClientAction[T any](ctx context.Context, c *Client, retry *bool, read func(context.Context) (T, error)) (T, error) {
	ctx, finish, err := c.set.managedRoots.begin(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	defer finish()
	return retryEmpty(ctx, retry, func() (T, error) { return read(ctx) })
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

func isEmptyActionResponse(err error) bool {
	var zero *ContractFunctionZeroDataError
	var receipt *rpc.TransactionReceiptNotFoundError
	return errors.Is(err, rpc.ErrBlockNotFound) || errors.Is(err, ErrTransactionNotFound) ||
		errors.As(err, &receipt) || errors.As(err, &zero) || strings.Contains(err.Error(), "returned no data")
}

// The wait dependency keeps the retry policy separately testable; production
// always uses the real cancellable timer above, not a modified retry schedule.
func retryEmptyWithWait[T any](ctx context.Context, enabled *bool, action func() (T, error), wait func(context.Context, time.Duration) error) (T, error) {
	for attempt := 0; ; attempt++ {
		result, err := action()
		if err == nil || !isEmptyActionResponse(err) || attempt == 9 || (enabled != nil && !*enabled) {
			return result, err
		}
		if err := wait(ctx, 125*time.Millisecond*time.Duration(1<<attempt)); err != nil {
			var empty T
			return empty, err
		}
	}
}
func (c *Client) ReadContract(ctx context.Context, contract ContractCall, options ...ReadContractOptions) (any, error) {
	ctx, finish, admissionErr := c.set.managedRoots.begin(ctx)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer finish()
	if len(options) > 1 {
		return nil, fmt.Errorf("readContract accepts at most one options object")
	}
	var opt ReadContractOptions
	if len(options) == 1 {
		opt = options[0]
	}
	return retryEmpty(ctx, opt.RetryEmptyResponse, func() (any, error) {
		if err := c.recordContracts([]ContractCall{contract}, opt.BlockOptions, false); err != nil {
			return nil, err
		}
		data, selected, err := encodeContractCall(contract)
		if err != nil {
			return nil, err
		}
		tag, err := c.blockTag(ctx, []BlockOptions{opt.BlockOptions})
		if err != nil {
			return nil, err
		}
		tx := Object{"to": contract.Address, "data": data}
		raw, err := c.readEVM(ctx, tx, []any{tx, tag}, true, opt.CCIPRead)
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
