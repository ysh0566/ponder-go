package runtime

import (
	"context"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"ponder.local/ponder-go/bloom"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/finality"
	"ponder.local/ponder-go/rpc"
)

func (s *SourceRealtimeSync) getBlockByHash(ctx context.Context, hash string, full bool) (filter.Object, error) {
	params := []any{hash, full}
	var block filter.Object
	if err := s.requester.Call(ctx, "eth_getBlockByHash", params, &block); err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("Block at hash '%s' could not be found.", hash)
	}
	return rpc.StandardizeSourceBlock(block, rpc.ActionRequest{Method: "eth_getBlockByHash", Params: params})
}
func (s *SourceRealtimeSync) fetchBlockEventData(ctx context.Context, header filter.Object) (sourceRealtimeData, error) {
	result := sourceRealtimeData{block: header, logs: []filter.Object{}, traces: []rpc.CallTrace{}, transactions: []filter.Object{}, receipts: []filter.Object{}, children: []SourceRealtimeFragmentChildren{}}
	blockNumber, err := sourceHistoricalNumber(header["number"])
	if err != nil {
		return result, err
	}
	hash, _ := header["hash"].(string)
	var block filter.Object
	method := "eth_getBlockByHash"
	if !filter.IsUndefined(sourceHistoricalProperty(header, "transactions")) {
		block = header
		method = "eth_getBlockByNumber"
	}
	encodedBloom, _ := header["logsBloom"].(string)
	text := strings.TrimPrefix(encodedBloom, "0x")
	if len(text)%2 != 0 {
		text = "0" + text
	}
	bits, err := hex.DecodeString(text)
	if err != nil {
		return result, err
	}
	async := finality.IsAsyncExecutionChain(s.chain.ID)
	missing := encodedBloom == bloom.ZeroLogsBloom
	activeLogs, activeFactories := []filter.Object{}, []filter.Object{}
	for i, f := range s.kinds["log"] {
		if filter.IsBlockInFilter(f, float64(blockNumber)) && (async || missing || bloom.IsFilterInBloom(bits, s.logBlooms[i])) {
			activeLogs = append(activeLogs, f)
		}
	}
	for i, f := range s.factories {
		if filter.IsBlockInFilter(f, float64(blockNumber)) && (async || missing || bloom.IsFactoryInBloom(bits, s.factoryBlooms[i])) {
			activeFactories = append(activeFactories, f)
		}
	}
	merged := filter.MergeLogFiltersToBlockRequest(activeLogs, activeFactories)
	requestLogs := !filter.IsUndefined(merged)
	var logsRequest filter.Object
	blockRequest := func() rpc.ActionRequest {
		if method == "eth_getBlockByNumber" {
			return rpc.ActionRequest{Method: method, Params: []any{block["number"], true}}
		}
		return rpc.ActionRequest{Method: method, Params: []any{block["hash"], true}}
	}
	if requestLogs {
		query := merged.(filter.Object)
		logsRequest = filter.Object{"blockHash": hash, "address": sourceHistoricalProperty(query, "address"), "topics": sourceHistoricalProperty(query, "topics")}
		params := filter.RPCValue(logsRequest).(filter.Object)
		getLogs := func() error { var err error; result.logs, err = s.actions.GetLogs(ctx, params); return err }
		if block == nil {
			var wg sync.WaitGroup
			var blockErr, logsErr error
			wg.Add(2)
			go func() { defer wg.Done(); block, blockErr = s.getBlockByHash(ctx, hash, true) }()
			go func() { defer wg.Done(); logsErr = getLogs() }()
			wg.Wait()
			if blockErr != nil {
				return result, blockErr
			}
			if logsErr != nil {
				return result, logsErr
			}
		} else if err := getLogs(); err != nil {
			return result, err
		}
		if err := rpc.ValidateLogsAndBlock(result.logs, block, rpc.ActionRequest{Method: "eth_getLogs", Params: []any{logsRequest}}, blockRequest()); err != nil {
			return result, err
		}
		for _, log := range result.logs {
			if log["transactionHash"] == "0x"+strings.Repeat("0", 64) {
				index, _ := sourceHistoricalNumber(log["logIndex"])
				s.logWarn(map[string]any{"msg": "Detected log with empty transaction hash. This is expected for some chains like ZKsync.", "action": "fetch_block_data", "chain": s.chain.Name, "chain_id": s.chain.ID, "number": blockNumber, "hash": hash, "logIndex": index})
			}
		}
	}
	if len(s.kinds["trace"])+len(s.kinds["transfer"]) > 0 {
		getTraces := func() error {
			var raw []filter.Object
			if err := s.requester.Call(ctx, "debug_traceBlockByHash", []any{hash, map[string]any{"tracer": "callTracer"}}, &raw); err != nil {
				return err
			}
			if raw == nil {
				return fmt.Errorf("Received invalid empty debug_traceBlockByHash response.")
			}
			var err error
			result.traces, err = rpc.FlattenSourceCallTraces(raw, rpc.ActionRequest{Method: "debug_traceBlockByHash", Params: []any{hash, map[string]any{"tracer": "callTracer"}}})
			return err
		}
		if block == nil {
			var wg sync.WaitGroup
			var blockErr, traceErr error
			wg.Add(2)
			go func() { defer wg.Done(); block, blockErr = s.getBlockByHash(ctx, hash, true) }()
			go func() { defer wg.Done(); traceErr = getTraces() }()
			wg.Wait()
			if blockErr != nil {
				return result, blockErr
			}
			if traceErr != nil {
				return result, traceErr
			}
		} else if err := getTraces(); err != nil {
			return result, err
		}
		if err := rpc.ValidateSourceTracesAndBlock(result.traces, block, rpc.ActionRequest{Method: "debug_traceBlockByNumber", Params: []any{block["number"], map[string]any{"tracer": "callTracer"}}}, blockRequest()); err != nil {
			return result, err
		}
	}
	childIndices := map[string]int{}
	childSeen := map[string]map[string]bool{}
	factoryLogs := []filter.Object{}
	factoryLogSeen := map[uintptr]bool{}
	failureIDs := map[string]bool{}
	failures, successes := 0, 0
	for _, factory := range s.factories {
		for _, log := range result.logs {
			if !filter.IsLogFactoryMatched(factory, log) {
				continue
			}
			identity := reflect.ValueOf(log).Pointer()
			if !factoryLogSeen[identity] {
				factoryLogSeen[identity] = true
				factoryLogs = append(factoryLogs, log)
			}
			address, err := filter.GetSourceChildAddress(log, factory)
			if err != nil {
				if !filter.IsUndefined(sourceHistoricalProperty(factory, "address")) {
					return result, err
				}
				failures++
				id, _ := factory["id"].(string)
				if !failureIDs[id] {
					failureIDs[id] = true
					if s.options.Logger != nil {
						index, _ := sourceHistoricalNumber(log["logIndex"])
						n, _ := sourceHistoricalNumber(log["blockNumber"])
						s.options.Logger.Debug(map[string]any{"msg": "Failed to extract child address from log matched by factory using the provided ABI item", "chain": s.chain.Name, "chain_id": s.chain.ID, "factory": factory["sourceId"], "block_number": n, "log_index": index, "data": log["data"], "topics": log["topics"]})
					}
				}
				continue
			}
			successes++
			parent, _ := log["address"].(string)
			fragment := filter.FactoryFragmentID(factory, parent)
			index, ok := childIndices[fragment]
			if !ok {
				index = len(result.children)
				childIndices[fragment] = index
				childSeen[fragment] = map[string]bool{}
				result.children = append(result.children, SourceRealtimeFragmentChildren{FragmentID: fragment, Addresses: []string{}})
			}
			if !childSeen[fragment][address] {
				childSeen[fragment][address] = true
				result.children[index].Addresses = append(result.children[index].Addresses, address)
			}
		}
	}
	if failures > 0 && s.options.Logger != nil {
		s.options.Logger.Debug(map[string]any{"msg": "Logs matched by factory contained child addresses that could not be extracted", "failure_count": failures, "success_count": successes})
	}
	requiredTransactions, requiredReceipts := newSourceHistoricalHashSet(), newSourceHistoricalHashSet()
	weakLogs := []filter.Object{}
	zeroHash := "0x" + strings.Repeat("0", 64)
	for _, log := range result.logs {
		matched := false
		for _, f := range s.kinds["log"] {
			if filter.IsLogFilterMatched(f, log) {
				matched = true
				hash, _ := log["transactionHash"].(string)
				if hash != zeroHash {
					requiredTransactions.add(hash)
					if f["hasTransactionReceipt"] == true {
						requiredReceipts.add(hash)
						break
					}
				}
			}
		}
		if matched {
			weakLogs = append(weakLogs, log)
		}
	}
	result.logs = weakLogs
	if !async && block != nil {
		checkText, _ := block["logsBloom"].(string)
		checkText = strings.TrimPrefix(checkText, "0x")
		if len(checkText)%2 != 0 {
			checkText = "0" + checkText
		}
		blockBits, err := hex.DecodeString(checkText)
		if err != nil {
			return result, err
		}
		checked := map[string]bool{}
		for _, log := range append(append([]filter.Object{}, factoryLogs...), result.logs...) {
			values := []string{}
			if value, ok := log["address"].(string); ok {
				values = append(values, value)
			}
			for _, topic := range sourceRealtimeTopics(log) {
				if topic != "" {
					values = append(values, topic)
				}
			}
			invalid := false
			for _, value := range values {
				if checked[value] {
					continue
				}
				contains, err := bloom.IsInBloom(blockBits, value)
				if err != nil {
					return result, err
				}
				if !contains {
					invalid = true
					break
				}
			}
			if invalid {
				index, _ := sourceHistoricalNumber(log["logIndex"])
				s.logWarn(map[string]any{"msg": "Detected inconsistent RPC responses. Log not found in block.logsBloom.", "action": "fetch_block_data", "chain": s.chain.Name, "chain_id": s.chain.ID, "number": blockNumber, "hash": hash, "logIndex": index})
				break
			}
			for _, value := range values {
				checked[value] = true
			}
		}
	}
	weakTraces := []rpc.CallTrace{}
	for _, trace := range result.traces {
		matched := false
		for _, kind := range []string{"transfer", "trace"} {
			for _, f := range s.kinds[kind] {
				valid, err := sourceRealtimeTraceMatched(kind, f, trace.Trace, header)
				if err != nil {
					return result, err
				}
				if valid {
					matched = true
					requiredTransactions.add(trace.TransactionHash)
					if f["hasTransactionReceipt"] == true {
						requiredReceipts.add(trace.TransactionHash)
						break
					}
				}
			}
		}
		if matched {
			weakTraces = append(weakTraces, trace)
		}
	}
	result.traces = weakTraces
	if block == nil && len(s.kinds["transaction"]) == 0 {
		return result, nil
	}
	if block == nil {
		block, err = s.getBlockByHash(ctx, hash, true)
		if err != nil {
			return result, err
		}
	}
	result.block = block
	if err := rpc.ValidateSourceTransactionsAndBlock(block, blockRequest()); err != nil {
		return result, err
	}
	allTransactions, err := sourceHistoricalTransactions(block)
	if err != nil {
		return result, err
	}
	for _, transaction := range allTransactions {
		hash, _ := transaction["hash"].(string)
		matched := requiredTransactions.seen[hash]
		for _, f := range s.kinds["transaction"] {
			if filter.IsTransactionFilterMatched(f, transaction) {
				matched = true
				requiredTransactions.add(hash)
				requiredReceipts.add(hash)
			}
		}
		if matched {
			result.transactions = append(result.transactions, transaction)
		}
	}
	responses, err := s.actions.GetTransactionReceipts(ctx, hash, requiredReceipts.values, &rpc.ActionOptions{Logger: s.options.RPCLogger})
	if err != nil {
		return result, err
	}
	for _, response := range responses {
		if err := rpc.ValidateSourceReceiptsAndBlock(response.Receipts, block, response.Request, blockRequest()); err != nil {
			return result, err
		}
		for _, receipt := range response.Receipts {
			hash, _ := receipt["transactionHash"].(string)
			if requiredReceipts.seen[hash] {
				result.receipts = append(result.receipts, receipt)
			}
		}
	}
	return result, nil
}
func sourceRealtimeTopics(log filter.Object) []string {
	switch xs := log["topics"].(type) {
	case []string:
		return xs
	case []any:
		out := []string{}
		for _, x := range xs {
			v, _ := x.(string)
			out = append(out, v)
		}
		return out
	}
	return []string{}
}
func sourceRealtimeTraceMatched(kind string, f, trace, block filter.Object) (bool, error) {
	if kind == "transfer" {
		return filter.IsTransferFilterMatched(f, trace, block)
	}
	return filter.IsTraceFilterMatched(f, trace, block), nil
}
func (s *SourceRealtimeSync) filterBlockEventData(data sourceRealtimeData) (sourceRealtimeData, bool, error) {
	n, err := sourceHistoricalNumber(data.block["number"])
	if err != nil {
		return data, false, err
	}
	for i, factory := range s.factories {
		for _, fragment := range s.factoryFragments[i] {
			for _, entry := range data.children {
				if entry.FragmentID != fragment {
					continue
				}
				for _, address := range entry.Addresses {
					id, _ := factory["id"].(string)
					if err := s.children.sourceRealtimeInsertFirst(id, filter.ChildAddress{Address: address, Block: n}); err != nil {
						return data, false, err
					}
				}
			}
		}
	}
	s.stateMu.Lock()
	s.childrenPerBlock[n] = data.children
	s.stateMu.Unlock()
	matched := false
	logs := []filter.Object{}
	for _, log := range data.logs {
		keep := false
		for _, f := range s.kinds["log"] {
			if !filter.IsLogFilterMatched(f, log) {
				continue
			}
			valid, err := sourceHistoricalFactoryMatch(sourceHistoricalProperty(f, "address"), sourceHistoricalProperty(log, "address"), n, s.children)
			if err != nil {
				return data, false, err
			}
			if valid {
				matched = true
				keep = true
			}
		}
		if keep {
			logs = append(logs, log)
		}
	}
	data.logs = logs
	traces := []rpc.CallTrace{}
	for _, trace := range data.traces {
		keep := false
		for _, kind := range []string{"transfer", "trace"} {
			for _, f := range s.kinds[kind] {
				valid, err := sourceRealtimeTraceMatched(kind, f, trace.Trace, data.block)
				if err != nil {
					return data, false, err
				}
				if !valid {
					continue
				}
				valid, err = sourceHistoricalFactoryEndsMatch(f, sourceHistoricalProperty(trace.Trace, "from"), sourceHistoricalProperty(trace.Trace, "to"), n, s.children)
				if err != nil {
					return data, false, err
				}
				if valid {
					matched = true
					keep = true
				}
			}
		}
		if keep {
			traces = append(traces, trace)
		}
	}
	data.traces = traces
	hashes := map[string]bool{}
	for _, log := range data.logs {
		hash, _ := log["transactionHash"].(string)
		hashes[hash] = true
	}
	for _, trace := range data.traces {
		hashes[trace.TransactionHash] = true
	}
	transactions := []filter.Object{}
	for _, transaction := range data.transactions {
		hash, _ := transaction["hash"].(string)
		keep := hashes[hash]
		for _, f := range s.kinds["transaction"] {
			if !filter.IsTransactionFilterMatched(f, transaction) {
				continue
			}
			valid, err := sourceHistoricalFactoryEndsMatch(f, sourceHistoricalProperty(transaction, "from"), sourceHistoricalProperty(transaction, "to"), n, s.children)
			if err != nil {
				return data, false, err
			}
			if valid {
				matched = true
				keep = true
			}
		}
		if keep {
			transactions = append(transactions, transaction)
		}
	}
	data.transactions = transactions
	for _, transaction := range data.transactions {
		hash, _ := transaction["hash"].(string)
		hashes[hash] = true
	}
	receipts := []filter.Object{}
	for _, receipt := range data.receipts {
		hash, _ := receipt["transactionHash"].(string)
		if hashes[hash] {
			receipts = append(receipts, receipt)
		}
	}
	data.receipts = receipts
	for _, f := range s.kinds["block"] {
		if filter.IsBlockFilterMatched(f, data.block) {
			matched = true
		}
	}
	return data, matched, nil
}
