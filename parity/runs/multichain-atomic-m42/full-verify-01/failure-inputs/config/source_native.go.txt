package config

import (
	"encoding/json"
	"fmt"
	"math"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
)

// SourceFilter is the closed filter union returned by the source builder.
// Callers narrow to SourceBlockFilter before accessing interval-specific data.
type SourceFilter interface {
	Kind() string
	Value() filter.Object
	sourceFilter()
}
type sourceFilterBase struct{ fields filter.Object }

func (f sourceFilterBase) Kind() string                 { return f.fields["type"].(string) }
func (f sourceFilterBase) Value() filter.Object         { return sourceCopyObject(f.fields) }
func (sourceFilterBase) sourceFilter()                  {}
func (f sourceFilterBase) MarshalJSON() ([]byte, error) { return json.Marshal(f.fields) }

type SourceLogFilter struct{ sourceFilterBase }
type SourceTraceFilter struct{ sourceFilterBase }
type SourceTransactionFilter struct{ sourceFilterBase }
type SourceTransferFilter struct{ sourceFilterBase }
type SourceBlockFilter struct {
	sourceFilterBase
	Interval float64
	Offset   float64
}

type SourceEventCallback struct {
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Filter    SourceFilter    `json:"filter"`
	Fn        any             `json:"-"`
	Chain     filter.Object   `json:"chain"`
	ABIItem   json.RawMessage `json:"abiItem,omitempty"`
	Metadata  filter.Object   `json:"metadata,omitempty"`
	Direction string          `json:"direction,omitempty"`
}

func (callback SourceEventCallback) Value() filter.Object {
	value := filter.Object{"name": callback.Name, "type": callback.Type, "filter": callback.Filter.Value(), "fn": callback.Fn, "chain": sourceCopyObject(callback.Chain)}
	if len(callback.ABIItem) > 0 {
		var item any
		_ = json.Unmarshal(callback.ABIItem, &item)
		value["abiItem"] = item
	}
	if callback.Metadata != nil {
		value["metadata"] = sourceCopyObject(callback.Metadata)
	}
	if callback.Direction != "" {
		value["direction"] = callback.Direction
	}
	return value
}
func sourceCopyObject(value filter.Object) filter.Object {
	out := filter.Object{}
	for key, value := range value {
		out[key] = sourceNativeValue(value)
	}
	return out
}
func sourceNativeValue(value any) any {
	switch v := value.(type) {
	case json.Number:
		n, _ := v.Float64()
		return n
	case *sourceObject:
		return sourceCopyObject(sourcePlain(v).(map[string]any))
	case map[string]any:
		return sourceCopyObject(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sourceNativeValue(item)
		}
		return out
	default:
		return value
	}
}
func (build *SourceIndexingBuild) EventCallbacks() [][]SourceEventCallback {
	out := make([][]SourceEventCallback, len(build.callbacks))
	for i, callbacks := range build.callbacks {
		out[i] = []SourceEventCallback{}
		for _, raw := range callbacks {
			fields := sourceNativeValue(raw.get("filter")).(map[string]any)
			base := sourceFilterBase{fields: fields}
			var f SourceFilter
			switch fields["type"] {
			case "log":
				f = SourceLogFilter{base}
			case "trace":
				f = SourceTraceFilter{base}
			case "transaction":
				f = SourceTransactionFilter{base}
			case "transfer":
				f = SourceTransferFilter{base}
			case "block":
				f = SourceBlockFilter{sourceFilterBase: base, Interval: fields["interval"].(float64), Offset: fields["offset"].(float64)}
			}
			callback := SourceEventCallback{Name: sourceString(raw.get("name")), Type: sourceString(raw.get("type")), Filter: f, Fn: raw.get("fn"), Chain: sourceNativeValue(raw.get("chain")).(map[string]any)}
			if !filter.IsUndefined(raw.get("abiItem")) {
				callback.ABIItem, _ = json.Marshal(sourcePlain(raw.get("abiItem")))
			}
			if !filter.IsUndefined(raw.get("metadata")) {
				callback.Metadata = sourceNativeValue(raw.get("metadata")).(map[string]any)
			}
			if direction, ok := raw.get("direction").(string); ok {
				callback.Direction = direction
			}
			out[i] = append(out[i], callback)
		}
	}
	return out
}

// SourceRuntimePlan has its own versioned config/cache identity. Legacy native
// configurations keep their original JSON and factory identifiers unchanged.
type SourceRuntimePlan struct {
	Version          int                           `json:"version"`
	Callbacks        []SourceEventCallback         `json:"callbacks"`
	Factories        map[string]factory.LogFactory `json:"factories"`
	Setup            []SourceIndexingFunction      `json:"setup"`
	Client           *rpc.Client                   `json:"-"`
	CacheRPCRequests bool                          `json:"cacheRpcRequests"`
}

func sourceUint(value any) (uint64, error) {
	if sourceNullish(value) {
		return 0, nil
	}
	number, ok := sourceNumber(value)
	if !ok || !sourceInteger(value) || number < 0 || number > 9007199254740991 {
		return 0, fmt.Errorf("native runtime requires a non-negative safe integer, got %s", sourceString(value))
	}
	return uint64(number), nil
}
func sourceUintPointer(value any) (*uint64, error) {
	if sourceNullish(value) {
		return nil, nil
	}
	n, err := sourceUint(value)
	return &n, err
}

