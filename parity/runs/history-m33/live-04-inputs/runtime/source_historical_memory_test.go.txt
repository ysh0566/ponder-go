package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

type sourceHistoricalTestRequester func(context.Context, string, any, any) error

func (f sourceHistoricalTestRequester) Call(ctx context.Context, method string, params, out any) error {
	return f(ctx, method, params, out)
}
func sourceHistoricalTestResult(value, out any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Supplementary observable acquisition controls, separate from original cases.
func TestSourceHistoricalMemoryBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan uint64, 200)
	release := make(chan struct{})
	consumer := make(chan struct{})
	first := make(chan store.SourceEventData, 1)
	finished := make(chan error, 1)
	rpc := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
		if method != "eth_getBlockByNumber" {
			return fmt.Errorf("unexpected request %s", method)
		}
		number, err := sourceHistoricalNumber(params.([]any)[0])
		if err != nil {
			return err
		}
		requests <- number
		if number == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return sourceHistoricalTestResult(filter.Object{"hash": fmt.Sprintf("0x%064x", number), "parentHash": fmt.Sprintf("0x%064x", number-1), "number": Hex(number), "timestamp": Hex(number), "logsBloom": "0x", "transactions": []any{}}, out)
	})
	s, err := NewSourceInMemoryHistoricalSync(rpc, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		expected := 1.
		for data, err := range s.SyncBlockData(ctx, []store.SourceFilterInterval{{Filter: filter.Object{"type": "block", "interval": float64(1), "offset": float64(0)}, Interval: store.SourceInterval{1, 150}}}, nil) {
			if err != nil {
				finished <- err
				return
			}
			if data.Cursor != expected {
				finished <- fmt.Errorf("unordered block %v, expected %v", data.Cursor, expected)
				return
			}
			if expected == 1 {
				first <- data
				select {
				case <-consumer:
				case <-ctx.Done():
					finished <- ctx.Err()
					return
				}
			}
			expected++
		}
		if expected != 151 {
			finished <- fmt.Errorf("only %v blocks", expected-1)
			return
		}
		finished <- nil
	}()
	seen := map[uint64]bool{}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for len(seen) < 100 {
		select {
		case number := <-requests:
			if seen[number] {
				t.Fatal("duplicate block request", number)
			}
			seen[number] = true
		case <-deadline.C:
			t.Fatal("100 block requests did not start")
		}
	}
	for number := uint64(1); number <= 100; number++ {
		if !seen[number] {
			t.Fatal("wrong prefetched block set", seen)
		}
	}
	select {
	case data := <-first:
		t.Fatal("yield overtook blocked first block", data)
	case number := <-requests:
		t.Fatal("prefetch exceeded100 before first yield", number)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case data := <-first:
		if data.Cursor != 1 {
			t.Fatal(data)
		}
	case <-deadline.C:
		t.Fatal("first block did not yield")
	}
	select {
	case number := <-requests:
		t.Fatal("prefetch grew while consumer paused", number)
	case <-time.After(50 * time.Millisecond):
	}
	close(consumer)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-deadline.C:
		t.Fatal("bounded stream did not finish")
	}
}

func TestSourceHistoricalFactoryDrain(t *testing.T) {
	for _, mode := range []string{"valid", "undefined_parent_bad_topic", "defined_parent_bad_topic"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			parent := "0x" + strings.Repeat("1", 40)
			child := "0x" + strings.Repeat("2", 40)
			selector := "0x" + strings.Repeat("a", 64)
			factory := filter.Object{"id": "factory", "type": "log", "sourceId": "factory-source", "eventSelector": selector, "address": parent, "childAddressLocation": "topic1"}
			compiled, err := config.CompileSourceFilter(filter.Object{"type": "log", "address": factory, "topic0": selector})
			if err != nil {
				t.Fatal(err)
			}
			callback := config.SourceEventCallback{Name: "Factory:Seen", Type: "contract", Filter: compiled}
			before := callback.Filter.Value()
			address, err := callback.Filter.(config.SourceLogFilter).Address()
			if err != nil {
				t.Fatal(err)
			}
			typedFactory := address.(config.SourceFactoryAddress)
			if mode != "valid" {
				if err = typedFactory.SetChildAddressLocation("topic3"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "undefined_parent_bad_topic" {
				if err = typedFactory.SetParentAddress(config.SourceWildcardAddress{}); err != nil {
					t.Fatal(err)
				}
			}
			factory = callback.Filter.Value()["address"].(filter.Object)
			if typedFactory.ID() != "factory" || before["address"].(filter.Object)["childAddressLocation"] != "topic1" || before["address"].(filter.Object)["address"] != parent {
				t.Fatal("typed setter changed factory ID or a detached snapshot")
			}
			var mu sync.Mutex
			queries := []filter.Object{}
			rpc := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
				if method != "eth_getLogs" {
					return fmt.Errorf("unexpected %s", method)
				}
				query := params.([]any)[0].(map[string]any)
				mu.Lock()
				queries = append(queries, query)
				mu.Unlock()
				logs := []filter.Object{}
				if query["fromBlock"] == "0x2" {
					logs = append(logs, filter.Object{"blockNumber": "0x2", "logIndex": "0x0", "blockHash": "0x" + strings.Repeat("b", 64), "address": parent, "data": "0x", "topics": []any{selector, "0x" + strings.Repeat("0", 24) + child[2:]}})
				}
				return sourceHistoricalTestResult(logs, out)
			})
			children := NewSourceHistoricalChildren([]SourceFactoryChildren{{ID: "factory", Entries: []filter.ChildAddress{}}})
			one := uint64(1)
			s, err := NewSourceInMemoryHistoricalSync(rpc, nil, children, SourceHistoricalOptions{EthGetLogsBlockRange: &one})
			if err != nil {
				t.Fatal(err)
			}
			var failure error
			for _, err := range s.SyncBlockData(ctx, nil, []store.SourceFactoryInterval{{Factory: factory, Interval: store.SourceInterval{1, 2}}, {Factory: factory, Interval: store.SourceInterval{2, 3}}}) {
				if err != nil {
					failure = err
					break
				}
				t.Fatal("factory-only acquisition yielded a business block")
			}
			entries, _ := children.Entries("factory")
			if mode == "defined_parent_bad_topic" {
				if failure == nil || !strings.Contains(failure.Error(), "undefined") {
					t.Fatal("explicit parent must propagate extraction failure", failure)
				}
				return
			}
			if failure != nil {
				t.Fatal(failure)
			}
			if len(queries) != 3 {
				t.Fatal("factory pages were duplicated or not fully drained", queries)
			}
			for i, query := range queries {
				if query["fromBlock"] != Hex(uint64(i+1)) || query["toBlock"] != Hex(uint64(i+1)) {
					t.Fatal("merged factory bounds", queries)
				}
			}
			if mode == "valid" {
				if !reflect.DeepEqual(entries, []filter.ChildAddress{{Address: child, Block: 2}}) {
					t.Fatal("factory child result", entries)
				}
			} else if len(entries) != 0 {
				t.Fatal("invalid child accepted", entries)
			}
		})
	}
}

