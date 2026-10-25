package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

func TestSourceHistoricalPersistentLive(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			client, _ := localChain(t)
			backend := stagedBackend(t, ctx, dialect, "memory://")
			defer backend.Close()
			db, err := store.New(ctx, backend, schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}, fmt.Sprintf("ponder_go_historical_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			cache, err := db.EnableProtocolCache(ctx)
			if err != nil {
				t.Fatal(err)
			}
			source, err := cache.SourceSyncStore(0)
			if err != nil {
				t.Fatal(err)
			}
			emitter := "0x1000000000000000000000000000000000000001"
			item := abi.Event{Type: "event", Name: "Seen", Inputs: []abi.Parameter{{Name: "amount", Type: "uint256"}}}
			topic := abi.EventSelector(item)
			var ignored any
			if err = client.Call(ctx, "anvil_setCode", []any{emitter, "0x6000356000527f" + topic[2:] + "60206000a100"}, &ignored); err != nil {
				t.Fatal(err)
			}
			var accounts []string
			if err = client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
				t.Fatal(err)
			}
			var hash string
			if err = client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": emitter, "gas": "0x186a0", "data": fmt.Sprintf("0x%064x", 42)}}, &hash); err != nil {
				t.Fatal(err)
			}
			var receipt filter.Object
			deadline := time.Now().Add(5 * time.Second)
			for receipt == nil {
				if err = client.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
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
			span := store.SourceInterval{float64(number), float64(number)}
			logFilter := filter.Object{"type": "log", "chainId": float64(1), "sourceId": "C", "address": emitter, "topic0": topic, "fromBlock": float64(number), "hasTransactionReceipt": true}
			traceFilter := filter.Object{"type": "trace", "chainId": float64(1), "sourceId": "C", "toAddress": emitter, "fromBlock": float64(number), "hasTransactionReceipt": true}
			required := []store.SourceFilterInterval{{Filter: logFilter, Interval: span}, {Filter: traceFilter, Interval: span}}
			syncer, err := NewSourceHistoricalSync(1, client, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			logs, err := syncer.SyncBlockRangeData(ctx, span, required, nil, source)
			if err != nil {
				t.Fatal(err)
			}
			if len(logs) != 1 || logs[0]["transactionHash"] != hash {
				t.Fatal("actual acquired logs", logs)
			}
			block, err := syncer.SyncBlockData(ctx, span, required, logs, source)
			if err != nil {
				t.Fatal(err)
			}
			if block == nil || block["hash"] != receipt["blockHash"] || len(block["transactions"].([]filter.Object)) != 1 {
				t.Fatal("closest acquired block", block)
			}
			for _, table := range []string{"blocks", "logs", "transactions", "transaction_receipts", "traces"} {
				rows, err := cache.Rows(ctx, table, 1, number, number)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows.Rows) != 1 {
					t.Fatal(table, "was not independently populated", rows)
				}
				var row map[string]any
				if err = json.Unmarshal([]byte(rows.Rows[0]["row"].(string)), &row); err != nil {
					t.Fatal(err)
				}
				if table == "blocks" && row["hash"] != receipt["blockHash"] {
					t.Fatal("cached block provenance", row)
				}
				if table == "logs" && row["transaction_hash"] != hash {
					t.Fatal("cached log provenance", row)
				}
			}
			if err = source.InsertIntervals(ctx, 1, required, nil); err != nil {
				t.Fatal(err)
			}
			// Re-acquiring the same provider data must preserve physical rows under
			// the actual source cache conflict policy.
			if _, err = syncer.SyncBlockData(ctx, span, required, logs, source); err != nil {
				t.Fatal(err)
			}
			rows, err := cache.Rows(ctx, "logs", 1, number, number)
			if err != nil || len(rows.Rows) != 1 {
				t.Fatal("duplicate acquisition changed source cache", rows, err)
			}
			compiled, err := config.CompileSourceFilter(logFilter)
			if err != nil {
				t.Fatal(err)
			}
			callback := config.SourceEventCallback{Name: "C:Seen", Type: "contract", Filter: compiled}
			typedLog := callback.Filter.(config.SourceLogFilter)
			previous, err := typedLog.Address()
			if err != nil {
				t.Fatal(err)
			}
			zero := "0x0000000000000000000000000000000000000000"
			if err = typedLog.SetAddress(config.SourceAddressList{string(previous.(config.SourceLiteralAddress)), zero}); err != nil {
				t.Fatal(err)
			}
			updated := callback.Filter.Value()
			cached, err := source.GetIntervals(ctx, []filter.Object{updated})
			if err != nil {
				t.Fatal(err)
			}
			missing, missingFactories, err := RequiredSourceIntervals([]filter.Object{updated}, span, cached.Entries())
			if err != nil {
				t.Fatal(err)
			}
			if len(missing) != 1 || len(missingFactories) != 0 || !reflect.DeepEqual(missing[0].Filter["address"], []any{zero}) {
				t.Fatal("native partial-cache recovery", missing, missingFactories)
			}
			var requestsMu sync.Mutex
			actualRequests := []filter.Object{}
			partialRPC := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
				requestsMu.Lock()
				actualRequests = append(actualRequests, filter.Object{"method": method, "params": params})
				requestsMu.Unlock()
				return client.Call(ctx, method, params, out)
			})
			partial, err := NewSourceHistoricalSync(1, partialRPC, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			missingLogs, err := partial.SyncBlockRangeData(ctx, span, missing, nil, source)
			if err != nil {
				t.Fatal(err)
			}
			missingBlock, err := partial.SyncBlockData(ctx, span, missing, missingLogs, source)
			if err != nil {
				t.Fatal(err)
			}
			if len(missingLogs) != 0 || missingBlock != nil || len(actualRequests) != 1 || actualRequests[0]["method"] != "eth_getLogs" {
				t.Fatal("partial cache refetched completed data", missingLogs, missingBlock, actualRequests)
			}
			if query := actualRequests[0]["params"].([]any)[0].(filter.Object); !reflect.DeepEqual(query["address"], []any{zero}) {
				t.Fatal("partial cache requested wrong address", query)
			}
			// Range requests start their chunks concurrently; RPC-owned memory
			// pagination has a separate lazy sequential policy.
			started := make(chan string, 4)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			requester := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
				if method != "eth_getLogs" {
					return fmt.Errorf("unexpected %s", method)
				}
				query := params.([]any)[0].(map[string]any)
				started <- query["fromBlock"].(string)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				return sourceHistoricalTestResult([]any{}, out)
			})
			two := uint64(2)
			chunked, err := NewSourceHistoricalSync(1, requester, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{EthGetLogsBlockRange: &two})
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				_, err := chunked.SyncBlockRangeData(ctx, store.SourceInterval{1, 4}, []store.SourceFilterInterval{{Filter: logFilter, Interval: store.SourceInterval{1, 4}}}, nil, source)
				finished <- err
			}()
			got := map[string]bool{}
			for len(got) < 2 {
				select {
				case from := <-started:
					got[from] = true
				case <-time.After(5 * time.Second):
					t.Fatal("historical chunks did not both start before either completed", got)
				}
			}
			if !got["0x1"] || !got["0x3"] {
				t.Fatal("unexpected chunk ranges", got)
			}
			once.Do(func() { close(release) })
			if err = <-finished; err != nil {
				t.Fatal(err)
			}
		})
	}
}
