package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
)

func TestSourceSyncTransaction(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"atomic commit and own reads", "callback rollback", "later batch rollback and standalone partial commit", "nested joins and error poison", "ignored database error", "cancellation", "panic rollback", "escaped handles", "concurrent bound requests"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					b := sourceBackend(t, ctx, dialect)
					db, err := New(ctx, b, sourceActionsModel(), fmt.Sprintf("ponder_go_source_tx_%x", time.Now().UnixNano()), schema.Multichain)
					if err != nil {
						t.Fatal(err)
					}
					cache, err := db.EnableProtocolCache(ctx)
					if err != nil {
						t.Fatal(err)
					}
					ss, err := cache.SourceSyncStore(len(protocolFields["logs"]) + 1)
					if err != nil {
						t.Fatal(err)
					}
					fixtures := map[string]filter.Object{}
					for _, v := range protocolVectors(t) {
						if v.Name == v.Table+"/full" {
							fixtures[v.Table] = v.Input
						}
					}
					marker := errors.New("test transaction rollback")
					req := map[string]any{"method": "eth_chainId", "params": []any{}}
					assertCount := func(table string, want int) {
						t.Helper()
						r, e := b.Query(ctx, "SELECT count(*)::text AS n FROM "+cache.table(table))
						if e != nil || fmt.Sprint(r.Rows[0]["n"]) != fmt.Sprint(want) {
							t.Fatalf("%s count: %v %v, want %d", table, r, e, want)
						}
					}
					insert := func(bound *SourceSyncStore) error { return bound.InsertLogs(ctx, 1, []filter.Object{fixtures["logs"]}) }
					switch name {
					case "atomic commit and own reads":
						// Raise budget for a full block but retain the same owned cache.
						ss, _ = cache.SourceSyncStore(16000)
						err = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error {
							if e := bound.InsertBlocks(ctx, 1, []filter.Object{fixtures["blocks"]}); e != nil {
								return e
							}
							if e := insert(bound); e != nil {
								return e
							}
							f := filter.Object{"type": "block", "chainId": float64(1), "interval": float64(1), "offset": float64(0), "fromBlock": float64(0)}
							if e := bound.InsertIntervals(ctx, 1, []SourceFilterInterval{{Filter: f, Interval: SourceInterval{0, 1}}}, nil); e != nil {
								return e
							}
							ranges, e := bound.GetIntervals(ctx, []filter.Object{f})
							if e != nil {
								return e
							}
							if ranges.Len() != 1 {
								return fmt.Errorf("missing own intervals")
							}
							if e := bound.InsertChildAddresses(ctx, 1, []SourceFactoryChildren{{FragmentID: "tx-test-factory", Children: []SourceChildAddress{{Address: sourceZeroAddress, BlockNumber: 1}}}}); e != nil {
								return e
							}
							if e := bound.InsertRPCRawRequestResults(ctx, 1, []SourceRPCRawResult{{Request: req, Result: `"0x1"`}}); e != nil {
								return e
							}
							results, e := bound.GetRPCRawRequestResults(ctx, 1, []any{req})
							if e != nil {
								return e
							}
							if len(results) != 1 || results[0] == nil || *results[0] != `"0x1"` {
								return fmt.Errorf("own RPC read failed")
							}
							recovery, e := bound.GetSafeCrashRecoveryBlock(ctx, 1, 1000000)
							if e != nil {
								return e
							}
							if recovery == nil {
								return fmt.Errorf("own block read missing")
							}
							return nil
						})
						if err != nil {
							t.Fatal(err)
						}
						for _, table := range []string{"blocks", "logs", "intervals", "factories", "factory_addresses", "rpc_request_results"} {
							assertCount(table, 1)
						}
					case "callback rollback":
						err = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error {
							if e := insert(bound); e != nil {
								return e
							}
							if e := bound.InsertRPCRawRequestResults(ctx, 1, []SourceRPCRawResult{{Request: req, Result: `1`}}); e != nil {
								return e
							}
							return marker
						})
						if !errors.Is(err, marker) {
							t.Fatal(err)
						}
						assertCount("logs", 0)
						assertCount("rpc_request_results", 0)
					case "later batch rollback and standalone partial commit":
						rows := []filter.Object{fixtures["logs"], {"blockNumber": "0x1"}}
						err = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error { return bound.InsertLogs(ctx, 1, rows) })
						if err == nil {
							t.Fatal("malformed later batch accepted")
						}
						assertCount("logs", 0)
						if err = ss.InsertLogs(ctx, 1, rows); err == nil {
							t.Fatal("standalone malformed batch accepted")
						}
						assertCount("logs", 1)
					case "nested joins and error poison":
						err = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error {
							if e := bound.WithTransaction(ctx, insert); e != nil {
								return e
							}
							inner := bound.WithTransaction(ctx, func(*SourceSyncStore) error { return marker })
							if !errors.Is(inner, marker) {
								return fmt.Errorf("nested error lost: %v", inner)
							}
							return nil // Explicitly ignoring it must not commit earlier writes.
						})
						if !errors.Is(err, marker) {
							t.Fatal(err)
						}
						assertCount("logs", 0)
					case "ignored database error":
						err = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error {
							if e := insert(bound); e != nil {
								return e
							}
							_ = bound.transaction(ctx, func(tx Transaction) error { return tx.Exec(ctx, "SELECT 1/0") })
							return nil
						})
						if err == nil {
							t.Fatal("failed DB transaction committed")
						}
						assertCount("logs", 0)
					case "cancellation":
						cancelled, cancel := context.WithCancel(ctx)
						defer cancel()
						err = ss.WithTransaction(cancelled, func(bound *SourceSyncStore) error {
							if e := bound.InsertLogs(cancelled, 1, []filter.Object{fixtures["logs"]}); e != nil {
								return e
							}
							cancel()
							return nil
						})
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
						assertCount("logs", 0)
					case "panic rollback":
						func() {
							defer func() {
								if got := recover(); got != marker {
									t.Fatalf("panic changed: %v", got)
								}
							}()
							_ = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error {
								if e := insert(bound); e != nil {
									return e
								}
								panic(marker)
							})
						}()
						assertCount("logs", 0)
					case "escaped handles":
						var escaped *SourceSyncStore
						var adapter *SourceRPCRequestCache
						err = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error {
							escaped = bound
							adapter = bound.RPCRequestCache(1)
							return insert(bound)
						})
						if err != nil {
							t.Fatal(err)
						}
						assertCount("logs", 1)
						for _, call := range []func() error{
							func() error { return escaped.InsertLogs(ctx, 1, nil) },
							func() error { return escaped.InsertIntervals(ctx, 1, nil, nil) },
							func() error { return escaped.InsertChildAddresses(ctx, 1, nil) },
							func() error { _, e := escaped.GetRPCRawRequestResults(ctx, 1, nil); return e },
							func() error { return escaped.WithTransaction(ctx, func(*SourceSyncStore) error { return nil }) },
							func() error { return adapter.CachePut(ctx, `[1,"eth_chainId",[]]`, json.RawMessage(`1`)) },
							func() error { _, _, e := adapter.CacheGet(ctx, `[1,"eth_chainId",[]]`); return e },
						} {
							if e := call(); e == nil || !strings.Contains(e.Error(), "closed") {
								t.Fatalf("escaped handle not closed: %v", e)
							}
						}
					case "concurrent bound requests":
						err = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error {
							var wg sync.WaitGroup
							results := make(chan error, 12)
							for i := range 12 {
								wg.Add(1)
								go func(i int) {
									defer wg.Done()
									r := map[string]any{"method": "eth_chainId", "params": []any{i}}
									e := bound.InsertRPCRawRequestResults(ctx, 1, []SourceRPCRawResult{{Request: r, Result: fmt.Sprint(i)}})
									if e == nil {
										var got []*string
										got, e = bound.GetRPCRawRequestResults(ctx, 1, []any{r})
										if e == nil && (len(got) != 1 || got[0] == nil || *got[0] != fmt.Sprint(i)) {
											e = fmt.Errorf("concurrent result mismatch")
										}
									}
									results <- e
								}(i)
							}
							wg.Wait()
							close(results)
							for e := range results {
								if e != nil {
									return e
								}
							}
							adapter := bound.RPCRequestCache(1)
							if e := adapter.CachePut(ctx, `[1,"eth_chainId",[]]`, json.RawMessage(`1`)); e != nil {
								return e
							}
							value, ok, e := adapter.CacheGet(ctx, `[1,"eth_chainId",[]]`)
							if e != nil {
								return e
							}
							if !ok || string(value) != "1" {
								return fmt.Errorf("bound adapter failed")
							}
							return nil
						})
						if err != nil {
							t.Fatal(err)
						}
						assertCount("rpc_request_results", 13)
					}
				})
			}
		})
	}
}
