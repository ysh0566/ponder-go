package config

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/factory"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
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
	for i, group := range build.callbacks {
		out[i] = nativeEventCallbacks(group)
	}
	return out
}

func nativeEventCallbacks(callbacks []*sourceObject) []SourceEventCallback {
	out := make([]SourceEventCallback, 0, len(callbacks))
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
		out = append(out, callback)
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

// NativeConfig selects a single named chain and its compiled filters and handlers.
func (build *SourceIndexingBuild) NativeConfig(name string) (Config, error) {
	for i, chain := range build.chains {
		if chain.get("name") == name {
			return build.nativeConfig(i, "", nil)
		}
	}
	return Config{}, fmt.Errorf("source chain %q has no registered indexing functions", name)
}

// NativeConfigs returns the participating chains in build order with their
// compiled ordering, handlers, factory identities, RPC clients and cache policy.
func (build *SourceIndexingBuild) NativeConfigs() ([]Config, error) {
	if build == nil || len(build.chains) == 0 {
		return nil, fmt.Errorf("compiled source build requires at least one participating chain")
	}
	order, ok := build.ordering.(string)
	if !ok || order == "" {
		return nil, fmt.Errorf("invalid compiled source ordering %v", build.ordering)
	}
	configs := make([]Config, len(build.chains))
	for i := range build.chains {
		sourceHasEvents := make(map[string]bool)
		for _, setup := range build.setup[i] {
			sourceHasEvents[strings.TrimSuffix(sourceString(setup.get("name")), ":setup")] = false
		}
		for _, callback := range build.callbacks[i] {
			fields := callback.get("filter").(*sourceObject)
			sourceHasEvents[sourceString(fields.get("sourceId"))] = true
		}
		cfg, err := build.nativeConfig(i, schema.Ordering(order), sourceHasEvents)
		if err != nil {
			return nil, err
		}
		configs[i] = cfg
	}
	return configs, nil
}

func (build *SourceIndexingBuild) nativeConfig(selected int, ordering schema.Ordering, sourceHasEvents map[string]bool) (Config, error) {
	rawChain := build.chains[selected]
	name := sourceString(rawChain.get("name"))
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
	ws, _ := rawChain.get("ws").(string)
	chain := Chain{Name: name, ID: id, RPC: urls, WS: ws, ReorgWindow: &w, PollingIntervalMS: int(polling)}
	if metadata, ok := rawChain.get("viemChain").(*sourceObject); ok {
		if hint, ok := sourceNumber(metadata.get("blockTime")); ok {
			chain.BlockTimeMS = &hint
		}
	}
	callbacks := nativeEventCallbacks(build.callbacks[selected])
	plan := &SourceRuntimePlan{Version: 1, Callbacks: callbacks, Factories: map[string]factory.LogFactory{}, Client: build.base.value.RPCs[build.chainIndexes[selected]], CacheRPCRequests: rawChain.get("cacheRpcRequests") == true}
	for _, setup := range build.setup[selected] {
		plan.Setup = append(plan.Setup, SourceIndexingFunction{Name: sourceString(setup.get("name")), Fn: setup.get("fn")})
	}
	out := Config{Ordering: ordering, Chains: []Chain{chain}, SourcePlan: plan}
	for _, kind := range []string{"contracts", "accounts", "blocks"} {
		for _, source := range build.sources[kind] {
			if source.get("chain") != name {
				continue
			}
			sourceName := sourceString(source.get("name"))
			hasEvents, registered := sourceHasEvents[sourceName]
			if sourceHasEvents != nil && !registered {
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
			// A setup-only contract finishes when its initialization block runs.
			if registered && !hasEvents {
				end = &start
			}
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
