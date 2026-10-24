package runtime

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	protocol "ponder.local/ponder-go/events"
)

func TestContractLogProjection(t *testing.T) {
	cursor := checkpoint.Checkpoint{BlockTimestamp: 2, ChainID: 1, BlockNumber: 1, EventType: checkpoint.Logs, EventIndex: 1}
	log := Log{Address: "0x0000000000000000000000000000000000000001", BlockHash: "block1", TransactionHash: "tx1", TransactionIndex: "0x0", LogIndex: "0x1", Topics: []string{}, Data: "0x"}
	t.Run("legacy_partial_block_defaults_without_mutating_handler_input", func(t *testing.T) {
		transaction := map[string]any{"hash": "tx1"}
		block := Block{Number: "0x1", Hash: "block1", ParentHash: "block0", Timestamp: "0x2", Transactions: []map[string]any{transaction}}
		before, err := json.Marshal(block)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := normalizedContractLog(block, log, transaction, nil, cursor)
		if err != nil {
			t.Fatal("legacy supported input must reach ABI decoding and its handler", err)
		}
		for _, field := range []string{"difficulty", "gasLimit", "gasUsed", "size", "baseFeePerGas", "totalDifficulty"} {
			value, ok := raw.Block[field].(*big.Int)
			if !ok || value.Sign() != 0 {
				t.Fatalf("missing optional block quantity %s: %v", field, raw.Block[field])
			}
		}
		for _, field := range []string{"gas", "value", "gasPrice"} {
			value, ok := raw.Transaction[field].(*big.Int)
			if !ok || value.Sign() != 0 {
				t.Fatalf("missing native transaction quantity %s: %v", field, raw.Transaction[field])
			}
		}
		if fmt.Sprint(raw.Transaction["blockNumber"]) != "1" || fmt.Sprint(raw.Transaction["transactionIndex"]) != "0" || fmt.Sprint(raw.Log["blockNumber"]) != "1" {
			t.Fatal("known block/log provenance", raw)
		}
		after, err := json.Marshal(block)
		if err != nil || string(before) != string(after) || !reflect.DeepEqual(transaction, map[string]any{"hash": "tx1"}) || log.BlockNumber != "" {
			t.Fatal("raw handler input was mutated", string(before), string(after), transaction, log, err)
		}
	})
	for _, format := range []string{"hex", "json_integer"} {
		t.Run("preserves_large_"+format+"_quantities", func(t *testing.T) {
			quantity := `"0x20000000000001"`
			if format == "json_integer" {
				quantity = `9007199254740993`
			}
			var block Block
			data := `{"number":"0x1","hash":"block1","parentHash":"block0","timestamp":"0x2","difficulty":` + quantity + `,"gasLimit":"0x0","gasUsed":"0x0","size":"0x0","transactions":[]}`
			if err := json.Unmarshal([]byte(data), &block); err != nil {
				t.Fatal(err)
			}
			raw, err := normalizedContractLog(block, log, nil, nil, cursor)
			if err != nil {
				t.Fatal(err)
			}
			value, ok := raw.Block["difficulty"].(*big.Int)
			if !ok || value.String() != "9007199254740993" {
				t.Fatal("JSON projection rounded a supplied integer", raw.Block["difficulty"])
			}
		})
	}
	for _, value := range []string{`{}`, `null`} {
		t.Run("rejects_provided_invalid_quantity_"+value, func(t *testing.T) {
			var block Block
			data := `{"number":"0x1","hash":"block1","parentHash":"block0","timestamp":"0x2","difficulty":` + value + `,"gasLimit":"0x0","gasUsed":"0x0","size":"0x0","transactions":[]}`
			if err := json.Unmarshal([]byte(data), &block); err != nil {
				t.Fatal(err)
			}
			if _, err := normalizedContractLog(block, log, nil, nil, cursor); err == nil || !strings.Contains(err.Error(), "difficulty") {
				t.Fatal("provided invalid data must not be replaced by a default", err)
			}
		})
	}
}

func TestDecodePendingPreservesNormalizedCallbackIndex(t *testing.T) {
	cursor := checkpoint.Checkpoint{BlockTimestamp: 2, ChainID: 1, BlockNumber: 1, EventType: checkpoint.Transactions}
	raw := &protocol.RawEvent{ChainID: 1, EventCallbackIndex: 7, Checkpoint: checkpoint.MustEncode(cursor), Block: protocol.Object{"number": big.NewInt(1)}, Transaction: protocol.Object{"hash": "tx1"}}
	engine := &Engine{chain: config.Chain{Name: "local", ID: 1}}
	callback := protocol.Object{"name": "A:transaction:to", "type": "account", "filter": protocol.Object{"type": "transaction"}}
	input := []pending{{cursor: cursor, event: Event{Name: "A:transaction:to", Normalized: raw}, callback: callback}}
	output, err := engine.decodePending(input)
	if err != nil || len(output) != 1 || output[0].event.Normalized != raw || raw.EventCallbackIndex != 7 {
		t.Fatal("decoder-local callback lookup must preserve the existing handler's raw event", output, raw, err)
	}
}
