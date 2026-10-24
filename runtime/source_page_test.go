package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Real EVM -> SourceSyncStore SQL page -> existing event builder/ABI decoder ->
// native handler -> atomic writer/source RPC cache, replay and actual EVM reorg.
// These backend/cache companions add no original test mappings.
func TestSourcePageRuntime(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) {
				ctx := context.Background()
				raw, endpoint := localChain(t)
				raw.RequestsPerSecond = 0
				backend := stagedBackend(t, ctx, dialect, "memory://")
				defer backend.Close()
				var callCount atomic.Int64
				proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					data, e := io.ReadAll(r.Body)
					if e != nil {
						http.Error(w, e.Error(), 500)
						return
					}
					var request struct{ Method string }
					if e = json.Unmarshal(data, &request); e != nil {
						http.Error(w, e.Error(), 500)
						return
					}
					if request.Method == "eth_call" {
						callCount.Add(1)
					}
					response, e := http.Post(endpoint, "application/json", bytes.NewReader(data))
					if e != nil {
						http.Error(w, e.Error(), 500)
						return
					}
					defer response.Body.Close()
					w.WriteHeader(response.StatusCode)
					_, _ = io.Copy(w, response.Body)
				}))
				defer proxy.Close()
				emitter := "0x1000000000000000000000000000000000000001"
				item := abi.Event{Type: "event", Name: "Seen", Inputs: []abi.Parameter{{Name: "amount", Type: "uint256"}}}
				var ignored any
				code := "0x6000356000527f" + abi.EventSelector(item)[2:] + "60206000a1602a60005260206000f3"
				if e := raw.Call(ctx, "anvil_setCode", []any{emitter, code}, &ignored); e != nil {
					t.Fatal(e)
				}
				var accounts []string
				if e := raw.Call(ctx, "eth_accounts", []any{}, &accounts); e != nil {
					t.Fatal(e)
				}
				emit := func(amount int) map[string]any {
					t.Helper()
					var hash string
					if e := raw.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": emitter, "gas": "0x186a0", "data": fmt.Sprintf("0x%064x", amount)}}, &hash); e != nil {
						t.Fatal(e)
					}
					deadline := time.Now().Add(5 * time.Second)
					for {
						var receipt map[string]any
						if e := raw.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); e != nil {
							t.Fatal(e)
						}
						if receipt != nil {
							if receipt["status"] != "0x1" {
								t.Fatal(receipt)
							}
							return receipt
						}
						if time.Now().After(deadline) {
							t.Fatal("source page fixture was not mined")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				receipt1 := emit(42)
				var snapshot string
				if e := raw.Call(ctx, "evm_snapshot", []any{}, &snapshot); e != nil {
					t.Fatal(e)
				}
				receipt2 := emit(7)
				model := schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "amount", Type: "bigint", NotNull: true}}}}}
				db, e := store.New(ctx, backend, model, fmt.Sprintf("ponder_go_srcpage_%x", time.Now().UnixNano()), schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				cache, e := db.EnableProtocolCache(ctx)
				if e != nil {
					t.Fatal(e)
				}
				source, e := cache.SourceSyncStore(0)
				if e != nil {
					t.Fatal(e)
				}
				importReceipt := func(receipt map[string]any) {
					t.Helper()
					var block map[string]any
					if e := raw.Call(ctx, "eth_getBlockByNumber", []any{receipt["blockNumber"], true}, &block); e != nil {
						t.Fatal(e)
					}
					transactions := []filter.Object{}
					for _, v := range block["transactions"].([]any) {
						transactions = append(transactions, v.(map[string]any))
					}
					logs := []filter.Object{}
					for _, v := range receipt["logs"].([]any) {
						logs = append(logs, v.(map[string]any))
					}
					if e := source.InsertBlocks(ctx, 1, []filter.Object{block}); e != nil {
						t.Fatal(e)
					}
					if e := source.InsertTransactions(ctx, 1, transactions); e != nil {
						t.Fatal(e)
					}
					if e := source.InsertTransactionReceipts(ctx, 1, []filter.Object{receipt}); e != nil {
						t.Fatal(e)
					}
					if e := source.InsertLogs(ctx, 1, logs); e != nil {
						t.Fatal(e)
					}
				}
				importReceipt(receipt1)
				importReceipt(receipt2)
				first, _ := Number(receipt1["blockNumber"].(string))
				last, _ := Number(receipt2["blockNumber"].(string))
				base, e := config.ParseSourceConfiguration([]byte(fmt.Sprintf(`{"chains":{"local":{"id":1,"rpc":%q,"cacheRpcRequests":%t}}}`, proxy.URL, !disabled)))
				if e != nil {
					t.Fatal(e)
				}
				failOnce := true
				attempts := 0
				handler := Handler(func(ctx context.Context, c *Context) error {
					attempts++
					values := c.Event.Args.(map[string]any)
					amount := values["amount"].(*big.Int)
					if c.Event.Normalized == nil || c.Event.Receipt == nil || c.Event.Receipt["status"] != "success" {
						return fmt.Errorf("source normalized receipt/event missing")
					}
					if e := c.DB.Write(ctx, "audit", store.Row{"id": c.Event.Normalized.Checkpoint, "amount": amount}, store.Insert); e != nil {
						return e
					}
					block := c.Event.Block.Number
					request := rpc.SourceCallRequest{Params: rpc.SourceCallParameters{Transaction: map[string]any{"to": emitter}, Block: &block}}
					for i := 0; i < 2; i++ {
						var result string
						if e := c.Client.CallSourceCached(ctx, request, &result); e != nil {
							return e
						}
						if result != "0x"+fmt.Sprintf("%064x", 42) {
							return fmt.Errorf("real typed cached RPC returned %s", result)
						}
					}
					if failOnce {
						failOnce = false
						return fmt.Errorf("injected source handler rollback")
					}
					return nil
				})
				typed := config.CreateTypedSourceConfiguration(base, "T", config.SourceTypedContract[int, int]{Chain: config.SourceOnChain("local"), ABI: json.RawMessage(`[{"type":"event","name":"Seen","inputs":[{"name":"amount","type":"uint256","indexed":false}]}]`), Address: config.SourceSome[any](emitter), StartBlock: int(first), EndBlock: int(last), IncludeTransactionReceipts: config.SourceSome(true)})
				built, e := config.BuildTypedSourceConfig(typed, config.SourceBuildOptions{})
				if e != nil {
					t.Fatal(e)
				}
				indexing, e := config.SafeBuildTypedSourceIndexing(ctx, typed, built, []config.SourceIndexingFunction{{Name: "T:Seen", Fn: handler}})
				if e != nil {
					t.Fatal(e)
				}
				engine, e := NewFromSource(indexing, "local", db, Options{DisableCache: disabled})
				if e != nil {
					t.Fatal(e)
				}
				engine.Client.RequestsPerSecond = 0
				f := indexing.EventCallbacks()[0][0].Filter.Value()
				if e = source.InsertIntervals(ctx, 1, []store.SourceFilterInterval{{Filter: f, Interval: store.SourceInterval{float64(first), float64(last)}}}, nil); e != nil {
					t.Fatal(e)
				}
				page, e := engine.ReadSourcePage(ctx, source, float64(first), float64(last), 1)
				if e != nil || len(page.Events) != 1 || page.Cursor != float64(first) {
					t.Fatalf("source pagination page=%+v error=%v", page, e)
				}
				if e = engine.DispatchSourceEvents(ctx, page.Events); e == nil || !strings.Contains(e.Error(), "injected source handler rollback") {
					t.Fatal("handler failure not returned", e)
				}
				rows, e := db.Read(ctx, "audit")
				if e != nil || len(rows.Rows) != 0 {
					t.Fatal("failed event business write survived", rows, e)
				}
				countRows := func() int {
					t.Helper()
					rows, e := backend.Query(ctx, "SELECT request_hash FROM "+schema.Quote(cache.Namespace())+".rpc_request_results WHERE chain_id=1 AND length(request_hash)=32")
					if e != nil {
						t.Fatal(e)
					}
					return len(rows.Rows)
				}
				if countRows() != 0 {
					t.Fatal("failed event source RPC cache survived")
				}
				if e = engine.DispatchSourceEvents(ctx, page.Events); e != nil {
					t.Fatal(e)
				}
				beforeReplay := callCount.Load()
				if e = engine.DispatchSourceEvents(ctx, page.Events); e != nil || callCount.Load() != beforeReplay || attempts != 2 {
					t.Fatal("source page replay repeated handler", attempts, callCount.Load(), e)
				}
				second, e := engine.ReadSourcePage(ctx, source, page.Cursor+1, float64(last), 1)
				if e != nil || len(second.Events) != 1 || second.Cursor != float64(last) {
					t.Fatal("second source page", second, e)
				}
				if e = engine.DispatchSourceEvents(ctx, second.Events); e != nil {
					t.Fatal(e)
				}
				checkSum := func(want int64, count int) {
					t.Helper()
					rows, e := db.Read(ctx, "audit")
					if e != nil || len(rows.Rows) != count {
						t.Fatal(rows, e)
					}
					sum := new(big.Int)
					for _, row := range rows.Rows {
						n, ok := new(big.Int).SetString(fmt.Sprint(row["amount"]), 10)
						if !ok {
							t.Fatal(row)
						}
						sum.Add(sum, n)
					}
					if sum.Int64() != want {
						t.Fatal("source handler sum", sum, want)
					}
				}
				checkSum(49, 2)
				wantCalls := int64(3)
				wantCache := 2
				if disabled {
					wantCalls = 6
					wantCache = 0
				}
				if callCount.Load() != wantCalls || countRows() != wantCache {
					t.Fatal("source MD5 cache/transport count", callCount.Load(), countRows(), wantCalls, wantCache)
				}
				firstCursor, e := checkpoint.Decode(page.Events[0].Checkpoint)
				if e != nil {
					t.Fatal(e)
				}
				if e = db.RevertAndRewind(ctx, checkpoint.Block(firstCursor.BlockTimestamp, 1, first, true)); e != nil {
					t.Fatal(e)
				}
				checkSum(42, 1)
				intervals, e := source.GetIntervals(ctx, []filter.Object{f})
				if e != nil {
					t.Fatal(e)
				}
				coverage, ok := intervals.Get(f)
				if !ok || len(coverage) != 1 || len(coverage[0].Intervals) != 1 || coverage[0].Intervals[0] != (store.SourceInterval{float64(first), float64(first)}) {
					t.Fatal("source interval rewind", coverage)
				}
				var reverted bool
				if e = raw.Call(ctx, "evm_revert", []any{snapshot}, &reverted); e != nil || !reverted {
					t.Fatal(reverted, e)
				}
				replacement := emit(9)
				if replacement["blockHash"] == receipt2["blockHash"] {
					t.Fatal("real EVM fork hash did not change")
				}
				importReceipt(replacement)
				if e = source.InsertIntervals(ctx, 1, []store.SourceFilterInterval{{Filter: f, Interval: store.SourceInterval{float64(last), float64(last)}}}, nil); e != nil {
					t.Fatal(e)
				}
				next, e := engine.ReadSourcePage(ctx, source, float64(last), float64(last), 1)
				if e != nil || len(next.Events) != 1 {
					t.Fatal(next, e)
				}
				if e = engine.DispatchSourceEvents(ctx, next.Events); e != nil {
					t.Fatal(e)
				}
				checkSum(51, 2)
				wantCalls++
				if disabled {
					wantCalls++
				}
				if callCount.Load() != wantCalls || countRows() != wantCache {
					t.Fatal("source reorg RPC cache not invalidated", callCount.Load(), countRows(), wantCalls, wantCache)
				}
			})
		}
	}
}
