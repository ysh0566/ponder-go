package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/rpc"
)

type accountScanTestNode struct {
	mode         string
	heights      []uint64
	traceHeights []uint64
}

const scanSender = "0x1111111111111111111111111111111111111111"
const scanRecipient = "0x2222222222222222222222222222222222222222"
const scanWatched = "0x3333333333333333333333333333333333333333"

func (s *accountScanTestNode) Call(_ context.Context, method string, params, out any) error {
	hash := params.([]any)[0].(string)
	h, ok := logOnlyQuantity(hash)
	if method == "eth_getTransactionReceipt" && h < 100000 {
		h += 100000
		hash = sparseRangeHead(1, h-100000).Hash
	}
	if !ok || h < 100000 {
		return fmt.Errorf("unexpected account scan hash %s", hash)
	}
	n := h - 100000
	height := fmt.Sprintf("0x%x", n)
	txHash := fmt.Sprintf("0x%064x", n)
	to := scanRecipient
	if s.mode == "transaction" || s.mode == "reverted" {
		to = scanWatched
	}
	switch method {
	case "eth_getBlockByHash":
		s.heights = append(s.heights, n)
		tx := filter.Object{"hash": txHash, "transactionIndex": "0x0", "from": scanSender, "to": to, "blockHash": hash, "blockNumber": height, "gasPrice": "0x0"}
		block := filter.Object{"number": height, "timestamp": fmt.Sprintf("0x%x", 1000+n/2), "hash": hash, "parentHash": sparseRangeHead(1, n).ParentHash, "transactions": []filter.Object{tx}, "logsBloom": "0x"}
		switch s.mode {
		case "wrong_block":
			block["hash"] = txHash
		case "wrong_parent":
			block["parentHash"] = txHash
		case "wrong_timestamp":
			block["timestamp"] = "0x0"
		case "missing_transaction":
			block["transactions"] = []filter.Object{}
		case "wrong_position":
			tx["transactionIndex"] = "0x1"
		case "prefixless_recipient":
			tx["to"] = scanWatched[2:]
		case "missing_recipient":
			delete(tx, "to")
		case "duplicate_transaction":
			other := filter.Object{}
			for key, v := range tx {
				other[key] = v
			}
			other["transactionIndex"] = "0x1"
			block["transactions"] = []filter.Object{tx, other}
		}
		*out.(*map[string]any) = block
	case "eth_getBlockTransactionCountByHash":
		*out.(*string) = "0x1"
	case "eth_getBlockReceipts", "eth_getTransactionReceipt":
		if method == "eth_getBlockReceipts" && s.mode == "receipt_fallback" {
			return errors.New("block receipts unsupported")
		}
		receipt := filter.Object{"transactionHash": txHash, "transactionIndex": "0x0", "blockHash": hash, "blockNumber": height, "from": scanSender, "status": "0x1"}
		switch s.mode {
		case "wrong_receipt":
			receipt["blockHash"] = txHash
		case "bad_status":
			receipt["status"] = "unknown"
		case "missing_receipt":
			*out.(*[]map[string]any) = []filter.Object{}
			return nil
		case "reverted":
			receipt["status"] = "0x0"
		}
		if method == "eth_getTransactionReceipt" {
			*out.(*map[string]any) = receipt
		} else {
			*out.(*[]map[string]any) = []filter.Object{receipt}
		}
	case "debug_traceBlockByHash":
		s.traceHeights = append(s.traceHeights, n)
		frame := filter.Object{"type": "CALL", "from": scanSender, "to": to, "input": "0x", "value": "0x0"}
		entry := filter.Object{"txHash": txHash, "result": frame}
		entries := []filter.Object{entry}
		switch s.mode {
		case "transfer", "zero_transfer":
			value := "0xb"
			if s.mode == "zero_transfer" {
				value = "0x0"
			}
			frame["calls"] = []any{filter.Object{"type": "CALL", "from": scanRecipient, "to": scanWatched, "input": "0x", "value": value}}
		case "false_children":
			frame["calls"] = false
		case "bad_child":
			frame["calls"] = []any{filter.Object{"error": nil}}
		case "missing_trace":
			entries = nil
		case "duplicate_trace":
			entries = append(entries, entry)
		case "wrong_trace_recipient":
			frame["to"] = scanWatched
		case "wrong_trace_value":
			frame["value"] = "0x1"
		case "wrong_trace":
			entry["txHash"] = hash
		case "missing_value":
			delete(frame, "value")
		case "reverted", "false_revert":
			frame["error"] = "execution reverted"
		case "rpc_error":
			return errors.New("trace archive unavailable")
		}
		*out.(*[]map[string]any) = entries
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
	return nil
}

func TestOmnichainUncachedAccountEvidence(t *testing.T) {
	filters := map[uint64][]filter.Object{1: {
		{"chainId": 1, "type": "transaction", "fromBlock": 2, "toBlock": 3, "toAddress": scanWatched, "hasTransactionReceipt": true},
		{"chainId": 1, "type": "transfer", "fromBlock": 3, "toBlock": 3, "toAddress": scanWatched},
		{"chainId": 1, "type": "transfer", "fromBlock": 7, "toBlock": 7, "toAddress": scanWatched},
	}}
	callbacks, err := logOnlyABICallbacks(filters, map[uint64][]any{1: {nil, nil, nil}})
	if err != nil {
		t.Fatal(err)
	}
	opts := ManagedLiveOptions{runtimeLogFilters: filters, runtimeLogCallbacks: callbacks}
	for _, name := range strings.Fields("unrelated processed reverted receipt_fallback zero_transfer transaction transfer no_abis wrong_block wrong_parent wrong_timestamp missing_transaction wrong_position prefixless_recipient missing_recipient duplicate_transaction wrong_receipt bad_status missing_receipt missing_trace duplicate_trace wrong_trace wrong_trace_value wrong_trace_recipient missing_value bad_child false_children false_revert rpc_error") {
		t.Run(name, func(t *testing.T) {
			node := &accountScanTestNode{mode: name}
			r := logScanRange{chain: 1, from: 1, to: 8, checkEmpty: true, emptyFrom: 2}
			if name == "processed" {
				node.mode = "transfer"
				r.checkEmpty = false
			}
			reader := LogScanRPC{Actions: rpc.NewActions(node)}
			options := opts
			if name == "no_abis" {
				options.runtimeLogCallbacks = nil
				node.mode = "transaction"
			}
			scan, err := reader.scan(t.Context(), options, r, func(id, n uint64) (ManagedLiveHead, error) { return sparseRangeHead(id, n), nil })
			switch name {
			case "unrelated", "processed", "reverted", "receipt_fallback", "zero_transfer":
				if err != nil || scan.FiltersHash != managedHash(filters[1]) || !slices.Equal(node.heights, []uint64{2, 3, 7}) || !slices.Equal(node.traceHeights, []uint64{3, 7}) {
					t.Fatalf("scan %+v heights %v traces %v err %v", scan, node.heights, node.traceHeights, err)
				}
				if err := validateLogCoverage(filters[1], scan.Coverage, 1, 8); err != nil {
					t.Fatal(err)
				}
			case "transaction", "transfer", "no_abis":
				if !errors.Is(err, ErrManagedLivePendingActions) {
					t.Fatalf("eligible callback was not reported: %v", err)
				}
			default:
				if err == nil || errors.Is(err, ErrManagedLivePendingActions) {
					t.Fatalf("expected invalid or unavailable RPC evidence, got %v", err)
				}
			}
			if err != nil && (scan.FiltersHash != "" || len(scan.Coverage) != 0) {
				t.Fatalf("partial scan after error: %+v", scan)
			}
		})
	}
}

type accountAndLogScanNode struct {
	*accountScanTestNode
	logs logScanTestRPC
}

func (s accountAndLogScanNode) Call(ctx context.Context, method string, params, out any) error {
	if method == "eth_getLogs" {
		return s.logs.Call(ctx, method, params, out)
	}
	return s.accountScanTestNode.Call(ctx, method, params, out)
}

func TestOmnichainUncachedAccountFactoryAndLog(t *testing.T) {
	event := abi.Event{Type: "event", Name: "Value", Inputs: []abi.Parameter{{Name: "amount", Type: "uint256"}}}
	birthTopic := "0x" + strings.Repeat("aa", 32)
	factory := filter.Object{"id": "children", "type": "log", "chainId": 1, "address": scanSender, "eventSelector": birthTopic, "childAddressLocation": "topic1", "fromBlock": 1, "toBlock": 3}
	filters := map[uint64][]filter.Object{1: {
		{"type": "transaction", "chainId": 1, "fromBlock": 2, "toBlock": 2, "toAddress": factory, "hasTransactionReceipt": true},
		{"type": "log", "chainId": 1, "fromBlock": 2, "toBlock": 2, "topic0": abi.EventSelector(event), "address": scanRecipient},
	}}
	callbacks, err := logOnlyABICallbacks(filters, map[uint64][]any{1: {nil, event}})
	if err != nil {
		t.Fatal(err)
	}
	opts := ManagedLiveOptions{runtimeLogFilters: filters, runtimeLogCallbacks: callbacks}
	for _, name := range []string{"not_born", "born_at_event", "already_born", "abi_matches"} {
		t.Run(name, func(t *testing.T) {
			node := accountAndLogScanNode{accountScanTestNode: &accountScanTestNode{mode: "transaction"}, logs: func(q map[string]any) ([]filter.Object, error) {
				num, topics, address, data := uint64(2), []any{abi.EventSelector(event)}, scanRecipient, "0x00"
				if q["address"] == scanSender {
					switch name {
					case "already_born":
						num = 1
					case "born_at_event":
						num = 2
					default:
						return []filter.Object{}, nil
					}
					address, data, topics = scanSender, "0x", []any{birthTopic, "0x" + strings.Repeat("0", 24) + scanWatched[2:]}
				} else if name == "abi_matches" {
					data = fmt.Sprintf("0x%064x", 3)
				}
				return []filter.Object{{"blockNumber": fmt.Sprintf("0x%x", num), "blockHash": sparseRangeHead(1, num).Hash, "logIndex": "0x0", "address": address, "topics": topics, "data": data}}, nil
			}}
			reader := LogScanRPC{Actions: rpc.NewActions(node)}
			scan, err := reader.scan(t.Context(), opts, logScanRange{chain: 1, from: 2, to: 2, checkEmpty: true, emptyFrom: 2}, func(id, n uint64) (ManagedLiveHead, error) { return sparseRangeHead(id, n), nil })
			if name == "not_born" {
				if err != nil || scan.FiltersHash != managedHash(callbacks[1]) {
					t.Fatalf("scan %+v err %v", scan, err)
				}
			} else if !errors.Is(err, ErrManagedLivePendingActions) {
				t.Fatalf("expected pending callback, got %v", err)
			}
		})
	}
}
