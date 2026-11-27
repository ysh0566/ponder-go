package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/rpc"
)

type SourceIndexingBuild struct {
	executableOnce sync.Once
	executable     *ExecutableSourceIndexingBuild
	functions      []SourceIndexingFunction
	ordering       any
	sources        map[string][]*sourceObject
	latest         []*sourceObject
	base           *SourceConfigBuild
	chains         []*sourceObject
	chainIndexes   []int
	finalized      []*sourceObject
	callbacks      [][]*sourceObject
	setup          [][]*sourceObject
	contracts      []*sourceObject
	logs           []*sourceObject
}

func (build *SourceIndexingBuild) Value() map[string]any {
	clients := build.base.RPCClients()
	selected := clients[:0:0]
	for _, index := range build.chainIndexes {
		selected = append(selected, clients[index])
	}
	callbacks, setup := []any{}, []any{}
	for i := range build.callbacks {
		callbacks = append(callbacks, sourcePlain(build.callbacks[i]))
		setup = append(setup, sourcePlain(build.setup[i]))
	}
	return map[string]any{"chains": sourcePlain(build.chains), "rpcs": sourcePlain(selected), "finalizedBlocks": sourcePlain(build.finalized), "eventCallbacks": callbacks, "setupCallbacks": setup, "contracts": sourcePlain(build.contracts), "logs": sourcePlain(build.logs)}
}

func sourceFlatten(config *sourceObject, kind string) ([]*sourceObject, error) {
	result := []*sourceObject{}
	value := config.get(kind)
	if sourceNullish(value) {
		return result, nil
	}
	sources, err := sourceObjects(value)
	if err != nil {
		return nil, err
	}
	for _, name := range sources.keys {
		source, err := sourceObjects(sources.get(name))
		if err != nil {
			return nil, err
		}
		if overrides, ok := source.get("chain").(*sourceObject); ok {
			for _, chain := range overrides.keys {
				base := sourceRecord("name", name, "chain", chain)
				for _, key := range source.keys {
					if key != "chain" {
						base.set(key, source.get(key))
					}
				}
				override, err := sourceObjects(overrides.get(chain))
				if err != nil {
					return nil, err
				}
				result = append(result, base.overlay(override))
			}
		} else {
			result = append(result, sourceRecord("name", name).overlay(source))
		}
	}
	return result, nil
}

func sourceQuotedNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "'" + name + "'"
	}
	return strings.Join(quoted, ", ")
}

// BuildSourceIndexing resolves actual RPC heads before constructing callbacks.
// This dynamic adapter intentionally does not satisfy source type obligations.
func BuildSourceIndexing(ctx context.Context, config *SourceConfiguration, built *SourceConfigBuild, functions []SourceIndexingFunction) (*SourceIndexingBuild, error) {
	return buildSourceIndexing(ctx, config, built, functions, true)
}

// BuildRuntimeSourceIndexing compiles callbacks for a Go writer that acquires
// finality when it opens. Static bounds require no network access. Explicit
// "latest" bounds share a per-chain snapshot. FinalizedBlocks is empty.
func BuildRuntimeSourceIndexing(ctx context.Context, config *SourceConfiguration, options SourceBuildOptions, functions []SourceIndexingFunction) (*SourceIndexingBuild, error) {
	base, err := BuildSourceConfig(config, options)
	if err != nil {
		return nil, err
	}
	return buildSourceIndexing(ctx, config, base, functions, false)
}

// NewRuntimeRequesters constructs fresh transports for the participating chains
// in executable order, preserving the configuration's transport and concurrency settings.
func (build *SourceIndexingBuild) NewRuntimeRequesters() ([]rpc.Requester, error) {
	if build == nil || build.base == nil || build.base.value == nil || len(build.base.value.Chains) == 0 || len(build.chains) == 0 {
		return nil, fmt.Errorf("compiled source chains required for runtime requesters")
	}
	requesters := make([]rpc.Requester, len(build.chains))
	for i, chain := range build.chains {
		client, err := build.base.value.newRPC(chain)
		if err != nil {
			return nil, err
		}
		requesters[i] = client
	}
	return requesters, nil
}

// BuildNativeSourceConfigs compiles the single-store workers.
func BuildNativeSourceConfigs(ctx context.Context, config *SourceConfiguration, options SourceBuildOptions, functions []SourceIndexingFunction) ([]Config, error) {
	build, err := BuildRuntimeSourceIndexing(ctx, config, options, functions)
	if err != nil {
		return nil, err
	}
	return build.NativeConfigs()
}

