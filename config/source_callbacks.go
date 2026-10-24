package config

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/filter"
)

type sourceCallbacks struct {
	events, setup [][]*sourceObject
	contracts     []*sourceObject
	logs          []*sourceObject
}

var sourceBlockColumns = strings.Fields("baseFeePerGas difficulty extraData gasLimit gasUsed hash logsBloom miner mixHash totalDifficulty nonce number parentHash receiptsRoot sha3Uncles size stateRoot timestamp transactionsRoot")
var sourceTransactionColumns = strings.Fields("from gas hash input nonce r s to transactionIndex v value type gasPrice accessList maxFeePerGas maxPriorityFeePerGas")
var sourceReceiptColumns = strings.Fields("contractAddress cumulativeGasUsed effectiveGasPrice from gasUsed logsBloom status to type")
var sourceTraceColumns = strings.Fields("traceAddress type from to gas gasUsed input output value")
var sourceLogColumns = strings.Fields("address data logIndex removed topics")

func sourceInclude(kind string, receipts bool) []any {
	out := []any{}
	add := func(prefix string, columns []string) {
		for _, column := range columns {
			out = append(out, prefix+"."+column)
		}
	}
	switch kind {
	case "log":
		add("log", sourceLogColumns)
		add("transaction", sourceTransactionColumns)
		add("block", sourceBlockColumns)
	case "trace", "transfer":
		add("block", sourceBlockColumns)
		add("transaction", sourceTransactionColumns)
		add("trace", sourceTraceColumns)
	case "transaction":
		add("transaction", sourceTransactionColumns)
		add("transactionReceipt", sourceReceiptColumns)
		add("block", sourceBlockColumns)
	case "block":
		add("block", sourceBlockColumns)
	}
	if receipts && kind != "transaction" {
		add("transactionReceipt", sourceReceiptColumns)
	}
	return out
}

func sourceValidateAddresses(name string, value any) error {
	values, array := value.([]any)
	if !array {
		values = []any{value}
	}
	if len(values) == 0 {
		return fmt.Errorf("Validation failed: Invalid address for '%s'. Got an empty array, expected at least one address.", name)
	}
	for _, value := range values {
		address, ok := value.(string)
		if !ok {
			return fmt.Errorf("address.startsWith is not a function")
		}
		if !strings.HasPrefix(address, "0x") {
			prefix := address
			if len(prefix) > 2 {
				prefix = prefix[:2]
			}
			return fmt.Errorf("Validation failed: Invalid prefix for address '%s'. Got '%s', expected '0x'.", address, prefix)
		}
		if len(address) != 42 {
			return fmt.Errorf("Validation failed: Invalid length for address '%s'. Got %d, expected 42 characters.", address, len(address))
		}
	}
	return nil
}

