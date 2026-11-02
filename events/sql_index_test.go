package events

import (
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"testing"
)

// Real SQL extraction uses int64 for these integer columns. Preserve the
// original index for receipt pairing and complete event checkpoint construction.
func TestBuildEventsSQLTransactionIndices(t *testing.T) {
	callback := Object{"filter": Object{"type": "transaction", "chainId": uint64(1), "fromAddress": filter.Undefined, "toAddress": filter.Undefined, "fromBlock": uint64(1), "toBlock": filter.Undefined}}
	tx := func(index int64) Object {
		return Object{"blockNumber": float64(2), "transactionIndex": index, "from": "0x1111111111111111111111111111111111111111", "to": nil}
	}
	receipt := func(index int64, status string) Object {
		return Object{"blockNumber": float64(2), "transactionIndex": index, "status": status}
	}
	b := Batch{ChainID: 1, EventCallbacks: []Object{callback}, Blocks: []Object{{"number": uint64(2), "timestamp": uint64(1002)}}, Transactions: []Object{tx(4), tx(5)}, TransactionReceipts: []Object{receipt(4, "success"), receipt(5, "reverted")}}
	got, err := BuildEvents(b)
	if err != nil || len(got) != 1 {
		t.Fatalf("SQL receipt pairing: %+v %v", got, err)
	}
	cp, err := checkpoint.Decode(got[0].Checkpoint)
	if err != nil || cp.TransactionIndex != 4 || got[0].TransactionReceipt["transactionIndex"] != int64(4) {
		t.Fatalf("SQL index/checkpoint lost: %+v %v", got, err)
	}
}

func TestBuildEventsSQLLogAndTraceIndices(t *testing.T) {
	for _, kind := range []string{"log", "trace"} {
		t.Run(kind, func(t *testing.T) {
			f := Object{"type": kind, "chainId": uint64(1), "fromBlock": uint64(1), "toBlock": filter.Undefined, "address": filter.Undefined, "fromAddress": filter.Undefined, "toAddress": filter.Undefined, "functionSelector": filter.Undefined, "callType": filter.Undefined}
			b := Batch{ChainID: 1, EventCallbacks: []Object{{"filter": f}}, Blocks: []Object{{"number": uint64(2), "timestamp": uint64(1002)}}, Transactions: []Object{{"blockNumber": float64(2), "transactionIndex": int64(4), "from": "0x1111111111111111111111111111111111111111", "to": nil}}}
			eventIndex := uint64(0)
			if kind == "log" {
				eventIndex = 9
				b.Logs = []Object{{"blockNumber": float64(2), "transactionIndex": int64(4), "logIndex": int64(9), "address": "0x1111111111111111111111111111111111111111", "topics": []any{}, "data": "0x"}}
			} else {
				b.Traces = []Object{{"blockNumber": float64(2), "transactionIndex": int64(4), "traceAddress": []int{}, "from": "0x1111111111111111111111111111111111111111", "to": nil, "type": "CALL", "input": "0x"}}
			}
			got, err := BuildEvents(b)
			if err != nil || len(got) != 1 {
				t.Fatalf("SQL %s construction: %+v %v", kind, got, err)
			}
			cp, err := checkpoint.Decode(got[0].Checkpoint)
			if err != nil || cp.TransactionIndex != 4 || cp.EventIndex != eventIndex {
				t.Fatalf("SQL %s checkpoint lost indices: %+v %v", kind, got, err)
			}
		})
	}
}