func buildSourceIndexing(ctx context.Context, config *SourceConfiguration, built *SourceConfigBuild, functions []SourceIndexingFunction, withFinalizedBlocks bool) (*SourceIndexingBuild, error) {
	if config == nil || built == nil || built.value == nil {
		return nil, fmt.Errorf("source configuration and config build are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	chains := built.value.Chains
	latest := make([]*sourceObject, len(chains))
	finalized := make([]*sourceObject, len(chains))
	chainByName := map[string]int{}
	chainNames := []string{}
	fetchLatest := func(i int) error {
		block, err := sourceHeader(ctx, built.value.RPCs[i], "latest")
		if err != nil {
			return fmt.Errorf("Unable to fetch \"latest\" block for chain '%s':\n%w", chains[i].get("name"), err)
		}
		latest[i] = block
		return nil
	}
	for i, chain := range chains {
		name := sourceString(chain.get("name"))
		chainByName[name], chainNames = i, append(chainNames, name)
		if !withFinalizedBlocks {
			continue
		}
		if err := fetchLatest(i); err != nil {
			return nil, err
		}
		block, err := sourceFinalized(ctx, chain, built.value.RPCs[i], latest[i])
		if err != nil {
			return nil, err
		}
		finalized[i] = block
	}
	allSources := map[string][]*sourceObject{}
	names := []string{}
	seenNames := map[string]bool{}
	for _, kind := range []string{"contracts", "accounts", "blocks"} {
		if table, ok := config.value.get(kind).(*sourceObject); ok {
			for _, name := range table.keys {
				if seenNames[name] {
					return nil, fmt.Errorf("Validation failed: Duplicate name '%s' not allowed. The name must be unique across blocks, contracts, and accounts.", name)
				}
				seenNames[name] = true
				names = append(names, name)
			}
		}
		sources, err := sourceFlatten(config.value, kind)
		if err != nil {
			return nil, err
		}
		allSources[kind] = sources
	}
	if len(functions) == 0 {
		return nil, fmt.Errorf("Validation failed: Found 0 registered indexing functions.")
	}
	registered := map[string]SourceIndexingFunction{}
	for _, function := range functions {
		parts := strings.Split(function.Name, ":")
		if strings.Contains(function.Name, ".") {
			parts = strings.Split(function.Name, ".")
		}
		bad := func() error {
			return fmt.Errorf("Validation failed: Invalid event '%s', expected format '{sourceName}:{eventName}' or '{sourceName}.{functionName}'.", function.Name)
		}
		if len(parts) == 0 || parts[0] == "" {
			return nil, bad()
		}
		if len(parts) == 3 {
			if (parts[1] != "transaction" && parts[1] != "transfer") || (parts[2] != "from" && parts[2] != "to") {
				return nil, fmt.Errorf("Validation failed: Invalid event '%s', expected format '{sourceName}:transaction:from', '{sourceName}:transaction:to', '{sourceName}:transfer:from', or '{sourceName}:transfer:to'.", function.Name)
			}
		} else if len(parts) != 2 || parts[1] == "" {
			return nil, bad()
		}
		if _, exists := registered[function.Name]; exists {
			return nil, fmt.Errorf("Validation failed: Multiple indexing functions registered for event '%s'.", function.Name)
		}
		registered[function.Name] = function
		if !seenNames[parts[0]] {
			return nil, fmt.Errorf("Validation failed: Invalid event '%s' uses an unrecognized contract, account, or block interval name. Expected one of [%s].", function.Name, sourceQuotedNames(names))
		}
	}
	for _, kind := range []string{"contracts", "accounts", "blocks"} {
		for _, source := range allSources[kind] {
			name := sourceString(source.get("name"))
			if sourceNullish(source.get("chain")) {
				return nil, fmt.Errorf("Validation failed: Chain for '%s' is null or undefined. Expected one of [%s]. Did you forget to change 'network' to 'chain' when migrating to 0.11?", name, sourceQuotedNames(chainNames))
			}
			chainName := sourceString(source.get("chain"))
			index, ok := chainByName[chainName]
			if !ok {
				return nil, fmt.Errorf("Validation failed: Invalid chain for '%s'. Got '%s', expected one of [%s].", name, chainName, sourceQuotedNames(chainNames))
			}
			if latest[index] == nil && sourceNeedsLatest(source) {
				if err := fetchLatest(index); err != nil {
					return nil, err
				}
			}
			from, err := sourceResolveBlock(source.get("startBlock"), latest[index])
			if err != nil {
				return nil, err
			}
			to, err := sourceResolveBlock(source.get("endBlock"), latest[index])
			if err != nil {
				return nil, err
			}
			if !filter.IsUndefined(from) && !filter.IsUndefined(to) {
				left, leftOK := sourceNumber(from)
				right, rightOK := sourceNumber(to)
				if leftOK && rightOK && right < left {
					return nil, fmt.Errorf("Validation failed: Start block for '%s' is after end block (%s > %s).", name, sourceString(from), sourceString(to))
				}
			}
			for _, boundary := range []struct {
				name  string
				value any
			}{{"start", from}, {"end", to}} {
				if !filter.IsUndefined(boundary.value) && !sourceInteger(boundary.value) {
					return nil, fmt.Errorf("Validation failed: Invalid %s block for '%s'. Got %s typeof %s, expected an integer.", boundary.name, name, sourceString(boundary.value), sourceType(boundary.value))
				}
			}
			if address, ok := source.get("address").(*sourceObject); ok {
				factoryFrom, err := sourceResolveBlock(address.get("startBlock"), latest[index])
				if err != nil {
					return nil, err
				}
				factoryTo, err := sourceResolveBlock(address.get("endBlock"), latest[index])
				if err != nil {
					return nil, err
				}
				factoryFrom, factoryTo = sourceDefault(factoryFrom, from), sourceDefault(factoryTo, to)
				f, _ := sourceNumber(factoryFrom)
				t, _ := sourceNumber(factoryTo)
				s, _ := sourceNumber(from)
				e, _ := sourceNumber(to)
				if !filter.IsUndefined(factoryFrom) && (filter.IsUndefined(from) || f > s) {
					return nil, fmt.Errorf("Validation failed: Start block for '%s' is before start block of factory address (%s > %s).", name, sourceString(factoryFrom), sourceString(from))
				}
				if !filter.IsUndefined(to) && (filter.IsUndefined(factoryTo) || t > e) {
					return nil, fmt.Errorf("Validation failed: End block for %s  is before end block of factory address (%s > %s).", name, sourceString(factoryTo), sourceString(to))
				}
				if !filter.IsUndefined(factoryFrom) && !filter.IsUndefined(factoryTo) && t < f {
					return nil, fmt.Errorf("Validation failed: Start block for '%s' factory address is after end block (%s > %s).", name, sourceString(factoryFrom), sourceString(factoryTo))
				}
			}
		}
	}
	allCallbacks, err := sourceBuildAllCallbacks(allSources, chains, latest, functions)
	if err != nil {
		return nil, err
	}
	result := &SourceIndexingBuild{sources: allSources, latest: latest, base: built, chains: []*sourceObject{}, chainIndexes: []int{}, finalized: []*sourceObject{}, callbacks: [][]*sourceObject{}, setup: [][]*sourceObject{}, contracts: []*sourceObject{}, logs: allCallbacks.logs}
	result.functions = append([]SourceIndexingFunction(nil), functions...)
	result.ordering = config.value.get("ordering")
	if sourceNullish(result.ordering) {
		result.ordering = "multichain"
	}
	for i, chain := range chains {
		callbacks, setup, contracts := allCallbacks.events[i], allCallbacks.setup[i], allCallbacks.contracts[i]
		if len(callbacks) == 0 && len(setup) == 0 {
			result.logs = append(result.logs, sourceRecord("level", "warn", "msg", "No registered indexing functions", "chain", chain.get("name"), "chain_id", chain.get("id")))
			continue
		}
		result.chains = append(result.chains, chain)
		result.chainIndexes = append(result.chainIndexes, i)
		if withFinalizedBlocks {
			result.finalized = append(result.finalized, finalized[i])
		}
		result.callbacks, result.setup, result.contracts = append(result.callbacks, callbacks), append(result.setup, setup), append(result.contracts, contracts)
	}
	if len(result.chains) == 0 {
		return nil, fmt.Errorf("Validation failed: Found 0 chains with registered indexing functions.")
	}
	return result, nil
}

func sourceParseABI(value any) (abi.ABI, []any, error) {
	items, ok := value.([]any)
	if !ok {
		return abi.ABI{}, nil, fmt.Errorf("contract ABI must be an array")
	}
	data, err := json.Marshal(sourcePlain(items))
	if err != nil {
		return abi.ABI{}, nil, err
	}
	parsed, err := abi.Parse(string(data))
	return parsed, items, err
}