func TestSourceHistoricalMemoryLive(t *testing.T) {
	ctx := context.Background()
	client, _ := localChain(t)
	emitter := "0x1000000000000000000000000000000000000001"
	item := abi.Event{Type: "event", Name: "Seen", Inputs: []abi.Parameter{{Name: "amount", Type: "uint256"}}}
	topic := abi.EventSelector(item)
	var ignored any
	if err := client.Call(ctx, "anvil_setCode", []any{emitter, "0x6000356000527f" + topic[2:] + "60206000a100"}, &ignored); err != nil {
		t.Fatal(err)
	}
	var accounts []string
	if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": emitter, "gas": "0x186a0", "data": fmt.Sprintf("0x%064x", 42)}}, &hash); err != nil {
		t.Fatal(err)
	}
	var receipt map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for receipt == nil {
		if err := client.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("live fixture did not mine")
		}
	}
	number, err := sourceHistoricalNumber(receipt["blockNumber"])
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"log_receipt_trace", "zero_hash_log"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			methods := map[string]int{}
			requester := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
				mu.Lock()
				methods[method]++
				mu.Unlock()
				var raw any
				if err := client.Call(ctx, method, params, &raw); err != nil {
					return err
				}
				return sourceHistoricalTestResult(raw, out)
			})
			base, err := rpc.NewSourceHistoricalProvider(requester, rpc.SourceHistoricalOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			var consumerRPC rpc.Requester = base
			if mode == "zero_hash_log" {
				typed, err := rpc.NewSourceHistoricalProvider(base, rpc.SourceHistoricalOverrides{Logs: func(ctx context.Context, query map[string]any) ([]rpc.SourceLog, error) {
					logs, err := base.Logs(ctx, query)
					if err != nil {
						return nil, err
					}
					for _, log := range logs {
						log["transactionHash"] = "0x" + strings.Repeat("0", 64)
					}
					return logs, nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				consumerRPC = typed
			}
			s, err := NewSourceInMemoryHistoricalSync(consumerRPC, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			filters := []store.SourceFilterInterval{{Filter: filter.Object{"type": "log", "address": emitter, "topic0": topic, "hasTransactionReceipt": true}, Interval: store.SourceInterval{float64(number), float64(number)}}}
			if mode == "log_receipt_trace" {
				filters = append(filters, store.SourceFilterInterval{Filter: filter.Object{"type": "trace", "toAddress": emitter, "hasTransactionReceipt": true}, Interval: store.SourceInterval{float64(number), float64(number)}})
			}
			pages := []store.SourceEventData{}
			for data, err := range s.SyncBlockData(ctx, filters, nil) {
				if err != nil {
					t.Fatal(err)
				}
				pages = append(pages, data)
			}
			if len(pages) != 1 || pages[0].Cursor != float64(number) || len(pages[0].Blocks) != 1 || len(pages[0].Logs) != 1 {
				t.Fatal("live acquired page", pages)
			}
			data := pages[0]
			if mode == "zero_hash_log" {
				if len(data.Transactions) != 0 || len(data.TransactionReceipts) != 0 || methods["eth_getBlockReceipts"]+methods["eth_getTransactionReceipt"] != 0 {
					t.Fatal("zero-hash receipts/transactions", data, methods)
				}
			} else {
				if len(data.Transactions) != 1 || len(data.TransactionReceipts) != 1 || len(data.Traces) != 1 || data.TransactionReceipts[0]["status"] != "success" || data.Transactions[0]["hash"] != hash {
					t.Fatal("actual trace/receipt provenance", data, methods)
				}
			}
		})
	}
}
