package config

// Future source-shaped builder draft only. No public/static parity claim, no
// runtime integration or test execution yet. Real chain catalog and live bridge
// are required before these descriptors can become accepted implementation.
import (
	"fmt"
	"math"
	"net/url"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
)

type sourceConfigBuild struct {
	Chains []*sourceObject
	RPCs   []*rpc.Client
	Logs   []*sourceObject
}
type sourceBuilder struct {
	// Catalog must be generated from fixed source Object.values(viemChains), first
	// matching id, with the source's explicit Hyperliquid999 override and canonical
	// shared object identity. Source metadata/raw ABI must retain original fields.
	Catalog map[string]*sourceObject
}

func sourceRecord(pairs ...any) *sourceObject {
	result := sourceNewObject()
	for i := 0; i < len(pairs); i += 2 {
		result.set(pairs[i].(string), pairs[i+1])
	}
	return result
}
func sourceAsObject(value any) (*sourceObject, bool) {
	object, ok := value.(*sourceObject)
	return object, ok
}
func sourceDefault(value, fallback any) any {
	if sourceNullish(value) {
		return fallback
	}
	return value
}
func sourceObjects(value any) (*sourceObject, error) {
	object, ok := sourceAsObject(value)
	if !ok {
		return nil, fmt.Errorf("source configuration object required")
	}
	return object, nil
}
func sourceStrings(value any) ([]string, error) {
	if text, ok := value.(string); ok {
		return []string{text}, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("native transport adaptation is not implemented")
	}
	result := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("RPC URL must be a string")
		}
		result[i] = text
	}
	return result, nil
}
func sourceDefaultURLs(chain *sourceObject, key string) []any {
	urls, ok := sourceAsObject(chain.get("rpcUrls"))
	if !ok {
		return nil
	}
	defaults, ok := sourceAsObject(urls.get("default"))
	if !ok {
		return nil
	}
	values, _ := defaults.get(key).([]any)
	return values
}
func (builder *sourceBuilder) buildConfig(config *sourceObject, maxConcurrency int) (*sourceConfigBuild, error) {
	chains, err := sourceObjects(config.get("chains"))
	if err != nil {
		return nil, err
	}
	result := &sourceConfigBuild{Chains: []*sourceObject{}, RPCs: []*rpc.Client{}, Logs: []*sourceObject{}}
	for _, name := range chains.entries() {
		raw, err := sourceObjects(chains.get(name))
		if err != nil {
			return nil, err
		}
		id := raw.get("id")
		number, numeric := sourceNumber(id)
		if numeric && number > 9007199254740991 {
			return nil, fmt.Errorf("Chain %q with id %s has a chain_id that is too large.", name, sourceString(id))
		}
		matched := builder.Catalog[sourceString(id)]
		urls := raw.get("rpc")
		if filter.IsUndefined(urls) || urls == "" {
			if matched == nil {
				return nil, fmt.Errorf("Chain %q with id %s has no RPC defined and no default RPC URL was found in 'viem/chains'.", name, sourceString(id))
			}
			urls = sourceDefaultURLs(matched, "http")
			raw.set("rpc", urls) // The original buildConfig intentionally mutates input.
		}
		endpoints, err := sourceStrings(urls)
		if err != nil {
			return nil, err
		}
		if len(endpoints) == 0 {
			return nil, fmt.Errorf("Chain %q with id %s has no RPC URLs.", name, sourceString(id))
		}
		if matched != nil {
			for _, endpoint := range endpoints {
				for _, kind := range []string{"http", "webSocket"} {
					for _, defaultURL := range sourceDefaultURLs(matched, kind) {
						if endpoint == defaultURL {
							result.Logs = append(result.Logs, sourceRecord("level", "warn", "msg", "Detected public RPC URL. Most apps require an RPC URL with a higher rate limit.", "chain", name, "chain_id", id, "url", defaultURL))
							break
						}
					}
				}
			}
		}
		polling := raw.get("pollingInterval")
		if n, ok := sourceNumber(polling); !filter.IsUndefined(polling) && ok && n < 100 {
			return nil, fmt.Errorf("Invalid 'pollingInterval' for chain '%s. Expected 100 milliseconds or greater, got %s milliseconds.", name, sourceString(polling))
		}
		window := raw.get("reorgWindow")
		if !filter.IsUndefined(window) {
			n, numeric := sourceNumber(window)
			if !sourceInteger(window) || !numeric || n < 0 || n > 600 {
				return nil, fmt.Errorf("Invalid 'reorgWindow' for chain '%s'. Expected an integer between 0 and 600 seconds, got %s.", name, sourceString(window))
			}
		}
		metadata := any(matched)
		if matched == nil {
			metadata = filter.Undefined
		}
		chain := sourceRecord("id", id, "name", name, "rpc", urls, "ws", raw.get("ws"), "pollingInterval", sourceDefault(polling, float64(1000)), "reorgWindow", sourceDefault(window, float64(180)), "cacheRpcRequests", sourceDefault(raw.get("cacheRpcRequests"), true), "ethGetLogsBlockRange", raw.get("ethGetLogsBlockRange"), "viemChain", metadata)
		result.Chains = append(result.Chains, chain)
	}
	seen := map[string]bool{}
	if maxConcurrency == 0 {
		maxConcurrency = 256
	}
	for _, chain := range result.Chains {
		id := sourceString(chain.get("id"))
		if seen[id] {
			return nil, fmt.Errorf("Invalid id for chain %q. %s is already in use.", sourceString(chain.get("name")), id)
		}
		seen[id] = true
	}
	// Source currently creates all RPCs after validating every chain. Preserve
	// error ordering. Native request slots are shared by cache-scoped clients.
	for _, chain := range result.Chains {
		urls, err := sourceStrings(chain.get("rpc"))
		if err != nil {
			return nil, err
		}
		for _, endpoint := range urls {
			parsed, err := url.Parse(endpoint)
			if err != nil {
				return nil, err
			}
			switch parsed.Scheme {
			case "http", "https", "ws", "wss":
			default:
				return nil, fmt.Errorf("Unsupported RPC URL protocol: %s:", parsed.Scheme)
			}
		}
		client := rpc.New(urls...)
		if err := client.ConfigureConcurrency(maxConcurrency / len(result.Chains)); err != nil {
			return nil, err
		}
		if id, ok := sourceNumber(chain.get("id")); ok && id >= 0 && math.Trunc(id) == id && id <= 9007199254740991 {
			client.ChainID = uint64(id)
		}
		result.RPCs = append(result.RPCs, client)
	}
	return result, nil
}
