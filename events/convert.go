package events

import "ponder.local/ponder-go/filter"

func SyncBlockToInternal(block Object) (Object, error) {
	o := copyFields(block, "extraData", "hash", "logsBloom", "mixHash", "nonce", "parentHash", "receiptsRoot", "sha3Uncles", "stateRoot", "transactionsRoot")
	o["miner"] = lower(get(block, "miner"))
	if e := bigFields(o, block, "difficulty", "gasLimit", "gasUsed", "number", "size", "timestamp"); e != nil {
		return nil, e
	}
	for _, k := range []string{"baseFeePerGas", "totalDifficulty"} {
		v, e := optionalBig(get(block, k))
		if e != nil {
			return nil, e
		}
		o[k] = v
	}
	return o, nil
}
func SyncLogToInternal(log Object) (Object, error) {
	o := copyFields(log, "data", "topics")
	o["address"] = lower(get(log, "address"))
	o["removed"] = false
	if e := numberFields(o, log, "blockNumber", "logIndex", "transactionIndex"); e != nil {
		return nil, e
	}
	return o, nil
}
func SyncTransactionToInternal(tx Object) (Object, error) {
	o := copyFields(tx, "hash", "input", "r", "s")
	o["from"] = lower(get(tx, "from"))
	o["to"] = lower(get(tx, "to"))
	o["type"] = transactionType(get(tx, "type"))
	if e := bigFields(o, tx, "gas", "value"); e != nil {
		return nil, e
	}
	if e := numberFields(o, tx, "blockNumber", "transactionIndex", "nonce"); e != nil {
		return nil, e
	}
	v, e := optionalBig(get(tx, "v"))
	if e != nil {
		return nil, e
	}
	o["v"] = v
	switch get(tx, "type") {
	case "0x0", "0x1":
		if e = bigFields(o, tx, "gasPrice"); e != nil {
			return nil, e
		}
		if get(tx, "type") == "0x1" {
			o["accessList"] = get(tx, "accessList")
		}
	case "0x2":
		if e = bigFields(o, tx, "maxFeePerGas", "maxPriorityFeePerGas"); e != nil {
			return nil, e
		}
	case "0x7e":
		for _, k := range []string{"maxFeePerGas", "maxPriorityFeePerGas"} {
			if nullish(get(tx, k)) || get(tx, k) == "" {
				o[k] = filter.Undefined
			} else {
				o[k], e = integer(get(tx, k))
				if e != nil {
					return nil, e
				}
			}
		}
	}
	return o, nil
}
func SyncTransactionReceiptToInternal(receipt Object) (Object, error) {
	o := copyFields(receipt, "logsBloom")
	if e := numberFields(o, receipt, "blockNumber", "transactionIndex"); e != nil {
		return nil, e
	}
	if e := bigFields(o, receipt, "cumulativeGasUsed", "effectiveGasPrice", "gasUsed"); e != nil {
		return nil, e
	}
	o["from"] = lower(get(receipt, "from"))
	for _, k := range []string{"contractAddress", "to"} {
		o[k] = nil
		if !nullish(get(receipt, k)) && get(receipt, k) != "" {
			o[k] = lower(get(receipt, k))
		}
	}
	o["status"] = get(receipt, "status")
	switch o["status"] {
	case "0x1":
		o["status"] = "success"
	case "0x0":
		o["status"] = "reverted"
	}
	o["type"] = transactionType(get(receipt, "type"))
	return o, nil
}
func SyncTraceToInternal(trace, block, transaction Object) (Object, error) {
	frame := object(trace["trace"])
	o := copyFields(frame, "traceAddress", "type", "input", "output")
	o["from"] = lower(get(frame, "from"))
	o["to"] = nil
	if !nullish(get(frame, "to")) && get(frame, "to") != "" {
		o["to"] = lower(get(frame, "to"))
	}
	if e := bigFields(o, frame, "gas", "gasUsed"); e != nil {
		return nil, e
	}
	n, e := quantity(get(block, "number"))
	if e != nil {
		return nil, e
	}
	o["blockNumber"] = n
	n, e = quantity(get(transaction, "transactionIndex"))
	if e != nil {
		return nil, e
	}
	o["transactionIndex"] = n
	o["value"], e = optionalBig(get(frame, "value"))
	return o, e
}
