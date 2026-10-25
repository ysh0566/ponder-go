package runtime

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
	"sync"
	"testing"
	"time"
)

// Supplementary RPC-identity ownership controls, not original source records.
func TestSourceHistoricalProviderOwnership(t *testing.T) {
	t.Run("log_pages", func(t *testing.T) {
		ctx := context.Background()
		var mu sync.Mutex
		queries := []map[string]any{}
		raw := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
			if method != "eth_getLogs" {
				return fmt.Errorf("unexpected %s", method)
			}
			mu.Lock()
			queries = append(queries, params.([]any)[0].(map[string]any))
			mu.Unlock()
			return sourceHistoricalTestResult([]any{}, out)
		})
		provider, err := rpc.NewSourceHistoricalProvider(raw, rpc.SourceHistoricalOverrides{})
		if err != nil {
			t.Fatal(err)
		}
		for _, end := range []float64{501, 510} {
			syncer, err := NewSourceInMemoryHistoricalSync(provider, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, err := range syncer.SyncBlockData(ctx, []store.SourceFilterInterval{{Filter: filter.Object{"type": "log", "address": "0xaaa", "topic0": "0xtopic", "topic1": nil, "topic2": nil, "topic3": nil}, Interval: store.SourceInterval{1, end}}}, nil) {
				if err != nil {
					t.Fatal(err)
				}
				t.Fatal("empty logs should not yield blocks")
			}
		}
		if len(queries) != 3 {
			t.Fatal("same typed RPC lost source pagination learning across constructors", queries)
		}
		if queries[2]["fromBlock"] != "0x1" || queries[2]["toBlock"] != "0x1fe" {
			t.Fatal("wrong retained range", queries)
		}
	})
	t.Run("receipt_fallback", func(t *testing.T) {
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
		var receipt filter.Object
		deadline := time.Now().Add(5 * time.Second)
		for receipt == nil {
			if err := client.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatal("fixture did not mine")
			}
		}
		number, err := sourceHistoricalNumber(receipt["blockNumber"])
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		methods := map[string]int{}
		raw := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
			mu.Lock()
			methods[method]++
			mu.Unlock()
			if method == "eth_getBlockReceipts" {
				return fmt.Errorf("explicit unsupported provider input")
			}
			return client.Call(ctx, method, params, out)
		})
		provider, err := rpc.NewSourceHistoricalProvider(raw, rpc.SourceHistoricalOverrides{})
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			syncer, err := NewSourceInMemoryHistoricalSync(provider, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			pages := 0
			for data, err := range syncer.SyncBlockData(ctx, []store.SourceFilterInterval{{Filter: filter.Object{"type": "log", "address": emitter, "topic0": topic, "topic1": nil, "topic2": nil, "topic3": nil, "hasTransactionReceipt": true}, Interval: store.SourceInterval{float64(number), float64(number)}}}, nil) {
				if err != nil {
					t.Fatal(err)
				}
				pages++
				if len(data.Logs) != 1 || len(data.TransactionReceipts) != 1 {
					t.Fatal("actual fallback receipt missing", data)
				}
			}
			if pages != 1 {
				t.Fatal("expected one actual block", pages)
			}
		}
		if methods["eth_getBlockReceipts"] != 1 || methods["eth_getTransactionReceipt"] != 2 {
			t.Fatal("same typed RPC lost source receipt fallback across constructors", methods)
		}
	})
}
