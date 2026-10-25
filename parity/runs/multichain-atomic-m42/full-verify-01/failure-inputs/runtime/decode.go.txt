package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
)

func projectionDefaults(input, defaults protocol.Object) protocol.Object {
	result := make(protocol.Object, len(input)+len(defaults))
	for key, value := range input {
		result[key] = value
	}
	for key, value := range defaults {
		if _, present := result[key]; !present {
			result[key] = value
		}
	}
	return result
}

func projectionObject(value any) (protocol.Object, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result protocol.Object
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err = decoder.Decode(&result)
	return result, err
}

func normalizedContractLog(block Block, log Log, transaction, receipt map[string]any, cursor checkpoint.Checkpoint) (*protocol.RawEvent, error) {
	rawBlock, err := projectionObject(block)
	if err != nil {
		return nil, err
	}
	// Legacy handlers accept envelopes with omitted optional EVM quantities.
	// Apply defaults only to the projection, keeping raw handler input intact.
	rawBlock = projectionDefaults(rawBlock, protocol.Object{"difficulty": "0x0", "gasLimit": "0x0", "gasUsed": "0x0", "size": "0x0", "baseFeePerGas": "0x0", "totalDifficulty": "0x0"})
	internalBlock, err := protocol.SyncBlockToInternal(rawBlock)
	if err != nil {
		return nil, err
	}
	if log.BlockNumber == "" {
		log.BlockNumber = block.Number
	}
	rawLog, err := projectionObject(log)
	if err != nil {
		return nil, err
	}
	internalLog, err := protocol.SyncLogToInternal(rawLog)
	if err != nil {
		return nil, err
	}
	var internalTransaction, internalReceipt protocol.Object
	if transaction != nil {
		projected := projectionDefaults(transaction, protocol.Object{"blockNumber": block.Number, "transactionIndex": Hex(cursor.TransactionIndex), "gas": "0x0", "value": "0x0", "gasPrice": "0x0", "nonce": "0x0", "v": "0x0", "r": "0x0", "s": "0x0", "type": "0x0", "input": "0x"})
		internalTransaction, err = protocol.SyncTransactionToInternal(projected)
		if err != nil {
			return nil, err
		}
	}
	if receipt != nil {
		projected := projectionDefaults(receipt, protocol.Object{"blockNumber": block.Number, "transactionIndex": Hex(cursor.TransactionIndex), "cumulativeGasUsed": "0x0", "effectiveGasPrice": "0x0", "gasUsed": "0x0", "type": "0x0"})
		internalReceipt, err = protocol.SyncTransactionReceiptToInternal(projected)
		if err != nil {
			return nil, err
		}
	}
	return &protocol.RawEvent{ChainID: cursor.ChainID, Checkpoint: checkpoint.MustEncode(cursor), Block: internalBlock, Log: internalLog, Transaction: internalTransaction, TransactionReceipt: internalReceipt}, nil
}

// Decode matched logs and traces together, after checkpoint ordering and before
// business dispatch. Selector deduplication and summaries share one block batch.
func (e *Engine) decodePending(input []pending) ([]pending, error) {
	callbacks := []protocol.Object{}
	raw := []protocol.RawEvent{}
	for i := range input {
		if input[i].callback == nil {
			continue
		}
		if input[i].event.Normalized == nil {
			return nil, fmt.Errorf("missing normalized event %s", input[i].event.Name)
		}
		local := *input[i].event.Normalized
		local.EventCallbackIndex = len(callbacks)
		callbacks = append(callbacks, input[i].callback)
		raw = append(raw, local)
	}
	decoder := protocol.Decoder{Logger: e.options.Logger}
	decoded, err := decoder.DecodeEvents(protocol.Object{"id": e.chain.ID, "name": e.chain.Name}, callbacks, raw)
	if err != nil {
		return nil, err
	}
	public := map[string]protocol.Object{}
	for _, value := range decoded {
		callback := value["eventCallback"].(map[string]any)
		public[fmt.Sprint(callback["name"])+":"+fmt.Sprint(value["checkpoint"])] = value["event"].(map[string]any)
	}
	result := make([]pending, 0, len(input))
	for _, event := range input {
		if event.callback != nil {
			body, ok := public[event.event.Name+":"+checkpoint.MustEncode(event.cursor)]
			if !ok {
				continue
			}
			if event.callback["type"] == "contract" {
				event.event.Args = body["args"]
				event.event.Result = body["result"]
			}
		}
		result = append(result, event)
	}
	return result, nil
}
