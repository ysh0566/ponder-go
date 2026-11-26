package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/filter"
)

func TestOmnichainAccountScanEvidence(t *testing.T) {
	const sender = "0x1111111111111111111111111111111111111111"
	const recipient = "0x2222222222222222222222222222222222222222"
	for _, name := range []string{"unrelated", "pending_transaction", "reverted", "missing_receipt", "missing_header", "pending_transfer", "zero_value", "delegatecall"} {
		t.Run(name, func(t *testing.T) {
			f := filter.Object{"type": "transaction", "chainId": 1, "fromBlock": 1, "toBlock": 2, "fromAddress": sender, "hasTransactionReceipt": true}
			tx := filter.Object{"blockNumber": 1, "transactionIndex": 0, "from": sender, "to": recipient}
			receipt := filter.Object{"blockNumber": 1, "transactionIndex": 0, "status": "success"}
			rows := SourceEventData{Blocks: []filter.Object{{"number": 1, "timestamp": 8}}, Transactions: []filter.Object{tx}, TransactionReceipts: []filter.Object{receipt}, Cursor: 2}
			var pending, invalid bool
			switch name {
			case "unrelated":
				tx["from"] = recipient
			case "pending_transaction":
				pending = true
			case "reverted":
				receipt["status"] = "reverted"
			case "missing_receipt":
				rows.TransactionReceipts = nil
				invalid = true
			case "missing_header":
				rows.Blocks[0]["number"] = 2
				invalid = true
			default:
				f["type"] = "transfer"
				tr := filter.Object{"blockNumber": 1, "transactionIndex": 0, "from": sender, "to": recipient, "type": "CALL", "value": "3"}
				rows.Traces = []filter.Object{tr}
				switch name {
				case "pending_transfer":
					pending = true
				case "zero_value":
					tr["value"] = "0"
				case "delegatecall":
					tr["type"] = "DELEGATECALL"
				}
			}
			args := SourceEventDataArgs{ChainID: 1, Filters: []filter.Object{f}, FromBlock: 1, ToBlock: 2, Limit: 1}
			var opts ManagedLiveOptions
			digest, err := opts.logOnlyScanHash(t.Context(), args, nil, func(context.Context, SourceEventDataArgs) (SourceEventData, error) { return rows, nil })
			if errors.Is(err, ErrManagedLivePendingActions) != pending || (err != nil) != (pending || invalid) || (err == nil && digest != managedHash(args.Filters)) {
				t.Fatalf("digest %q, err %v", digest, err)
			}
		})
	}
}

func TestOmnichainAccountScanWithABILog(t *testing.T) {
	const recipient = "0x2222222222222222222222222222222222222222"
	event := abi.Event{Type: "event", Name: "Value", Inputs: []abi.Parameter{{Name: "n", Type: "uint256"}}}
	selector := abi.EventSelector(event)
	filters := map[uint64][]filter.Object{1: {
		{"type": "log", "chainId": 1, "fromBlock": 1, "toBlock": 2, "topic0": selector},
		{"type": "transaction", "chainId": 1, "fromBlock": 1, "toBlock": 2, "toAddress": recipient, "hasTransactionReceipt": true},
	}}
	callbacks, err := logOnlyABICallbacks(filters, map[uint64][]any{1: {event, nil}})
	if err != nil {
		t.Fatal(err)
	}
	opts := ManagedLiveOptions{runtimeLogFilters: filters, runtimeLogCallbacks: callbacks}
	for _, name := range []string{"skipped_log_and_revert", "active_log", "active_account"} {
		t.Run(name, func(t *testing.T) {
			data := "0x01"
			status := "reverted"
			if name == "active_log" {
				data = fmt.Sprintf("0x%064x", 1)
			}
			if name == "active_account" {
				status = "success"
			}
			rows := SourceEventData{Cursor: 2, Blocks: []filter.Object{{"number": 2, "timestamp": 11}},
				Transactions:        []filter.Object{{"blockNumber": 2, "transactionIndex": 0, "to": recipient}},
				TransactionReceipts: []filter.Object{{"blockNumber": 2, "transactionIndex": 0, "status": status}},
				Logs:                []filter.Object{{"blockNumber": 2, "transactionIndex": 0, "logIndex": 0, "topics": []any{selector}, "data": data}},
			}
			digest, err := opts.logOnlyScanHash(t.Context(), SourceEventDataArgs{ChainID: 1, Filters: filters[1], FromBlock: 1, ToBlock: 2, Limit: 1}, nil, func(context.Context, SourceEventDataArgs) (SourceEventData, error) { return rows, nil })
			if name == "skipped_log_and_revert" {
				if err != nil || digest != managedHash(callbacks[1]) {
					t.Fatal(digest, err)
				}
			} else if !errors.Is(err, ErrManagedLivePendingActions) || digest != "" {
				t.Fatal(digest, err)
			}
		})
	}
}
