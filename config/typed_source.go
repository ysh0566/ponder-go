package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"

	"ponder.local/ponder-go/filter"
)

// SourceUndefinedBound and SourceLatestBound retain source tags without using
// arbitrary strings. Numeric values, including NaN, are validated at runtime.
type SourceUndefinedBound struct{}
type SourceLatestBound struct{}
type SourceBlockBound interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64 |
		SourceUndefinedBound | SourceLatestBound
}

// SourceOption separates an omitted property from an explicit zero/null value.
type SourceOption[T any] struct {
	Value   T
	Present bool
}

func SourceSome[T any](value T) SourceOption[T] { return SourceOption[T]{Value: value, Present: true} }

// SourceChainSelection represents a shortcut or ordered per-chain overrides.
// Membership in a particular app's chain-name set remains a separate type
// obligation; these native constructors still validate names at runtime.
type SourceChainSelection interface{ sourceSelection() any }
type sourceChainName string

func (name sourceChainName) sourceSelection() any    { return string(name) }
func SourceOnChain(name string) SourceChainSelection { return sourceChainName(name) }

type SourceChainOverride struct {
	Name                       string
	Address                    SourceOption[any]
	StartBlock                 SourceOption[any]
	EndBlock                   SourceOption[any]
	Filter                     SourceOption[any]
	IncludeTransactionReceipts SourceOption[bool]
	IncludeCallTraces          SourceOption[bool]
}
type sourceChainOverrides []SourceChainOverride

func SourceOnChains(overrides ...SourceChainOverride) SourceChainSelection {
	return sourceChainOverrides(append([]SourceChainOverride{}, overrides...))
}
func (overrides sourceChainOverrides) sourceSelection() any {
	object := sourceNewObject()
	for _, override := range overrides {
		value := sourceNewObject()
		sourceSetOption(value, "address", override.Address)
		sourceSetOption(value, "startBlock", override.StartBlock)
		sourceSetOption(value, "endBlock", override.EndBlock)
		sourceSetOption(value, "filter", override.Filter)
		sourceSetOption(value, "includeTransactionReceipts", override.IncludeTransactionReceipts)
		sourceSetOption(value, "includeCallTraces", override.IncludeCallTraces)
		object.set(override.Name, value)
	}
	return object
}

// SourceTypedContract deliberately retains the inferred bound types. The data
// container is unconstrained so ignored-error legacy data can be represented;
// the production constructor and both builder consumers enforce the bounds.
type SourceTypedContract[S, E any] struct {
	Chain                      SourceChainSelection
	ABI                        json.RawMessage
	Address                    SourceOption[any]
	StartBlock                 S
	EndBlock                   E
	Filter                     SourceOption[any]
	IncludeTransactionReceipts SourceOption[bool]
	IncludeCallTraces          SourceOption[bool]
}
type SourceTypedContractShape[S, E any] interface {
	~struct {
		Chain                      SourceChainSelection
		ABI                        json.RawMessage
		Address                    SourceOption[any]
		StartBlock                 S
		EndBlock                   E
		Filter                     SourceOption[any]
		IncludeTransactionReceipts SourceOption[bool]
		IncludeCallTraces          SourceOption[bool]
	}
}

// SourceTypedConfiguration binds one typed contract into a native source app.
// Other source kinds/contracts in Base remain available to the real builder.
// Chain/event literal membership and a complete generated app DSL are pending.
type SourceTypedConfiguration[C any] struct {
	Base     *SourceConfiguration
	Name     string
	Contract C
}

func CreateTypedSourceConfiguration[S, E SourceBlockBound, C SourceTypedContractShape[S, E]](base *SourceConfiguration, name string, contract C) SourceTypedConfiguration[C] {
	return SourceTypedConfiguration[C]{Base: base, Name: name, Contract: contract}
}

