package events

import (
	"fmt"
	"sort"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
)

type Batch struct {
	EventCallbacks      []Object                         `json:"eventCallbacks"`
	Blocks              []Object                         `json:"blocks"`
	Logs                []Object                         `json:"logs"`
	Transactions        []Object                         `json:"transactions"`
	TransactionReceipts []Object                         `json:"transactionReceipts"`
	Traces              []Object                         `json:"traces"`
	ChildAddresses      map[string][]filter.ChildAddress `json:"-"`
	ChainID             uint64                           `json:"chainId"`
}
type RawEvent struct {
	ChainID                                            uint64
	EventCallbackIndex                                 int
	Checkpoint                                         string
	Block, Log, Trace, Transaction, TransactionReceipt Object
}

func optionalObject(o Object) any {
	if o == nil {
		return filter.Undefined
	}
	return o
}
func (e RawEvent) Object() Object {
	return Object{"chainId": e.ChainID, "eventCallbackIndex": e.EventCallbackIndex, "checkpoint": e.Checkpoint, "block": e.Block, "log": optionalObject(e.Log), "trace": optionalObject(e.Trace), "transaction": optionalObject(e.Transaction), "transactionReceipt": optionalObject(e.TransactionReceipt)}
}
func rawObject(o Object) RawEvent {
	return RawEvent{ChainID: mustNumber(o["chainId"]), EventCallbackIndex: int(mustNumber(o["eventCallbackIndex"])), Checkpoint: text(o["checkpoint"]), Block: object(o["block"]), Log: object(o["log"]), Trace: object(o["trace"]), Transaction: object(o["transaction"]), TransactionReceipt: object(o["transactionReceipt"])}
}