func sourceAddress(source, chain, latest *sourceObject) (any, error) {
	value := source.get("address")
	if object, ok := value.(*sourceObject); ok {
		if !filter.IsUndefined(object.get("address")) {
			if err := sourceValidateAddresses(sourceString(source.get("name")), object.get("address")); err != nil {
				return nil, err
			}
		}
		from, err := sourceResolveBlock(source.get("startBlock"), latest)
		if err != nil {
			return nil, err
		}
		to, err := sourceResolveBlock(source.get("endBlock"), latest)
		if err != nil {
			return nil, err
		}
		factoryFrom, err := sourceResolveBlock(object.get("startBlock"), latest)
		if err != nil {
			return nil, err
		}
		factoryTo, err := sourceResolveBlock(object.get("endBlock"), latest)
		if err != nil {
			return nil, err
		}
		factoryFrom, factoryTo = sourceDefault(factoryFrom, from), sourceDefault(factoryTo, to)
		eventData, err := json.Marshal(sourcePlain(object.get("event")))
		if err != nil {
			return nil, err
		}
		var event abi.Event
		if err = json.Unmarshal(eventData, &event); err != nil {
			return nil, err
		}
		id, _ := sourceNumber(chain.get("id"))
		cfg := factory.Config{ChainID: uint64(id), SourceID: sourceString(source.get("name")), Event: event}
		if !filter.IsUndefined(object.get("address")) {
			cfg.Address = sourcePlain(object.get("address"))
		}
		if p, ok := object.get("parameter").(string); ok {
			cfg.Parameter = &p
		}
		if p, ok := object.get("location").(string); ok {
			cfg.Location = &p
		}
		if n, ok := sourceNumber(factoryFrom); ok {
			v := uint64(n)
			cfg.FromBlock = &v
		}
		if n, ok := sourceNumber(factoryTo); ok {
			v := uint64(n)
			cfg.ToBlock = &v
		}
		built, err := factory.Build(cfg)
		if err != nil {
			return nil, err
		}
		address := built.Address
		if address == nil {
			address = filter.Undefined
		}
		return sourceRecord("id", built.ID, "type", built.Type, "chainId", chain.get("id"), "sourceId", source.get("name"), "address", address, "eventSelector", built.EventSelector, "childAddressLocation", built.ChildAddressLocation, "fromBlock", factoryFrom, "toBlock", factoryTo), nil
	}
	if filter.IsUndefined(value) {
		return value, nil
	}
	if err := sourceValidateAddresses(sourceString(source.get("name")), value); err != nil {
		return nil, err
	}
	if array, ok := value.([]any); ok {
		seen := map[string]bool{}
		out := []any{}
		for _, v := range array {
			s := v.(string)
			if !seen[s] {
				seen[s] = true
				out = append(out, strings.ToLower(s))
			}
		}
		return out, nil
	}
	return strings.ToLower(value.(string)), nil
}

