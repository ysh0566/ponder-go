package events

import (
	"encoding/json"
	"fmt"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/filter"
)

type DebugLogger interface{ Debug(Object) }
type DecodeStats struct{ LogSuccess, LogFailure, TraceSuccess, TraceFailure int }
type Decoder struct {
	Logger DebugLogger
	Stats  DecodeStats
}

func (d *Decoder) debug(fields Object) {
	if d.Logger != nil {
		d.Logger.Debug(fields)
	}
}
func metadata(item any, destination any) error {
	data, e := json.Marshal(item)
	if e != nil {
		return e
	}
	return json.Unmarshal(data, destination)
}
func stringList(v any) []string {
	out := []string{}
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		for _, x := range xs {
			out = append(out, text(x))
		}
	}
	return out
}

// DecodeEvents preserves source batch-local failure-selector dedup and logging.
// Invalid matched ABI data is counted/logged and omitted, never a business event.
func (d *Decoder) DecodeEvents(chain Object, callbacks []Object, raw []RawEvent) ([]Object, error) {
	d.Stats = DecodeStats{}
	logFailures, traceFailures := map[string]bool{}, map[string]bool{}
	result := []Object{}
	for _, event := range raw {
		if event.EventCallbackIndex < 0 || event.EventCallbackIndex >= len(callbacks) {
			return nil, fmt.Errorf("invalid event callback index")
		}
		callback := callbacks[event.EventCallbackIndex]
		kind := text(object(callback["filter"])["type"])
		if callback["type"] != "contract" || (kind != "log" && kind != "trace") {
			decoded, e := DecodePublicEvents(chain, callbacks, []RawEvent{event})
			if e != nil {
				return nil, e
			}
			result = append(result, decoded...)
			continue
		}
		body := Object{"id": event.Checkpoint, "block": event.Block, "transaction": optionalObject(event.Transaction), "transactionReceipt": optionalObject(event.TransactionReceipt)}
		callbackChain := object(callback["chain"])
		if callbackChain == nil {
			callbackChain = chain
		}
		blockNumber := get(event.Block, "number")
		if nullish(blockNumber) {
			blockNumber = "unknown"
		}
		if kind == "log" {
			var item abi.Event
			if e := metadata(callback["abiItem"], &item); e != nil {
				return nil, e
			}
			args, e := abi.DecodeEventLog(item, stringList(get(event.Log, "topics")), text(get(event.Log, "data")))
			if e != nil {
				d.Stats.LogFailure++
				selector := abi.EventSelector(item)
				if !logFailures[selector] {
					logFailures[selector] = true
					topics, _ := json.Marshal(get(event.Log, "topics"))
					d.debug(Object{"msg": "Failed to decode matched event log using provided ABI item", "chain": get(callbackChain, "name"), "chain_id": get(callbackChain, "id"), "event": get(callback, "name"), "block_number": blockNumber, "log_index": get(event.Log, "logIndex"), "data": get(event.Log, "data"), "topics": string(topics)})
				}
				continue
			}
			d.Stats.LogSuccess++
			body["args"] = args
			body["log"] = event.Log
		} else {
			var item abi.Function
			if e := metadata(callback["abiItem"], &item); e != nil {
				return nil, e
			}
			output := get(event.Trace, "output")
			if nullish(output) {
				output = "0x"
			}
			args, values, e := item.DecodeCall(text(get(event.Trace, "input")), text(output))
			if e != nil {
				d.Stats.TraceFailure++
				selector := abi.FunctionSelector(item)
				if !traceFailures[selector] {
					traceFailures[selector] = true
					var address any = filter.Undefined
					if event.Trace != nil {
						data, _ := json.Marshal(get(event.Trace, "traceAddress"))
						address = string(data)
					}
					d.debug(Object{"msg": "Failed to decode matched call trace using provided ABI item", "chain": get(callbackChain, "name"), "chain_id": get(callbackChain, "id"), "function": get(callback, "name"), "block_number": blockNumber, "transaction_index": get(event.Transaction, "transactionIndex"), "trace_address": address, "input": get(event.Trace, "input"), "output": get(event.Trace, "output")})
				}
				continue
			}
			d.Stats.TraceSuccess++
			body["args"] = filter.Undefined
			if len(item.Inputs) > 0 {
				body["args"] = args
			}
			body["result"] = filter.Undefined
			if len(item.Outputs) == 1 {
				body["result"] = values[0]
			} else if len(item.Outputs) > 1 {
				body["result"] = values
			}
			body["trace"] = event.Trace
		}
		result = append(result, Object{"type": kind, "checkpoint": event.Checkpoint, "chain": chain, "eventCallback": callback, "event": body})
	}
	if d.Stats.LogFailure > 0 {
		d.debug(Object{"msg": "Event batch contained logs that could not be decoded", "failure_count": d.Stats.LogFailure, "success_count": d.Stats.LogSuccess})
	}
	if d.Stats.TraceFailure > 0 {
		d.debug(Object{"msg": "Event batch contained traces that could not be decoded", "failure_count": d.Stats.TraceFailure, "success_count": d.Stats.TraceSuccess})
	}
	return result, nil
}