// NativeConfig selects one source chain for the existing native scheduler. It
// preserves compiled callbacks/clients/factory IDs. Multi-chain scheduling is
// still a separate implementation track, so this API requires a chain name.
func (build *SourceIndexingBuild) NativeConfig(name string) (Config, error) {
	selected := -1
	for i, chain := range build.chains {
		if chain.get("name") == name {
			selected = i
			break
		}
	}
	if selected < 0 {
		return Config{}, fmt.Errorf("source chain %q has no registered indexing functions", name)
	}
	rawChain := build.chains[selected]
	id, err := sourceUint(rawChain.get("id"))
	if err != nil {
		return Config{}, err
	}
	urls, err := sourceStrings(rawChain.get("rpc"))
	if err != nil {
		return Config{}, err
	}
	window, _ := sourceNumber(rawChain.get("reorgWindow"))
	polling, _ := sourceNumber(rawChain.get("pollingInterval"))
	w := int(window)
	chain := Chain{Name: name, ID: id, RPC: urls, ReorgWindow: &w, PollingIntervalMS: int(polling)}
	if metadata, ok := rawChain.get("viemChain").(*sourceObject); ok {
		if hint, ok := sourceNumber(metadata.get("blockTime")); ok {
			chain.BlockTimeMS = &hint
		}
	}
	callbacks := build.EventCallbacks()[selected]
	plan := &SourceRuntimePlan{Version: 1, Callbacks: callbacks, Factories: map[string]factory.LogFactory{}, Client: build.base.value.RPCs[build.chainIndexes[selected]], CacheRPCRequests: rawChain.get("cacheRpcRequests") == true}
	for _, setup := range build.setup[selected] {
		plan.Setup = append(plan.Setup, SourceIndexingFunction{Name: sourceString(setup.get("name")), Fn: setup.get("fn")})
	}
	out := Config{Chains: []Chain{chain}, SourcePlan: plan}
	for _, kind := range []string{"contracts", "accounts", "blocks"} {
		for _, source := range build.sources[kind] {
			if source.get("chain") != name {
				continue
			}
			latest := build.latest[build.chainIndexes[selected]]
			from, _ := sourceResolveBlock(source.get("startBlock"), latest)
			to, _ := sourceResolveBlock(source.get("endBlock"), latest)
			start, err := sourceUint(from)
			if err != nil {
				return Config{}, err
			}
			end, err := sourceUintPointer(to)
			if err != nil {
				return Config{}, err
			}
			sourceName := sourceString(source.get("name"))
			if kind == "blocks" {
				interval := sourceDefault(source.get("interval"), float64(1))
				n, _ := sourceNumber(interval)
				if n < 1 || math.Trunc(n) != n {
					return Config{}, fmt.Errorf("native runtime requires a positive block interval, got %s", sourceString(interval))
				}
				out.Blocks = append(out.Blocks, BlockSource{Name: sourceName, Chain: name, StartBlock: start, EndBlock: end, Interval: uint64(n)})
				continue
			}
			address, err := sourceAddress(source, rawChain, latest)
			if err != nil {
				return Config{}, err
			}
			var addresses []string
			var factoryConfig *factory.Config
			switch value := address.(type) {
			case string:
				addresses = []string{value}
			case []any:
				addresses = []string{}
				for _, value := range value {
					addresses = append(addresses, value.(string))
				}
			case *sourceObject:
				fFrom, err := sourceUintPointer(value.get("fromBlock"))
				if err != nil {
					return Config{}, err
				}
				fTo, err := sourceUintPointer(value.get("toBlock"))
				if err != nil {
					return Config{}, err
				}
				fAddress := sourcePlain(value.get("address"))
				if filter.IsUndefined(fAddress) {
					fAddress = nil
				}
				logFactory := factory.LogFactory{ID: sourceString(value.get("id")), Type: "log", ChainID: id, SourceID: sourceName, Address: fAddress, EventSelector: sourceString(value.get("eventSelector")), ChildAddressLocation: sourceString(value.get("childAddressLocation")), FromBlock: fFrom, ToBlock: fTo}
				plan.Factories[sourceName] = logFactory
				factoryRaw := source.get("address").(*sourceObject)
				data, _ := json.Marshal(sourcePlain(factoryRaw.get("event")))
				var event abi.Event
				if err := json.Unmarshal(data, &event); err != nil {
					return Config{}, err
				}
				factoryConfig = &factory.Config{ChainID: id, SourceID: sourceName, Address: fAddress, Event: event, FromBlock: fFrom, ToBlock: fTo}
				if p, ok := factoryRaw.get("parameter").(string); ok {
					factoryConfig.Parameter = &p
				}
				if p, ok := factoryRaw.get("location").(string); ok {
					factoryConfig.Location = &p
				}
			}
			if kind == "contracts" {
				data, err := json.Marshal(sourcePlain(source.get("abi")))
				if err != nil {
					return Config{}, err
				}
				out.Contracts = append(out.Contracts, Contract{Name: sourceName, Chain: name, ABI: data, Address: addresses, Factory: factoryConfig, StartBlock: start, EndBlock: end, IncludeCallTraces: source.get("includeCallTraces") == true, IncludeTransactionReceipts: source.get("includeTransactionReceipts") == true})
			} else {
				out.Accounts = append(out.Accounts, AccountSource{Name: sourceName, Chain: name, Address: addresses, Factory: factoryConfig, StartBlock: start, EndBlock: end})
			}
		}
	}
	if err := out.Validate(); err != nil {
		return Config{}, err
	}
	return out, nil
}