// BuildEvents follows the source's sorted normalized-input contract. Receipt
// matching uses (blockNumber,transactionIndex), never receipt array position.
// Raw maps are retained by reference, including the matched receipt.
func BuildEvents(b Batch) ([]RawEvent, error) {
	result := []RawEvent{}
	indexes := map[string][]int{}
	for i, c := range b.EventCallbacks {
		f := object(c["filter"])
		if mustNumber(f["chainId"]) == b.ChainID {
			kind := text(f["type"])
			indexes[kind] = append(indexes[kind], i)
		}
	}
	makeEvent := func(i int, block, tx, receipt, log, trace Object, eventType int, index uint64) (RawEvent, error) {
		n, e := quantity(block["number"])
		if e != nil {
			return RawEvent{}, e
		}
		timestamp, e := quantity(block["timestamp"])
		if e != nil {
			return RawEvent{}, e
		}
		txIndex := checkpoint.Max.TransactionIndex
		if tx != nil {
			txIndex = mustNumber(tx["transactionIndex"])
		}
		if log != nil {
			txIndex = mustNumber(log["transactionIndex"])
		}
		cp, e := checkpoint.Encode(checkpoint.Checkpoint{BlockTimestamp: timestamp, ChainID: b.ChainID, BlockNumber: n, TransactionIndex: txIndex, EventType: eventType, EventIndex: index})
		return RawEvent{b.ChainID, i, cp, block, log, trace, tx, receipt}, e
	}
	appendEvent := func(i int, block, tx, receipt, log, trace Object, eventType int, index uint64) error {
		e, err := makeEvent(i, block, tx, receipt, log, trace, eventType, index)
		if err == nil {
			result = append(result, e)
		}
		return err
	}
	factoryMatched := func(f, event Object, n uint64, keys ...string) (bool, error) {
		for _, key := range keys {
			value := get(f, key)
			if !filter.IsAddressFactory(value) {
				continue
			}
			factory := object(value)
			children, ok := b.ChildAddresses[text(factory["id"])]
			if !ok {
				return false, fmt.Errorf("missing factory child map %s", text(factory["id"]))
			}
			eventKey := "address"
			if key == "fromAddress" {
				eventKey = "from"
			}
			if key == "toAddress" {
				eventKey = "to"
			}
			address := get(event, eventKey)
			if nullish(address) {
				address = filter.Undefined
			}
			if !filter.IsAddressMatched(address, float64(n), children) {
				return false, nil
			}
		}
		return true, nil
	}
	blockAt := func(n uint64, position *int) (Object, error) {
		for *position < len(b.Blocks) && mustNumber(b.Blocks[*position]["number"]) < n {
			*position++
		}
		if *position >= len(b.Blocks) {
			return nil, fmt.Errorf("Failed to build events from block data. Missing block %d for chain ID %d", n, b.ChainID)
		}
		return b.Blocks[*position], nil
	}
	itemAt := func(xs []Object, n, index uint64, position *int) Object {
		for *position < len(xs) && (mustNumber(xs[*position]["blockNumber"]) < n || mustNumber(xs[*position]["blockNumber"]) == n && mustNumber(xs[*position]["transactionIndex"]) < index) {
			*position++
		}
		if *position < len(xs) && mustNumber(xs[*position]["blockNumber"]) == n && mustNumber(xs[*position]["transactionIndex"]) == index {
			return xs[*position]
		}
		return nil
	}
	missingReceipt := func(n, index uint64) error {
		return fmt.Errorf("Failed to build events from block data. Missing transaction receipt for block %d and transaction index %d for chain ID %d", n, index, b.ChainID)
	}
	for _, block := range b.Blocks {
		for _, i := range indexes["block"] {
			if filter.IsBlockFilterMatched(object(b.EventCallbacks[i]["filter"]), block) {
				if e := appendEvent(i, block, nil, nil, nil, nil, checkpoint.Blocks, 0); e != nil {
					return nil, e
				}
			}
		}
	}
	blockPosition, txPosition, receiptPosition := 0, 0, 0
	for _, tx := range b.Transactions {
		n, index := mustNumber(tx["blockNumber"]), mustNumber(tx["transactionIndex"])
		block, e := blockAt(n, &blockPosition)
		if e != nil {
			return nil, e
		}
		receipt := itemAt(b.TransactionReceipts, n, index, &receiptPosition)
		for _, i := range indexes["transaction"] {
			f := object(b.EventCallbacks[i]["filter"])
			if !filter.IsTransactionFilterMatched(f, tx) {
				continue
			}
			ok, e := factoryMatched(f, tx, n, "fromAddress", "toAddress")
			if e != nil {
				return nil, e
			}
			if !ok {
				continue
			}
			// Source transaction filters require receipts to exclude reverted txs.
			if receipt == nil {
				return nil, missingReceipt(n, index)
			}
			if receipt["status"] != "success" {
				continue
			}
			if e = appendEvent(i, block, tx, receipt, nil, nil, checkpoint.Transactions, 0); e != nil {
				return nil, e
			}
		}
	}
	blockPosition, receiptPosition = 0, 0
	var traceIndex uint64
	previous := ""
	for _, trace := range b.Traces {
		key := indexKey(trace)
		if key != previous {
			traceIndex = 0
			previous = key
		}
		before := len(result)
		n, index := mustNumber(trace["blockNumber"]), mustNumber(trace["transactionIndex"])
		block, e := blockAt(n, &blockPosition)
		if e != nil {
			return nil, e
		}
		tx := itemAt(b.Transactions, n, index, &txPosition)
		if tx == nil {
			return nil, fmt.Errorf("Failed to build events from block data. Missing transaction for block %d and transaction index %d for chain ID %d", n, index, b.ChainID)
		}
		receipt := itemAt(b.TransactionReceipts, n, index, &receiptPosition)
		for _, kind := range []string{"trace", "transfer"} {
			for _, i := range indexes[kind] {
				f := object(b.EventCallbacks[i]["filter"])
				matched := false
				if kind == "trace" {
					matched = filter.IsTraceFilterMatched(f, trace, block)
				} else {
					matched, e = filter.IsTransferFilterMatched(f, trace, block)
					if e != nil {
						return nil, e
					}
				}
				if !matched {
					continue
				}
				ok, e := factoryMatched(f, trace, n, "fromAddress", "toAddress")
				if e != nil {
					return nil, e
				}
				if !ok {
					continue
				}
				var selected Object
				if f["hasTransactionReceipt"] == true {
					if receipt == nil {
						return nil, missingReceipt(n, index)
					}
					selected = receipt
				}
				if e = appendEvent(i, block, tx, selected, nil, trace, checkpoint.Traces, traceIndex); e != nil {
					return nil, e
				}
			}
		}
		if len(result) > before {
			traceIndex++
		}
	}
	blockPosition, txPosition, receiptPosition = 0, 0, 0
	for _, log := range b.Logs {
		n, index := mustNumber(log["blockNumber"]), mustNumber(log["transactionIndex"])
		block, e := blockAt(n, &blockPosition)
		if e != nil {
			return nil, e
		}
		tx := itemAt(b.Transactions, n, index, &txPosition)
		receipt := itemAt(b.TransactionReceipts, n, index, &receiptPosition)
		for _, i := range indexes["log"] {
			f := object(b.EventCallbacks[i]["filter"])
			if !filter.IsLogFilterMatched(f, log) {
				continue
			}
			ok, e := factoryMatched(f, log, n, "address")
			if e != nil {
				return nil, e
			}
			if !ok {
				continue
			}
			var selected Object
			if f["hasTransactionReceipt"] == true {
				if receipt == nil {
					return nil, missingReceipt(n, index)
				}
				selected = receipt
			}
			if e = appendEvent(i, block, tx, selected, log, nil, checkpoint.Logs, mustNumber(log["logIndex"])); e != nil {
				return nil, e
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Checkpoint < result[j].Checkpoint })
	return result, nil
}
