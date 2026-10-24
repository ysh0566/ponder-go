package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

// Candidate exact original replay. Remains unmapped until all nine type
// obligations and complete pristine/call-through original backend evidence pass.
func TestUpstreamSourceRPCCache(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, name := range []string{"insertRpcRequestResults() ", "insertRpcRequestResults() hash matches postgres", "getRpcRequestResults()", "pruneRpcRequestResult"} {
			t.Run(dialect+"/"+name, func(t *testing.T) {
				ctx := context.Background()
				backend := sourceBackend(t, ctx, dialect)
				s, err := New(ctx, backend, sourceActionsModel(), fmt.Sprintf("ponder_go_source_rpc_%x", time.Now().UnixNano()), schema.Multichain)
				if err != nil {
					t.Fatal(err)
				}
				cache, err := s.EnableProtocolCache(ctx)
				if err != nil {
					t.Fatal(err)
				}
				request := func(value string) any { return map[string]any{"method": "eth_call", "params": []any{value}} }
				block := uint64(1)
				if name == "pruneRpcRequestResult" {
					entries := []SourceRPCRawResult{}
					for i := uint64(1); i <= 4; i++ {
						value := fmt.Sprintf("0x%d", i)
						n := i
						entries = append(entries, SourceRPCRawResult{Request: request(value), BlockNumber: &n, Result: value})
					}
					if err = cache.InsertSourceRPCRawRequestResults(ctx, 1, entries); err != nil {
						t.Fatal(err)
					}
					if err = cache.PruneSourceRPCRequestResults(ctx, 1, []uint64{2, 4}); err != nil {
						t.Fatal(err)
					}
					rows, err := backend.Query(ctx, "SELECT * FROM "+cache.table("rpc_request_results"))
					if err != nil {
						t.Fatal(err)
					}
					if len(rows.Rows) != 2 {
						t.Fatal("original remaining request count", rows)
					}
					return
				}
				if err = cache.InsertSourceRPCRawRequestResults(ctx, 1, []SourceRPCRawResult{{Request: request("0x1"), BlockNumber: &block, Result: "0x1"}}); err != nil {
					t.Fatal(err)
				}
				switch name {
				case "insertRpcRequestResults() ":
					rows, err := backend.Query(ctx, "SELECT * FROM "+cache.table("rpc_request_results"))
					if err != nil {
						t.Fatal(err)
					}
					if len(rows.Rows) != 1 {
						t.Fatal(rows)
					}
					if rows.Rows[0]["request_hash"] != "39d5ace8093d42c1bd00ce7781a7891a" {
						t.Fatal(rows)
					}
					if rows.Rows[0]["result"] != "0x1" {
						t.Fatal(rows)
					}
				case "insertRpcRequestResults() hash matches postgres":
					rows, err := backend.Query(ctx, "SELECT * FROM "+cache.table("rpc_request_results"))
					if err != nil {
						t.Fatal(err)
					}
					jsHash := rows.Rows[0]["request_hash"]
					rows, err = backend.Query(ctx, "SELECT MD5($1) as request_hash", `{"method":"eth_call","params":["0x1"]}`)
					if err != nil {
						t.Fatal(err)
					}
					if jsHash != rows.Rows[0]["request_hash"] {
						t.Fatal(jsHash, rows)
					}
				case "getRpcRequestResults()":
					values, err := cache.GetSourceRPCRawRequestResults(ctx, 1, []any{request("0x1"), request("0x2")})
					if err != nil {
						t.Fatal(err)
					}
					one := "0x1"
					if !reflect.DeepEqual(values, []*string{&one, nil}) {
						t.Fatal(values)
					}
				}
			})
		}
	}
}
