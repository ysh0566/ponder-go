package runtime

import (
	"context"
	"fmt"
	"strings"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
)

func (e *Engine) accountEvents(ctx context.Context, b Block, number uint64) ([]pending, error) {
	callbacks := []protocol.Object{}
	needsTrace := false

	childrenByFactory := map[string][]filter.ChildAddress{}
	for _, source := range e.Config.Contracts {
		if !source.IncludeCallTraces || number < source.StartBlock || source.EndBlock != nil && number > *source.EndBlock {
			continue
		}
		a := e.abis[source.Name]
		functions := a.FunctionEntries()
		counts := map[string]int{}
		for _, item := range functions {
			counts[item.Name]++
		}
		registered := map[string]bool{}
		for _, item := range functions {
			selector := abi.FunctionSelector(item)
			name := source.Name + "." + item.SafeName(counts[item.Name] > 1)
			if e.Handlers[name] == nil || registered[name] {
				continue
			}
			registered[name] = true
			address := nativeContractFilter(source, selector)["address"]
			if factory, ok := e.factories[source.Name]; ok {
				address = nativeFactory(factory)
				children, err := e.Store.FactoryChildren(ctx, factory.ID, e.chain.ID, number)
				if err != nil {
					return nil, err
				}
				entries := []filter.ChildAddress{}
				for child, birth := range children {
					entries = append(entries, filter.ChildAddress{Address: child, Block: birth})
				}
				childrenByFactory[factory.ID] = entries
			}
			f := protocol.Object{"type": "trace", "chainId": e.chain.ID, "sourceId": source.Name, "fromAddress": filter.Undefined, "toAddress": address, "callType": "CALL", "functionSelector": selector, "fromBlock": source.StartBlock, "toBlock": filter.Undefined, "hasTransactionReceipt": source.IncludeTransactionReceipts}
			if source.EndBlock != nil {
				f["toBlock"] = *source.EndBlock
			}
			callbacks = append(callbacks, protocol.ContractCallback[abi.Function]{CallbackBase: protocol.CallbackBase{Name: name, Chain: protocol.Object{"id": e.chain.ID, "name": e.chain.Name}, Filter: f}, ABIItem: item}.CallbackObject())
			needsTrace = true
		}
	}
	for _, source := range e.Config.Accounts {
		if number < source.StartBlock || source.EndBlock != nil && number > *source.EndBlock {
			continue
		}
		var address any = filter.Undefined
		if source.Address != nil {
			xs := []any{}
			for _, a := range source.Address {
				xs = append(xs, strings.ToLower(a))
			}
			address = xs
		}
		for _, kind := range []string{"transaction", "transfer"} {
			for _, direction := range []string{"from", "to"} {
				name := source.Name + ":" + kind + ":" + direction
				if e.Handlers[name] == nil {
					continue
				}
				f := protocol.Object{"type": kind, "chainId": e.chain.ID, "fromAddress": filter.Undefined, "toAddress": filter.Undefined, "fromBlock": source.StartBlock, "toBlock": filter.Undefined, "hasTransactionReceipt": kind == "transaction"}
				if source.EndBlock != nil {
					f["toBlock"] = *source.EndBlock
				}
				f[direction+"Address"] = address
				callbacks = append(callbacks, protocol.Object{"name": name, "type": "account", "filter": f})
				needsTrace = needsTrace || kind == "transfer"
			}
		}
	}
	out := []pending{}
	if len(callbacks) == 0 {
		return out, nil
	}
	rawBlock, err := projectionObject(b)
	if err != nil {
		return nil, err
	}
	block, err := protocol.SyncBlockToInternal(rawBlock)
	if err != nil {
		return nil, err
	}
	batch := protocol.Batch{EventCallbacks: callbacks, Blocks: []protocol.Object{block}, ChainID: e.chain.ID, ChildAddresses: childrenByFactory}
	txByHash := map[string]map[string]any{}
	receiptByIndex := map[string]map[string]any{}
	for _, tx := range b.Transactions {
		value, err := protocol.SyncTransactionToInternal(tx)
		if err != nil {
			return nil, err
		}
		batch.Transactions = append(batch.Transactions, value)
		hash, _ := tx["hash"].(string)
		txByHash[hash] = tx
		var receipt map[string]any
		if err = e.Client.CallCached(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
			return nil, err
		}
		if receipt == nil {
			return nil, fmt.Errorf("missing receipt for %s", hash)
		}
		if receipt["transactionHash"] != hash || receipt["blockHash"] != b.Hash || receipt["transactionIndex"] != tx["transactionIndex"] || receipt["blockNumber"] != b.Number {
			return nil, fmt.Errorf("account receipt provenance mismatch")
		}
		internal, err := protocol.SyncTransactionReceiptToInternal(receipt)
		if err != nil {
			return nil, err
		}
		batch.TransactionReceipts = append(batch.TransactionReceipts, internal)
		receiptByIndex[fmt.Sprint(internal["transactionIndex"])] = receipt
		if e.cache != nil {
			if err = e.cache.Insert(ctx, "transaction_receipts", e.chain.ID, []map[string]any{receipt}); err != nil {
				return nil, err
			}
		}
	}
	if needsTrace && len(b.Transactions) > 0 {
		traces, err := e.Client.TraceBlock(ctx, b.Hash, b.Transactions)
		if err != nil {
			return nil, err
		}
		cacheRows := []map[string]any{}
		for _, trace := range traces {
			tx, ok := txByHash[trace.TransactionHash]
			if !ok {
				return nil, fmt.Errorf("trace has no transaction in canonical block")
			}
			internal, err := protocol.SyncTraceToInternal(protocol.Object{"trace": trace.Trace}, rawBlock, tx)
			if err != nil {
				return nil, err
			}
			batch.Traces = append(batch.Traces, internal)
			row := map[string]any{}
			for k, v := range trace.Trace {
				row[k] = v
			}
			row["blockNumber"] = b.Number
			row["transactionIndex"] = tx["transactionIndex"]
			cacheRows = append(cacheRows, row)
		}
		if e.cache != nil && len(cacheRows) > 0 {
			if err = e.cache.Insert(ctx, "traces", e.chain.ID, cacheRows); err != nil {
				return nil, err
			}
		}
	}
	built, err := protocol.BuildEvents(batch)
	if err != nil {
		return nil, err
	}

	for _, raw := range built {
		callback := callbacks[raw.EventCallbackIndex]
		name := callback["name"].(string)
		cp, err := checkpoint.Decode(raw.Checkpoint)
		if err != nil {
			return nil, err
		}
		hash, _ := raw.Transaction["hash"].(string)
		event := Event{Name: name, Block: b, Transaction: txByHash[hash], Receipt: receiptByIndex[fmt.Sprint(raw.Transaction["transactionIndex"])], Normalized: &raw}
		if callback["type"] == "contract" {
			event.Trace = raw.Trace
			if callback["filter"].(map[string]any)["hasTransactionReceipt"] != true {
				event.Receipt = nil
			}
		} else if raw.Trace != nil {
			event.Trace = raw.Trace
			event.Transfer = map[string]any{"from": raw.Trace["from"], "to": raw.Trace["to"], "value": raw.Trace["value"]}
			event.Args = event.Transfer
		}
		out = append(out, pending{cursor: cp, id: fmt.Sprintf("account:%d:%s:%s:%s", e.chain.ID, b.Hash, name, raw.Checkpoint), event: event, callback: callback})
	}
	return out, nil
}
