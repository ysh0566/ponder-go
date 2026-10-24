package store

import (
	"context"
	"fmt"

	"ponder.local/ponder-go/filter"
)

func sourceLiveInvoke(ctx context.Context, s *SourceSyncStore, operation string, args filter.Object) (any, error) {
	void := func(e error) (any, error) { return filter.Undefined, e }
	var chain uint64
	if value, ok := args["chainId"]; ok {
		n, e := sourceLiveUint(value)
		if e != nil {
			return nil, e
		}
		chain = n
	}
	switch operation {
	case "getFilterBlockRange":
		filters, e := sourceLiveObjects(args["filters"])
		if e != nil {
			return nil, e
		}
		bounds := []SourceFilterRange{}
		for _, f := range filters {
			r, e := sourceRangeFromFilter(f)
			if e != nil {
				return nil, e
			}
			bounds = append(bounds, r)
		}
		from, e := sourceLiveNumber(args["fromBlock"])
		if e != nil {
			return nil, e
		}
		to, e := sourceLiveNumber(args["toBlock"])
		if e != nil {
			return nil, e
		}
		return GetSourceFilterBlockRange(bounds, from, to), nil
	case "insertIntervals":
		values, e := sourceLiveObjects(args["intervals"])
		if e != nil {
			return nil, e
		}
		intervals := []SourceFilterInterval{}
		for _, v := range values {
			f, e := sourceLiveObject(v["filter"])
			if e != nil {
				return nil, e
			}
			span, e := sourceLiveSpan(v["interval"])
			if e != nil {
				return nil, e
			}
			intervals = append(intervals, SourceFilterInterval{f, span})
		}
		values, e = sourceLiveObjects(args["factoryIntervals"])
		if e != nil {
			return nil, e
		}
		factories := []SourceFactoryInterval{}
		for _, v := range values {
			f, e := sourceLiveObject(v["factory"])
			if e != nil {
				return nil, e
			}
			span, e := sourceLiveSpan(v["interval"])
			if e != nil {
				return nil, e
			}
			factories = append(factories, SourceFactoryInterval{f, span})
		}
		return void(s.InsertIntervals(ctx, chain, intervals, factories))
	case "getIntervals":
		filters, e := sourceLiveObjects(args["filters"])
		if e != nil {
			return nil, e
		}
		return s.GetIntervals(ctx, filters)
	case "insertChildAddresses":
		entries, ok := args["childAddresses"].(sourceLiveMap)
		if !ok {
			return nil, fmt.Errorf("source child addresses must be a Map")
		}
		children := []SourceFactoryChildren{}
		for _, entry := range entries {
			id, e := sourceLiveString(entry.Key)
			if e != nil {
				return nil, e
			}
			values, ok := entry.Value.(sourceLiveMap)
			if !ok {
				return nil, fmt.Errorf("source children must be a Map")
			}
			item := SourceFactoryChildren{FragmentID: id, Children: []SourceChildAddress{}}
			for _, child := range values {
				address, e := sourceLiveString(child.Key)
				if e != nil {
					return nil, e
				}
				block, e := sourceLiveUint(child.Value)
				if e != nil {
					return nil, e
				}
				item.Children = append(item.Children, SourceChildAddress{Address: address, BlockNumber: block})
			}
			children = append(children, item)
		}
		return void(s.InsertChildAddresses(ctx, chain, children))
	case "getChildAddresses":
		factory, e := sourceLiveObject(args["factory"])
		if e != nil {
			return nil, e
		}
		return s.GetChildAddresses(ctx, factory)
	case "getSafeCrashRecoveryBlock":
		timestamp, e := sourceLiveInt(args["timestamp"])
		if e != nil {
			return nil, e
		}
		return s.GetSafeCrashRecoveryBlock(ctx, chain, int64(timestamp))
	case "insertLogs", "insertBlocks", "insertTransactions", "insertTransactionReceipts":
		key := map[string]string{"insertLogs": "logs", "insertBlocks": "blocks", "insertTransactions": "transactions", "insertTransactionReceipts": "transactionReceipts"}[operation]
		values, e := sourceLiveObjects(args[key])
		if e != nil {
			return nil, e
		}
		switch operation {
		case "insertLogs":
			return void(s.InsertLogs(ctx, chain, values))
		case "insertBlocks":
			return void(s.InsertBlocks(ctx, chain, values))
		case "insertTransactions":
			return void(s.InsertTransactions(ctx, chain, values))
		default:
			return void(s.InsertTransactionReceipts(ctx, chain, values))
		}
	case "insertTraces":
		values, e := sourceLiveObjects(args["traces"])
		if e != nil {
			return nil, e
		}
		traces := []SourceTraceInput{}
		for _, v := range values {
			trace, e := sourceLiveObject(v["trace"])
			if e != nil {
				return nil, e
			}
			block, e := sourceLiveObject(v["block"])
			if e != nil {
				return nil, e
			}
			transaction, e := sourceLiveObject(v["transaction"])
			if e != nil {
				return nil, e
			}
			traces = append(traces, SourceTraceInput{Trace: trace, Block: block, Transaction: transaction})
		}
		return void(s.InsertTraces(ctx, chain, traces))
	case "getEventData":
		filters, e := sourceLiveObjects(args["filters"])
		if e != nil {
			return nil, e
		}
		from, e := sourceLiveNumber(args["fromBlock"])
		if e != nil {
			return nil, e
		}
		to, e := sourceLiveNumber(args["toBlock"])
		if e != nil {
			return nil, e
		}
		limit, e := sourceLiveInt(args["limit"])
		if e != nil {
			return nil, e
		}
		return s.GetEventData(ctx, SourceEventDataArgs{Filters: filters, FromBlock: from, ToBlock: to, ChainID: chain, Limit: limit})
	case "insertRpcRequestResults":
		values, e := sourceLiveObjects(args["requests"])
		if e != nil {
			return nil, e
		}
		requests := []SourceRPCRawResult{}
		for _, v := range values {
			blockValue := v["blockNumber"]
			block, e := sourceLiveOptionalBlock(blockValue)
			if e != nil {
				return nil, e
			}
			result, e := sourceLiveString(v["result"])
			if e != nil {
				return nil, e
			}
			requests = append(requests, SourceRPCRawResult{Request: v["request"], BlockNumber: block, Result: result})
		}
		return void(s.InsertRPCRawRequestResults(ctx, chain, requests))
	case "getRpcRequestResults":
		requests, e := sourceLiveList(args["requests"])
		if e != nil {
			return nil, e
		}
		return s.GetRPCRawRequestResults(ctx, chain, requests)
	case "pruneRpcRequestResults":
		values, e := sourceLiveObjects(args["blocks"])
		if e != nil {
			return nil, e
		}
		blocks := []uint64{}
		for _, v := range values {
			n, e := sourceLiveHex(v["number"])
			if e != nil {
				return nil, e
			}
			blocks = append(blocks, n)
		}
		return void(s.PruneRPCRequestResults(ctx, chain, blocks))
	case "pruneByChain":
		return void(s.PruneByChain(ctx, chain))
	default:
		return nil, fmt.Errorf("unknown source SyncStore operation %q", operation)
	}
}
