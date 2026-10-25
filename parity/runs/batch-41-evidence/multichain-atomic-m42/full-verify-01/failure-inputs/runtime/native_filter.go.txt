package runtime

import (
	"strings"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/factory"
	selection "ponder.local/ponder-go/filter"
)

func nativeFactory(f factory.LogFactory) selection.Object {
	a := f.Address
	if a == nil {
		a = selection.Undefined
	}
	o := selection.Object{"id": f.ID, "type": "log", "chainId": f.ChainID, "sourceId": f.SourceID, "address": a, "eventSelector": f.EventSelector, "childAddressLocation": f.ChildAddressLocation, "fromBlock": selection.Undefined, "toBlock": selection.Undefined}
	if f.FromBlock != nil {
		o["fromBlock"] = *f.FromBlock
	}
	if f.ToBlock != nil {
		o["toBlock"] = *f.ToBlock
	}
	return o
}
func nativeLog(log Log, number uint64) selection.Object {
	block := log.BlockNumber
	if block == "" {
		block = Hex(number)
	}
	return selection.Object{"address": log.Address, "topics": log.Topics, "data": log.Data, "blockNumber": block}
}
func nativeContractFilter(contract config.Contract, selector string) selection.Object {
	a := any(selection.Undefined)
	if contract.Address != nil {
		xs := make([]string, len(contract.Address))
		for i, v := range contract.Address {
			xs[i] = strings.ToLower(v)
		}
		a = xs
	}
	o := selection.Object{"type": "log", "address": a, "topic0": selector, "topic1": nil, "topic2": nil, "topic3": nil, "fromBlock": contract.StartBlock, "toBlock": selection.Undefined}
	if contract.EndBlock != nil {
		o["toBlock"] = *contract.EndBlock
	}
	return o
}

// Request planning may deliberately widen a dimension. Actual event dispatch
// always applies the source predicate and factory birth-block check afterward.
func plannedBlockLogs(address any, hash string) (selection.Object, bool) {
	v := selection.MergeLogFiltersToBlockRequest([]selection.Object{{"address": address, "topic0": nil, "topic1": nil, "topic2": nil, "topic3": nil}}, nil)
	if selection.IsUndefined(v) {
		return nil, false
	}
	query := selection.RPCValue(v).(map[string]any)
	query["blockHash"] = hash
	return query, true
}