func sourceBuildAllCallbacks(sources map[string][]*sourceObject, chains, latest []*sourceObject, functions []SourceIndexingFunction) (*sourceCallbacks, error) {
	out := &sourceCallbacks{events: make([][]*sourceObject, len(chains)), setup: make([][]*sourceObject, len(chains)), contracts: make([]*sourceObject, len(chains)), logs: []*sourceObject{}}
	chainIndex := map[string]int{}
	registered := map[string]SourceIndexingFunction{}
	for _, f := range functions {
		registered[f.Name] = f
	}
	for i, c := range chains {
		chainIndex[sourceString(c.get("name"))] = i
		out.events[i] = []*sourceObject{}
		out.setup[i] = []*sourceObject{}
		out.contracts[i] = sourceNewObject()
	}
	warn := func(source *sourceObject, kind string) {
		out.logs = append(out.logs, sourceRecord("level", "warn", "msg", "No registered indexing functions", "name", source.get("name"), "type", kind))
	}
	bounds := func(source *sourceObject, i int) (any, any) {
		from, _ := sourceResolveBlock(source.get("startBlock"), latest[i])
		to, _ := sourceResolveBlock(source.get("endBlock"), latest[i])
		return from, to
	}
	for _, source := range sources["contracts"] {
		i := chainIndex[sourceString(source.get("chain"))]
		chain := chains[i]
		name := sourceString(source.get("name"))
		from, to := bounds(source, i)
		if f, ok := registered[name+":setup"]; ok {
			out.setup[i] = append(out.setup[i], sourceRecord("name", f.Name, "fn", f.Fn, "chain", chain, "block", from))
		}
		address, err := sourceAddress(source, chain, latest[i])
		if err != nil {
			return nil, err
		}
		contractAddress := address
		if _, ok := address.(*sourceObject); ok {
			contractAddress = filter.Undefined
		}
		out.contracts[i].set(name, sourceRecord("abi", source.get("abi"), "address", contractAddress, "startBlock", from, "endBlock", to))
		parsed, raw, err := sourceParseABI(source.get("abi"))
		if err != nil {
			return nil, err
		}
		eventItems := map[string]any{}
		functionItems := map[string]any{}
		eventNames, functionNames := []string{}, []string{}
		eventIndex, functionIndex := 0, 0
		counts := map[string]int{}
		for _, f := range parsed.FunctionEntries() {
			counts[f.Name]++
		}
		for _, item := range raw {
			object, ok := item.(*sourceObject)
			if !ok {
				continue
			}
			switch object.get("type") {
			case "event":
				event := parsed.EventEntries()[eventIndex]
				eventIndex++
				safe := parsed.EventSafeName(event)
				eventNames = append(eventNames, safe)
				if _, ok := eventItems[safe]; !ok {
					eventItems[safe] = item
				}
			case "function":
				function := parsed.FunctionEntries()[functionIndex]
				functionIndex++
				safe := function.SafeName(counts[function.Name] > 1)
				functionNames = append(functionNames, safe)
				if _, ok := functionItems[safe]; !ok {
					functionItems[safe] = item
				}
			}
		}
		topicFilters := map[string]abi.EventTopics{}
		if value := source.get("filter"); !sourceNullish(value) {
			filters, ok := value.([]any)
			if !ok {
				filters = []any{value}
			}
			for _, value := range filters {
				item, err := sourceObjects(value)
				if err != nil {
					return nil, err
				}
				eventName := sourceString(item.get("event"))
				if _, exists := eventItems[eventName]; !exists {
					return nil, fmt.Errorf("Validation failed: Invalid filter for contract '%s'. Got event name '%s', expected one of [%s].", name, eventName, sourceQuotedNames(eventNames))
				}
			}
			for _, value := range filters {
				item := value.(*sourceObject)
				eventName := sourceString(item.get("event"))
				event, _ := parsed.FindEventSafe(eventName)
				args := item.get("args")
				if filter.IsUndefined(args) {
					args = nil
				} else {
					args = sourcePlain(args)
				}
				topics, err := abi.EncodeEventTopics(event, args)
				if err != nil {
					return nil, err
				}
				// The source filter check looks up the bare ABI name, including overloads.
				if _, exists := registered[name+":"+event.Name]; !exists {
					return nil, fmt.Errorf("Validation failed: Event selector '%s' is used in a filter but does not have a corresponding indexing function.", parsed.EventSafeName(event))
				}
				topicFilters[topics.Topic0] = topics
			}
		}
		logNames, callNames := []string{}, []string{}
		for _, f := range functions {
			if strings.Contains(f.Name, ":") {
				parts := strings.Split(f.Name, ":")
				if parts[0] == name && parts[1] != "setup" {
					logNames = append(logNames, parts[1])
				}
			}
			if strings.Contains(f.Name, ".") && source.get("includeCallTraces") == true {
				parts := strings.Split(f.Name, ".")
				if parts[0] == name {
					callNames = append(callNames, parts[1])
				}
			}
		}
		receipts := sourceDefault(source.get("includeTransactionReceipts"), false)
		for _, safe := range logNames {
			rawItem, ok := eventItems[safe]
			if !ok {
				return nil, fmt.Errorf("Validation failed: Event name for event '%s' not found in the contract ABI. Got '%s', expected one of [%s].", safe, safe, sourceQuotedNames(eventNames))
			}
			event, _ := parsed.FindEventSafe(safe)
			selector := abi.EventSelector(event)
			topics := abi.EventTopics{Topic0: selector}
			if filtered, ok := topicFilters[selector]; ok {
				topics = filtered
			}
			eventName := name + ":" + safe
			f := registered[eventName]
			logFilter := sourceRecord("type", "log", "chainId", chain.get("id"), "sourceId", source.get("name"), "address", address, "topic0", selector, "topic1", topics.Topic1, "topic2", topics.Topic2, "topic3", topics.Topic3, "fromBlock", from, "toBlock", to, "hasTransactionReceipt", receipts, "include", sourceInclude("log", receipts == true))
			out.events[i] = append(out.events[i], sourceRecord("filter", logFilter, "name", eventName, "fn", f.Fn, "chain", chain, "type", "contract", "abiItem", rawItem, "metadata", sourceRecord("safeName", safe, "abi", source.get("abi"))))
		}
		for _, safe := range callNames {
			rawItem, ok := functionItems[safe]
			if !ok {
				return nil, fmt.Errorf("Validation failed: Function name for function '%s' not found in the contract ABI. Got '%s', expected one of [%s].", safe, safe, sourceQuotedNames(functionNames))
			}
			var function abi.Function
			data, _ := json.Marshal(sourcePlain(rawItem))
			if err := json.Unmarshal(data, &function); err != nil {
				return nil, err
			}
			eventName := name + "." + safe
			f := registered[eventName]
			traceFilter := sourceRecord("type", "trace", "chainId", chain.get("id"), "sourceId", source.get("name"), "fromAddress", filter.Undefined, "toAddress", address, "callType", "CALL", "functionSelector", abi.FunctionSelector(function), "fromBlock", from, "toBlock", to, "hasTransactionReceipt", receipts, "include", sourceInclude("trace", receipts == true))
			out.events[i] = append(out.events[i], sourceRecord("filter", traceFilter, "name", eventName, "fn", f.Fn, "chain", chain, "type", "contract", "abiItem", rawItem, "metadata", sourceRecord("safeName", safe, "abi", source.get("abi"))))
		}
		if len(logNames) == 0 && len(callNames) == 0 {
			warn(source, "contract")
		}
	}
	for _, source := range sources["accounts"] {
		i := chainIndex[sourceString(source.get("chain"))]
		chain := chains[i]
		name := sourceString(source.get("name"))
		from, to := bounds(source, i)
		if filter.IsUndefined(source.get("address")) {
			return nil, fmt.Errorf("Validation failed: Account '%s' must specify an 'address'.", name)
		}
		address, err := sourceAddress(source, chain, latest[i])
		if err != nil {
			return nil, err
		}
		has := false
		for _, kind := range []string{"transaction", "transfer"} {
			for _, direction := range []string{"to", "from"} {
				eventName := name + ":" + kind + ":" + direction
				f, ok := registered[eventName]
				if !ok {
					continue
				}
				has = true
				var fromAddress, toAddress any = filter.Undefined, address
				if direction == "from" {
					fromAddress, toAddress = address, filter.Undefined
				}
				receipts := sourceDefault(source.get("includeTransactionReceipts"), false)
				if kind == "transaction" {
					receipts = true
				}
				accountFilter := sourceRecord("type", kind, "chainId", chain.get("id"), "sourceId", source.get("name"), "fromAddress", fromAddress, "toAddress", toAddress, "fromBlock", from, "toBlock", to, "hasTransactionReceipt", receipts, "include", sourceInclude(kind, receipts == true))
				out.events[i] = append(out.events[i], sourceRecord("filter", accountFilter, "name", eventName, "fn", f.Fn, "chain", chain, "type", "account", "direction", direction))
			}
		}
		if !has {
			warn(source, "account")
		}
	}
	for _, source := range sources["blocks"] {
		i := chainIndex[sourceString(source.get("chain"))]
		chain := chains[i]
		name := sourceString(source.get("name"))
		from, to := bounds(source, i)
		interval := sourceDefault(source.get("interval"), float64(1))
		number, _ := sourceNumber(interval)
		if math.IsNaN(number) {
			interval, number = float64(0), 0
		}
		if !sourceInteger(interval) || number == 0 {
			return nil, fmt.Errorf("Validation failed: Invalid interval for block interval '%s'. Got %s, expected a non-zero integer.", name, sourceString(interval))
		}
		eventName := name + ":block"
		f, ok := registered[eventName]
		if !ok {
			warn(source, "block")
			continue
		}
		start, _ := sourceNumber(from)
		blockFilter := sourceRecord("type", "block", "chainId", chain.get("id"), "sourceId", source.get("name"), "interval", interval, "offset", math.Mod(start, number), "fromBlock", from, "toBlock", to, "hasTransactionReceipt", false, "include", sourceInclude("block", false))
		out.events[i] = append(out.events[i], sourceRecord("filter", blockFilter, "name", eventName, "fn", f.Fn, "chain", chain, "type", "block"))
	}
	return out, nil
}
