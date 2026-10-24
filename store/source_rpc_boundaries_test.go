package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
)

func TestSourceRPCBoundaries(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, name := range []string{"duplicate_chain_zero_native", "range_100_101", "prune_null_metadata", "failed_batch_and_lifetime"} {
			t.Run(dialect+"/"+name, func(t *testing.T) {
				ctx := context.Background()
				backend := sourceBackend(t, ctx, dialect)
				db, e := New(ctx, backend, sourceActionsModel(), fmt.Sprintf("ponder_go_srcrpc_%x", time.Now().UnixNano()), schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				cache, e := db.EnableProtocolCache(ctx)
				if e != nil {
					t.Fatal(e)
				}
				request := func(n int) any {
					return map[string]any{"method": "eth_getBlockByNumber", "params": []any{fmt.Sprintf("0x%x", n), false}}
				}
				entry := func(n int, result string) SourceRPCRawResult {
					block := uint64(n)
					return SourceRPCRawResult{Request: request(n), BlockNumber: &block, Result: result}
				}
				get := func(chain uint64, values ...any) []*string {
					t.Helper()
					rows, e := cache.GetSourceRPCRawRequestResults(ctx, chain, values)
					if e != nil {
						t.Fatal(e)
					}
					return rows
				}
				want := func(v *string, text string) {
					t.Helper()
					if v == nil || *v != text {
						t.Fatal("source cache value", v, text)
					}
				}
				switch name {
				case "duplicate_chain_zero_native":
					if e = cache.InsertSourceRPCRawRequestResults(ctx, 1, []SourceRPCRawResult{entry(0, "first"), entry(0, "second")}); e != nil {
						t.Fatal(e)
					}
					if e = cache.InsertSourceRPCRawRequestResults(ctx, 2, []SourceRPCRawResult{entry(0, "other")}); e != nil {
						t.Fatal(e)
					}
					want(get(1, request(0))[0], "first")
					want(get(2, request(0))[0], "other")
					rows, e := backend.Query(ctx, "SELECT block_number FROM "+cache.table("rpc_request_results")+" WHERE chain_id=1")
					if e != nil || len(rows.Rows) != 1 || rows.Rows[0]["block_number"] != nil {
						t.Fatal("source explicit zero must store NULL", rows, e)
					}
					key := `[1,"eth_getBlockByNumber",["0x0",false]]`
					if e = cache.CachePut(ctx, key, json.RawMessage(`"native1"`)); e != nil {
						t.Fatal(e)
					}
					if e = cache.CachePut(ctx, key, json.RawMessage(`"native2"`)); e != nil {
						t.Fatal(e)
					}
					value, ok, e := cache.CacheGet(ctx, key)
					if e != nil || !ok || string(value) != `"native2"` {
						t.Fatal("native SHA/update contract", string(value), ok, e)
					}
					want(get(1, request(0))[0], "first")
					rows, e = backend.Query(ctx, "SELECT length(request_hash) AS length FROM "+cache.table("rpc_request_results")+" WHERE chain_id=1 ORDER BY length(request_hash)")
					if e != nil || len(rows.Rows) != 2 || fmt.Sprint(rows.Rows[0]["length"]) != "32" || fmt.Sprint(rows.Rows[1]["length"]) != "64" {
						t.Fatal("source/native key namespaces", rows, e)
					}
				case "range_100_101":
					values := []SourceRPCRawResult{}
					requests := []any{}
					for n := 0; n < 101; n++ {
						values = append(values, entry(n, fmt.Sprintf("v%d", n)))
						requests = append(requests, request(n))
					}
					other := map[string]any{"method": "eth_chainId", "params": []any{}}
					values = append(values, SourceRPCRawResult{Request: other, Result: "chain"})
					if e = cache.InsertSourceRPCRawRequestResults(ctx, 1, values); e != nil {
						t.Fatal(e)
					}
					first := get(1, requests[:100]...)
					want(first[0], "v0")
					want(first[99], "v99")
					many := get(1, append(requests, other)...)
					if len(many) != 102 || many[0] != nil {
						t.Fatal("source >100 numeric range omits genesis NULL", many)
					}
					for n := 1; n < 101; n++ {
						want(many[n], fmt.Sprintf("v%d", n))
					}
					want(many[101], "chain")
				case "prune_null_metadata":
					latest := map[string]any{"method": "eth_getBalance", "params": []any{"0x1", "latest"}}
					values := []SourceRPCRawResult{entry(0, "genesis"), entry(1, "one"), {Request: latest, Result: "latest"}}
					if e = cache.InsertSourceRPCRawRequestResults(ctx, 1, values); e != nil {
						t.Fatal(e)
					}
					if e = cache.InsertSourceRPCRawRequestResults(ctx, 2, []SourceRPCRawResult{entry(1, "other")}); e != nil {
						t.Fatal(e)
					}
					if e = cache.PruneSourceRPCRequestResults(ctx, 1, []uint64{0, 1}); e != nil {
						t.Fatal(e)
					}
					rows := get(1, request(0), request(1), latest)
					want(rows[0], "genesis")
					if rows[1] != nil {
						t.Fatal("numbered result not pruned")
					}
					want(rows[2], "latest")
					want(get(2, request(1))[0], "other")
				case "failed_batch_and_lifetime":
					if e = backend.Exec(ctx, "ALTER TABLE "+cache.table("rpc_request_results")+" ADD CONSTRAINT reject_source_result CHECK(result <> 'bad')"); e != nil {
						t.Fatal(e)
					}
					if e = cache.InsertSourceRPCRawRequestResults(ctx, 1, []SourceRPCRawResult{entry(1, "good"), entry(2, "bad")}); e == nil {
						t.Fatal("injected SQL batch accepted")
					}
					if rows := get(1, request(1), request(2)); rows[0] != nil || rows[1] != nil {
						t.Fatal("partial source RPC batch committed", rows)
					}
					if e = cache.InsertSourceRPCRawRequestResults(ctx, 1, []SourceRPCRawResult{entry(1, "good"), {Request: map[string]any{"method": "eth_call", "params": []any{nil}}, Result: "invalid"}}); e == nil {
						t.Fatal("source orderObject(null) accepted")
					}
					if get(1, request(1))[0] != nil {
						t.Fatal("failed hash batch committed first row")
					}
					if _, e = rpc.SourceRequestValue(nil); e == nil {
						t.Fatal("nil typed request accepted")
					}
					adapter := cache.SourceRPCRequestCache(1)
					if _, _, e = adapter.CacheGet(ctx, `[2,"eth_chainId",[]]`); e == nil || !strings.Contains(e.Error(), "chain mismatch") {
						t.Fatal("source cache chain mismatch", e)
					}
					if e = cache.Close(); e != nil {
						t.Fatal(e)
					}
					if _, _, e = adapter.CacheGet(ctx, `[1,"eth_chainId",[]]`); e == nil {
						t.Fatal("source adapter read after close")
					}
				}
			})
		}
	}
}