func sourceSetOption[T any](object *sourceObject, key string, option SourceOption[T]) {
	if option.Present {
		object.set(key, sourceLiftNative(option.Value))
	}
}
func sourceLiftNative(value any) any {
	if value == nil || filter.IsUndefined(value) {
		return value
	}
	switch value.(type) {
	case SourceUndefinedBound:
		return filter.Undefined
	case SourceLatestBound:
		return "latest"
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return rv.Convert(reflect.TypeOf(float64(0))).Float()
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = sourceLiftNative(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return value
		}
		keys := rv.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		out := sourceNewObject()
		for _, key := range keys {
			out.set(key.String(), sourceLiftNative(rv.MapIndex(key).Interface()))
		}
		return out
	}
	return value
}
func sourceTypedConfigurationValue[S, E SourceBlockBound, C SourceTypedContractShape[S, E]](typed SourceTypedConfiguration[C]) (*SourceConfiguration, error) {
	if typed.Base == nil || typed.Base.value == nil {
		return nil, fmt.Errorf("typed source configuration requires a base configuration")
	}
	if typed.Name == "" {
		return nil, fmt.Errorf("typed source contract requires a name")
	}
	fields := reflect.ValueOf(typed.Contract)
	selection, _ := fields.FieldByName("Chain").Interface().(SourceChainSelection)
	if selection == nil {
		return nil, fmt.Errorf("typed source contract requires a chain selection")
	}
	rawABI := fields.FieldByName("ABI").Interface().(json.RawMessage)
	decoder := json.NewDecoder(bytes.NewReader(rawABI))
	decoder.UseNumber()
	abiValue, err := sourceReadJSON(decoder)
	if err != nil {
		return nil, fmt.Errorf("typed source ABI: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing typed source ABI data")
	}
	if _, ok := abiValue.([]any); !ok {
		return nil, fmt.Errorf("typed source ABI must be an array")
	}
	contract := sourceRecord("chain", selection.sourceSelection(), "abi", abiValue)
	for _, bound := range []struct{ field, key string }{{"StartBlock", "startBlock"}, {"EndBlock", "endBlock"}} {
		value := sourceLiftNative(fields.FieldByName(bound.field).Interface())
		if !filter.IsUndefined(value) {
			contract.set(bound.key, value)
		}
	}
	sourceSetOption(contract, "address", fields.FieldByName("Address").Interface().(SourceOption[any]))
	sourceSetOption(contract, "filter", fields.FieldByName("Filter").Interface().(SourceOption[any]))
	sourceSetOption(contract, "includeTransactionReceipts", fields.FieldByName("IncludeTransactionReceipts").Interface().(SourceOption[bool]))
	sourceSetOption(contract, "includeCallTraces", fields.FieldByName("IncludeCallTraces").Interface().(SourceOption[bool]))
	root := typed.Base.value.clone()
	contracts := sourceNewObject()
	if existing, ok := root.get("contracts").(*sourceObject); ok {
		contracts = existing.clone()
	}
	contracts.set(typed.Name, contract)
	root.set("contracts", contracts)
	return &SourceConfiguration{value: root}, nil
}

// BuildTypedSourceConfig consumes the same inferred shape/bounds as Create,
// then invokes the actual source configuration builder and native RPC clients.
func BuildTypedSourceConfig[S, E SourceBlockBound, C SourceTypedContractShape[S, E]](typed SourceTypedConfiguration[C], options SourceBuildOptions) (*SourceConfigBuild, error) {
	source, err := sourceTypedConfigurationValue[S, E](typed)
	if err != nil {
		return nil, err
	}
	return BuildSourceConfig(source, options)
}

// SafeBuildTypedSourceIndexing preserves native value/error handling while
// checking the same inferred shape/bounds at its own consumer boundary.
func SafeBuildTypedSourceIndexing[S, E SourceBlockBound, C SourceTypedContractShape[S, E]](ctx context.Context, typed SourceTypedConfiguration[C], built *SourceConfigBuild, functions []SourceIndexingFunction) (*SourceIndexingBuild, error) {
	source, err := sourceTypedConfigurationValue[S, E](typed)
	if err != nil {
		return nil, err
	}
	return BuildSourceIndexing(ctx, source, built, functions)
}
