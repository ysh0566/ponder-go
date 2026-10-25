package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

type SourceBlock map[string]any
type SourceLog map[string]any
type SourceReceipt map[string]any
type SourceRawTrace map[string]any

// Each historical method has its own response family. A broad-result callback
// cannot replace Logs (or another method) without narrowing its return value.
// Remaining RPC methods delegate to the original requester; this is not the
// entire source generic RPC schema.
type SourceHistoricalOverrides struct {
	Block         func(context.Context, string, bool) (SourceBlock, error)
	Logs          func(context.Context, map[string]any) ([]SourceLog, error)
	Traces        func(context.Context, string, map[string]any) ([]SourceRawTrace, error)
	Receipt       func(context.Context, string) (SourceReceipt, error)
	BlockReceipts func(context.Context, string) ([]SourceReceipt, error)
}
type SourceHistoricalProvider struct {
	base        Requester
	overrides   SourceHistoricalOverrides
	actionsOnce sync.Once
	actions     *Actions
}

// Actions retains pagination learning and receipt fallback on this RPC object,
// including when independent historical synchronizers share it.
func (p *SourceHistoricalProvider) Actions() *Actions {
	if p == nil {
		return nil
	}
	p.actionsOnce.Do(func() { p.actions = NewActions(p) })
	return p.actions
}

func NewSourceHistoricalProvider(base Requester, overrides SourceHistoricalOverrides) (*SourceHistoricalProvider, error) {
	if base == nil {
		return nil, fmt.Errorf("historical base RPC required")
	}
	return &SourceHistoricalProvider{base: base, overrides: overrides}, nil
}
func (p *SourceHistoricalProvider) Block(ctx context.Context, number string, transactions bool) (SourceBlock, error) {
	if p.overrides.Block != nil {
		return p.overrides.Block(ctx, number, transactions)
	}
	var result SourceBlock
	err := p.base.Call(ctx, "eth_getBlockByNumber", []any{number, transactions}, &result)
	return result, err
}
func (p *SourceHistoricalProvider) Logs(ctx context.Context, query map[string]any) ([]SourceLog, error) {
	if p.overrides.Logs != nil {
		return p.overrides.Logs(ctx, query)
	}
	var result []SourceLog
	err := p.base.Call(ctx, "eth_getLogs", []any{query}, &result)
	return result, err
}
func (p *SourceHistoricalProvider) Traces(ctx context.Context, number string, options map[string]any) ([]SourceRawTrace, error) {
	if p.overrides.Traces != nil {
		return p.overrides.Traces(ctx, number, options)
	}
	var result []SourceRawTrace
	err := p.base.Call(ctx, "debug_traceBlockByNumber", []any{number, options}, &result)
	return result, err
}
func (p *SourceHistoricalProvider) Receipt(ctx context.Context, hash string) (SourceReceipt, error) {
	if p.overrides.Receipt != nil {
		return p.overrides.Receipt(ctx, hash)
	}
	var result SourceReceipt
	err := p.base.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &result)
	return result, err
}
func (p *SourceHistoricalProvider) BlockReceipts(ctx context.Context, hash string) ([]SourceReceipt, error) {
	if p.overrides.BlockReceipts != nil {
		return p.overrides.BlockReceipts(ctx, hash)
	}
	var result []SourceReceipt
	err := p.base.Call(ctx, "eth_getBlockReceipts", []any{hash}, &result)
	return result, err
}

func sourceHistoricalAssign(out, value any) error {
	target := reflect.ValueOf(out)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return fmt.Errorf("historical RPC output must be a pointer")
	}
	target = target.Elem()
	source := reflect.ValueOf(value)
	if !source.IsValid() {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	if source.Type().ConvertibleTo(target.Type()) {
		target.Set(source.Convert(target.Type()))
		return nil
	}
	if source.Kind() == reflect.Slice && target.Kind() == reflect.Slice {
		if source.IsNil() {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		result := reflect.MakeSlice(target.Type(), source.Len(), source.Len())
		compatible := true
		for i := 0; i < source.Len(); i++ {
			entry := source.Index(i)
			if !entry.Type().ConvertibleTo(target.Type().Elem()) {
				compatible = false
				break
			}
			result.Index(i).Set(entry.Convert(target.Type().Elem()))
		}
		if compatible {
			target.Set(result)
			return nil
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func (p *SourceHistoricalProvider) Call(ctx context.Context, method string, params, out any) error {
	if p == nil || p.base == nil {
		return fmt.Errorf("historical base RPC required")
	}
	if method != "eth_getBlockByNumber" && method != "eth_getLogs" && method != "debug_traceBlockByNumber" && method != "eth_getTransactionReceipt" && method != "eth_getBlockReceipts" {
		return p.base.Call(ctx, method, params, out)
	}
	values, ok := params.([]any)
	if !ok {
		return fmt.Errorf("historical RPC parameters must be a tuple")
	}
	var result any
	var err error
	text := func(index int) (string, error) {
		if index >= len(values) {
			return "", fmt.Errorf("historical RPC parameter missing")
		}
		value, ok := values[index].(string)
		if !ok {
			return "", fmt.Errorf("historical RPC string parameter required")
		}
		return value, nil
	}
	switch method {
	case "eth_getBlockByNumber":
		if len(values) != 2 {
			return fmt.Errorf("block RPC tuple requires two parameters")
		}
		number, failure := text(0)
		if failure != nil {
			return failure
		}
		transactions, ok := values[1].(bool)
		if !ok {
			return fmt.Errorf("block RPC transaction flag required")
		}
		result, err = p.Block(ctx, number, transactions)
	case "eth_getLogs":
		if len(values) != 1 {
			return fmt.Errorf("logs RPC tuple requires one parameter")
		}
		query, ok := values[0].(map[string]any)
		if !ok {
			return fmt.Errorf("logs RPC query object required")
		}
		result, err = p.Logs(ctx, query)
	case "debug_traceBlockByNumber":
		if len(values) != 2 {
			return fmt.Errorf("trace RPC tuple requires two parameters")
		}
		number, failure := text(0)
		if failure != nil {
			return failure
		}
		options, ok := values[1].(map[string]any)
		if !ok {
			return fmt.Errorf("trace RPC options required")
		}
		result, err = p.Traces(ctx, number, options)
	case "eth_getTransactionReceipt", "eth_getBlockReceipts":
		if len(values) != 1 {
			return fmt.Errorf("receipt RPC tuple requires one parameter")
		}
		hash, failure := text(0)
		if failure != nil {
			return failure
		}
		if method == "eth_getBlockReceipts" {
			result, err = p.BlockReceipts(ctx, hash)
		} else {
			result, err = p.Receipt(ctx, hash)
		}
	}
	if err != nil {
		return err
	}
	return sourceHistoricalAssign(out, result)
}
