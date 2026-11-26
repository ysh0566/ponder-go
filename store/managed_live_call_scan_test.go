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

// Two callbacks can match the same selector but disagree about the return ABI.
func TestOmnichainContractCallScanEvidence(t *testing.T) {
	word := abi.Parameter{Name: "n", Type: "uint256"}
	fn := abi.Function{Name: "record", Type: "function", Inputs: []abi.Parameter{word}, Outputs: []abi.Parameter{word}}
	pair := fn
	pair.Outputs = []abi.Parameter{word, word}
	sel := abi.FunctionSelector(fn)
	f := filter.Object{"type": "trace", "callType": "CALL", "functionSelector": sel, "chainId": 1, "fromBlock": 1, "toBlock": 3, "toAddress": scanRecipient}
	input := sel + fmt.Sprintf("%064x", 7)
	output := fmt.Sprintf("0x%064x", 8)
	for _, tt := range []struct {
		name, address, input, output string
		pending                      bool
	}{
		{"one_callback_decodes", scanRecipient, input, output, true},
		{"bad_arguments", scanRecipient, sel, "0x", false},
		{"unrelated_address", scanWatched, input, output, false},
		{"different_selector", scanRecipient, "0x01020304", output, false},
		{"short_return", scanRecipient, input, "0x02", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			filters := map[uint64][]filter.Object{1: {f, f}}
			callbacks, err := logOnlyABICallbacks(filters, map[uint64][]any{1: {pair, fn}})
			if err != nil {
				t.Fatal(err)
			}
			opts := ManagedLiveOptions{runtimeLogFilters: filters, runtimeLogCallbacks: callbacks}
			rows := SourceEventData{Cursor: 3, Blocks: []filter.Object{{"number": 2, "timestamp": 1}}, Transactions: []filter.Object{{"blockNumber": 2, "transactionIndex": 0}}, Traces: []filter.Object{{"blockNumber": 2, "transactionIndex": 0, "from": scanSender, "to": tt.address, "input": tt.input, "output": tt.output, "type": "CALL"}}}
			digest, err := opts.logOnlyScanHash(t.Context(), SourceEventDataArgs{ChainID: 1, Filters: filters[1], FromBlock: 1, ToBlock: 3}, nil, func(context.Context, SourceEventDataArgs) (SourceEventData, error) { return rows, nil })
			if tt.pending {
				if !errors.Is(err, ErrManagedLivePendingActions) || digest != "" {
					t.Fatalf("decoded CALL was lost: %q %v", digest, err)
				}
			} else if err != nil || digest != managedHash(callbacks[1]) || opts.validLogScanHash(1, managedHash(filters[1])) {
				t.Fatalf("empty scan digest %q error %v", digest, err)
			}
		})
	}
}

func TestOmnichainContractCallScanRPCData(t *testing.T) {
	for _, tt := range []struct {
		name, input, output string
		path                []int
		valid               bool
	}{
		{"root", "0x01020304", "0x", nil, true},
		{"nested", "0xaabbccdd", "0x", []int{0}, true},
		{"different_root", "0xaabbccdd", "0x", nil, false},
		{"invalid_input", "0xabc", "0x", []int{0}, false},
		{"invalid_output", "0xaabbccdd", "0xa", []int{0}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hash := "0x" + strings.Repeat("11", 32)
			data := rpc.AccountScanBlock{Block: filter.Object{"transactions": []filter.Object{{"hash": hash, "input": "0x01020304"}}}, Traces: []rpc.CallTrace{{TransactionHash: hash, Trace: filter.Object{"type": "CALL", "input": tt.input, "output": tt.output, "traceAddress": tt.path}}}}
			if err := validateCallScanData(data); (err == nil) != tt.valid {
				t.Fatalf("validation error %v", err)
			}
		})
	}
}

type contractCallScanNode struct {
	accountScanTestNode
	input, output string
}

func (s *contractCallScanNode) Call(ctx context.Context, method string, params, out any) error {
	if err := s.accountScanTestNode.Call(ctx, method, params, out); err != nil {
		return err
	}
	switch method {
	case "eth_getBlockByHash":
		(*out.(*filter.Object))["transactions"].([]filter.Object)[0]["input"] = "0x"
	case "debug_traceBlockByHash":
		root := (*out.(*[]filter.Object))[0]["result"].(filter.Object)
		root["calls"] = []any{filter.Object{"type": "CALL", "from": scanRecipient, "to": scanWatched, "value": "0x0", "input": s.input, "output": s.output}}
	}
	return nil
}

func TestOmnichainUncachedContractCallEvidence(t *testing.T) {
	word := abi.Parameter{Type: "uint256"}
	fn := abi.Function{Type: "function", Name: "record", Inputs: []abi.Parameter{word}, Outputs: []abi.Parameter{word}}
	sel := abi.FunctionSelector(fn)
	var group []filter.Object
	for _, height := range []int{2, 7} {
		group = append(group, filter.Object{"type": "trace", "chainId": 1, "callType": "CALL", "toAddress": scanWatched, "functionSelector": sel, "fromBlock": height, "toBlock": height})
	}
	filters := map[uint64][]filter.Object{1: group}
	callbacks, err := logOnlyABICallbacks(filters, map[uint64][]any{1: {fn, fn}})
	if err != nil {
		t.Fatal(err)
	}
	opts := ManagedLiveOptions{runtimeLogFilters: filters, runtimeLogCallbacks: callbacks}
	for _, tt := range []struct {
		name, output string
		pending      bool
	}{
		{"decodable_nested_call", fmt.Sprintf("0x%064x", 11), true},
		{"skipped_short_result", "0x1234", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			node := &contractCallScanNode{input: sel + fmt.Sprintf("%064x", 8), output: tt.output}
			reader := LogScanRPC{Actions: rpc.NewActions(node)}
			scan, err := reader.scan(t.Context(), opts, logScanRange{chain: 1, from: 1, to: 8, checkEmpty: true, emptyFrom: 2}, func(id, height uint64) (ManagedLiveHead, error) { return sparseRangeHead(id, height), nil })
			if tt.pending {
				if !errors.Is(err, ErrManagedLivePendingActions) || scan.FiltersHash != "" {
					t.Fatalf("unrecorded call accepted: %+v %v", scan, err)
				}
			} else if err != nil || scan.FiltersHash != managedHash(callbacks[1]) || !slices.Equal(node.traceHeights, []uint64{2, 7}) {
				t.Fatalf("scan %+v traces at %v error %v", scan, node.traceHeights, err)
			}
		})
	}
}
