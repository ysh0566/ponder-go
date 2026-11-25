package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/filter"
)

// HandleEvent adapts the source-specific event projection to ordinary service dispatch.
func HandleEvent[E any](project func(EventView) (E, error), handler func(context.Context, E, *Context) error) Handler {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, args *HandlerArgs) error {
		if args == nil {
			return fmt.Errorf("missing callback arguments")
		}
		event, err := project(args.Event)
		if err != nil {
			return err
		}
		return handler(ctx, event, args.Context)
	}
}

type eventFields struct {
	raw   Object
	proxy *PropertyView
}

func eventComponent(v EventView, key string) (eventFields, error) {
	if p, ok := v.Property(key); ok && p != nil {
		return eventFields{proxy: p}, nil
	}
	if raw, ok := v.Get(key).(Object); ok && raw != nil {
		return eventFields{raw: raw}, nil
	}
	return eventFields{}, fmt.Errorf("missing event %s", key)
}

func readEventField[T any](v eventFields, key string, optional bool) (T, error) {
	var result T
	var value any
	if v.proxy != nil {
		var err error
		value, err = v.proxy.Get(key)
		if err != nil {
			return result, err
		}
	} else {
		value = v.raw[key]
	}
	if value == nil || filter.IsUndefined(value) {
		if optional {
			return result, nil
		}
		return result, fmt.Errorf("missing event property %q", key)
	}
	if n, ok := value.(*big.Int); ok {
		if n == nil {
			return result, fmt.Errorf("invalid event integer %q", key)
		}
		if result, ok := any(new(big.Int).Set(n)).(T); ok {
			return result, nil
		}
	}
	if typed, ok := value.(T); ok {
		return typed, nil
	}
	// The same property can be read from a normalized native object or a JSON
	// frame. Preserve integer precision and validate list and optional shapes.
	data, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(data, &result)
	}
	if err != nil {
		return result, fmt.Errorf("event property %q: %w", key, err)
	}
	return result, nil
}

type AccessListEntry struct {
	Address     abi.Hex   `json:"address"`
	StorageKeys []abi.Hex `json:"storageKeys"`
}

// EventTransfer is the source's native token movement.
type EventTransfer struct {
	From  abi.Hex  `abi:"from"`
	To    abi.Hex  `abi:"to"`
	Value *big.Int `abi:"value"`
}

func (v EventView) EventID() (string, error) {
	if id, ok := v.Get("id").(string); ok {
		return id, nil
	}
	return "", fmt.Errorf("event ID must be a string")
}

func (v EventView) OptionalReceipt() (*EventReceipt, error) {
	value := v.Get("transactionReceipt")
	if value == nil || filter.IsUndefined(value) {
		return nil, nil
	}
	receipt, err := v.ReceiptFields()
	if err != nil {
		return nil, err
	}
	return &receipt, nil
}

// AbsentABIValue checks a zero-arity function's decoded argument or result.
func (v EventView) AbsentABIValue(key string) (abi.NoValue, error) {
	value := v.Get(key)
	if value == nil || filter.IsUndefined(value) {
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected %s on a zero-arity ABI function", key)
}

// EmptyLogArgs represents the empty tuple of a parameterless log event.
func (v EventView) EmptyLogArgs() ([0]struct{}, error) {
	value := v.Get("args")
	if list, ok := value.([]any); ok && len(list) == 0 {
		return [0]struct{}{}, nil
	}
	if value == nil || filter.IsUndefined(value) {
		return [0]struct{}{}, nil
	}
	return [0]struct{}{}, fmt.Errorf("unexpected arguments on a parameterless log")
}
