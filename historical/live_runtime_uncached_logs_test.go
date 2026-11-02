package historical

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Actual static ABI logs pass through both historical memory acquisition and
// live decoding, with fresh RPC results and native business writes.
func TestRuntimeUncachedStaticLogs(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			p := &finiteRPC{}
			p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					ID     json.RawMessage
					Method string
					Params []any
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				id, _ := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/"), 10, 64)
				txhash := func(n uint64) string { return fmt.Sprintf("0x%064x", id*10000+n) }
				block := func(n uint64) map[string]any {
					b := finiteBlock(id, n)
					b["logsBloom"] = "0x" + strings.Repeat("f", 512)
					b["transactions"] = []any{map[string]any{"hash": txhash(n), "blockHash": b["hash"], "blockNumber": b["number"], "transactionIndex": "0x0", "from": "0x" + strings.Repeat("1", 40), "to": "0x" + strings.Repeat("2", 40), "gas": "0x5208", "gasPrice": "0x1", "value": "0x0", "nonce": "0x0", "type": "0x0", "input": "0x", "r": "0x1", "s": "0x1", "v": "0x1b"}}
					return b
				}
				var result any
				var err error
				switch q.Method {
				case "eth_getBlockByNumber":
					n := uint64(3)
					if q.Params[0] != "latest" {
						n, err = strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(q.Params[0]), "0x"), 16, 64)
					}
					result = block(n)
				case "eth_getBlockByHash":
					n, e := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(q.Params[0]), "0x"), 16, 64)
					err = e
					result = block(n - id*1000000)
				case "eth_getBalance":
					result = "0x2a"
				case "eth_getLogs":
					f := q.Params[0].(map[string]any)
					from, _ := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(f["fromBlock"]), "0x"), 16, 64)
					to, _ := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(f["toBlock"]), "0x"), 16, 64)
					if hash, ok := f["blockHash"]; ok {
						n, e := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(hash), "0x"), 16, 64)
						if e != nil {
							t.Error(e)
						}
						from = n - id*1000000
						to = from
					}
					topics := f["topics"].([]any)
					topic := topics[0]
					if xs, ok := topic.([]any); ok {
						topic = xs[0]
					}
					logs := []any{}
					for n := from; n <= to; n++ {
						b := block(n)
						logs = append(logs, map[string]any{"address": "0x" + strings.Repeat("2", 40), "blockHash": b["hash"], "blockNumber": b["number"], "transactionHash": txhash(n), "transactionIndex": "0x0", "logIndex": "0x0", "removed": false, "topics": []any{topic}, "data": fmt.Sprintf("0x%064x", n)})
					}
					result = logs
				default:
					err = fmt.Errorf("unexpected ABI fixture RPC %s", q.Method)
				}
				out := map[string]any{"jsonrpc": "2.0", "id": q.ID}
				if err != nil {
					out["error"] = map[string]any{"code": -32000, "message": err.Error()}
				} else {
					out["result"] = result
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
			}))
			t.Cleanup(p.server.Close)
			calls := map[uint64]int{}
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				var value any
				if args, ok := a.Event.Property("args"); ok {
					var err error
					value, err = args.Get("value")
					if err != nil {
						return err
					}
				} else {
					value = a.Event.Get("args").(map[string]any)["value"]
				}
				n, err := continuousNumber(a)
				if err != nil {
					return err
				}
				if fmt.Sprint(value) != fmt.Sprint(n) {
					return fmt.Errorf("ABI value=%v block=%d", value, n)
				}
				v, err := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
				if err != nil || v.String() != "42" {
					return fmt.Errorf("ABI client value=%v err=%v", v, err)
				}
				calls[finiteChain(a)]++
				return finiteIncrement(ctx, a)
			})
			raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":false},"two":{"id":2,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":false}},"contracts":{"C":{"abi":[{"type":"event","name":"Ping","inputs":[{"name":"value","type":"uint256","indexed":false}]}],"address":"0x%s","chain":{"one":{},"two":{}},"startBlock":1,"endBlock":3}}}`, p.server.URL+"/1", p.server.URL+"/2", strings.Repeat("2", 40))
			cfg, err := config.ParseSourceConfiguration([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range base.RPCClients() {
				r.Retries = 0
				r.RequestsPerSecond = 0
			}
			b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "C:Ping", Fn: h}})
			if err != nil {
				t.Fatal(err)
			}
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			r := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "uncached-static-abi", OpenID: "static-first", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}})
			uncachedSeed(t, r, 1)
			uncachedSeed(t, r, 2)
			runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			out, err := r.Run(runCtx)
			if err != nil || !out.Completed || !out.Ready || out.HistoricalBlocks != 4 || out.LiveBlocks != 2 || calls[1] != 3 || calls[2] != 3 {
				t.Fatalf("static ABI runtime=%+v calls=%v err=%v", out, calls, err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			uncachedRawInspect(t, f, []uint64{1, 2})
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "3" || fmt.Sprint(rows[1]["balance"]) != "3" {
					t.Fatalf("ABI business writes=%v", rows)
				}
			})
		})
	}
}
