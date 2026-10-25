package runtime

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Supplemental composition only: actual M37 acquisition/storage/Decode output
// feeds the handler service. The caller deliberately owns ONE native transaction
// checkpoint; this is not an implementation of the source outer multichain runner.
// Native PGlite rollback is a safety extension over source's callback-only wrapper.
func TestSourceHistoricalIndexingServiceComposition(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			if dialect == "postgres" && os.Getenv("PONDER_TEST_POSTGRES") == "" {
				t.Skip("explicit task-only PostgreSQL required; no parity credit")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			backend := stagedBackend(t, ctx, dialect, "memory://")
			defer backend.Close()
			model := schema.Schema{Tables: []schema.Table{{Name: "handled", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "chain", Type: "integer", NotNull: true}, {Name: "block", Type: "bigint", NotNull: true}, {Name: "ordinal", Type: "integer", NotNull: true}}}}}
			db, e := store.New(ctx, backend, model, fmt.Sprintf("ponder_go_m38_composition_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			cache, e := db.EnableProtocolCache(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				cache.Close()
				for _, ns := range []string{cache.Namespace(), db.SyncNamespace, db.Namespace} {
					if e := backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(ns)+" CASCADE"); e != nil {
						t.Error(e)
					}
				}
			}()
			source, e := cache.SourceSyncStore(0)
			if e != nil {
				t.Fatal(e)
			}
			block := localControlRawBlock(t)
			build := indexing.Build{}
			options := SourceHistoricalCoordinatorOptions{QuerySize: 100, Clock: func() time.Time { return time.Unix(0, 0) }}
			requesters := []rpc.Requester{}
			balanceTags := map[uint64][]string{}
			var clients *indexing.ClientSet
			var expectedDB *indexing.Database
			var firstContext *indexing.Context
			var handled []string
			var expected []string
			var abort bool
			failure := errors.New("intentional composition callback failure")
			for _, id := range []uint64{1, 2} {
				chain := indexing.Object{"id": id, "name": fmt.Sprintf("chain%d", id), "cacheRpcRequests": true}
				contract := indexing.Object{"Token": indexing.Object{"address": fmt.Sprintf("0x%040x", id)}}
				f := filter.Object{"type": "block", "chainId": float64(id), "fromBlock": float64(0), "interval": float64(1), "offset": float64(0), "include": []any{"block.number", "block.hash", "block.timestamp"}}
				name := fmt.Sprintf("Chain%d:block", id)
				handler := indexing.Handler(func(callCtx context.Context, args *indexing.HandlerArgs) error {
					if firstContext == nil {
						firstContext = args.Context
					} else if firstContext != args.Context {
						t.Fatal("context identity changed")
					}
					if args.Context.DB != expectedDB {
						t.Fatal("database context identity changed")
					}
					if args.Context.Chain["id"] != id || args.Context.Chain["name"] != chain["name"] {
						t.Fatal("wrong dispatched chain", args.Context.Chain)
					}
					if reflect.ValueOf(args.Context.Contracts).Pointer() != reflect.ValueOf(contract).Pointer() {
						t.Fatal("contracts identity changed")
					}
					expectedClient, _ := clients.GetClient(chain)
					if args.Context.Client != expectedClient {
						t.Fatal("client identity changed")
					}
					property, ok := args.Event.Property("block")
					if !ok {
						t.Fatal("historical block accessor missing")
					}
					value, err := property.Get("number")
					if err != nil {
						return err
					}
					number, ok := value.(*big.Int)
					if !ok {
						t.Fatalf("decoded block number has type %T", value)
					}
					balance, err := args.Context.Client.GetBalance(callCtx, "0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
					if err != nil {
						return err
					}
					if balance.Int64() != 42 {
						t.Fatal(balance)
					}
					key := fmt.Sprintf("%d:%s", id, number)
					handled = append(handled, key)
					_, err = args.Context.DB.InsertOne(callCtx, "handled", store.Row{"id": key, "chain": int64(id), "block": number, "ordinal": int64(len(handled))}, store.Conflict{})
					if err != nil {
						return err
					}
					if abort && len(handled) == 4 {
						return failure
					}
					return nil
				})
				callback := indexing.Object{"filter": f, "name": name, "type": "block", "fn": handler}
				requester := sourceHistoricalTestRequester(func(requestCtx context.Context, method string, params, out any) error {
					if err := requestCtx.Err(); err != nil {
						return err
					}
					switch method {
					case "eth_getBlockByNumber":
						n, err := sourceHistoricalNumber(params.([]any)[0])
						if err != nil {
							return err
						}
						return sourceHistoricalTestResult(block(n), out)
					case "eth_getBalance":
						p := params.([]any)
						if p[0] != "0x70997970C51812dc3A010C7d01b50e0d17dc79C8" {
							return fmt.Errorf("wrong balance address")
						}
						balanceTags[id] = append(balanceTags[id], p[1].(string))
						return sourceHistoricalTestResult("0x2a", out)
					default:
						return fmt.Errorf("unexpected composition RPC %s", method)
					}
				})
				intervals, err := EmptySourceCachedIntervals([]filter.Object{f})
				if err != nil {
					t.Fatal(err)
				}
				local := SourceLocalHistoricalOptions{ChainID: id, Requester: requester, Store: source, Children: NewSourceHistoricalChildren(nil), Progress: &SourceSyncProgress{ChainID: id, Start: block(0), End: block(2), Finalized: block(2)}, EventCallbacks: []filter.Object{callback}, CachedIntervals: intervals}
				options.Chains = append(options.Chains, SourceHistoricalChain{Chain: chain, Local: local, CacheRPCRequests: true})
				build.Chains = append(build.Chains, chain)
				build.Contracts = append(build.Contracts, contract)
				build.EventCallbacks = append(build.EventCallbacks, []indexing.Object{callback})
				build.IndexingFunctions = append(build.IndexingFunctions, indexing.Object{"name": name, "fn": handler})
				requesters = append(requesters, requester)
			}
			clients, e = indexing.NewClientSet(build.Chains, requesters)
			if e != nil {
				t.Fatal(e)
			}
			defer clients.Close(context.Background())
			var events []indexing.Object
			lastByChain := map[uint64]int64{}
			counts := map[uint64]int{}
			var maxCheckpoint string
			for group, err := range HistoricalSourceMultichain(ctx, options) {
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range group.Events {
					if event["type"] != "block" {
						t.Fatal("not actual decoded block", event)
					}
					body := event["event"].(map[string]any)
					n := body["block"].(map[string]any)["number"].(*big.Int).Int64()
					id := group.ChainID
					if counts[id] > 0 && n != lastByChain[id]+1 {
						t.Fatal("historical chain order changed")
					}
					lastByChain[id] = n
					counts[id]++
					expected = append(expected, fmt.Sprintf("%d:%d", id, n))
					events = append(events, event)
					cp := event["checkpoint"].(string)
					if cp > maxCheckpoint {
						maxCheckpoint = cp
					}
				}
			}
			if counts[1] != 3 || counts[2] != 3 || len(events) != 6 {
				t.Fatalf("actual historical outputs %v", counts)
			}
			cp, e := checkpoint.Decode(maxCheckpoint)
			if e != nil {
				t.Fatal(e)
			}
			run := func(eventID string, fail bool) error {
				handled = nil
				firstContext = nil
				abort = fail
				balanceTags = map[uint64][]string{}
				return db.Apply(ctx, store.Event{ID: eventID, Checkpoint: cp}, func(w *store.Writer) error {
					state := &indexing.ErrorState{}
					expectedDB = indexing.NewDatabase(w.Indexing(), state)
					service, err := indexing.NewService(indexing.Options{Build: build, Database: expectedDB, Clients: clients, Errors: state})
					if err != nil {
						return err
					}
					return service.ProcessHistoricalEvents(ctx, events, nil)
				})
			}
			if e = run("rollback", true); e == nil {
				t.Fatal("callback failure committed")
			}
			table := schema.Quote(db.Namespace) + ".handled"
			rows, e := backend.Query(ctx, "SELECT * FROM "+table)
			if e != nil || len(rows.Rows) != 0 {
				t.Fatalf("rollback rows %v %v", rows, e)
			}
			for _, internal := range []string{"_ponder_go_journal", "_ponder_go_events"} {
				rolledBack, err := backend.Query(ctx, "SELECT * FROM "+schema.Quote(db.Namespace)+"."+schema.Quote(internal))
				if err != nil || len(rolledBack.Rows) != 0 {
					t.Fatalf("rollback %s: %v %v", internal, rolledBack, err)
				}
			}
			progress, e := db.Progress(ctx)
			if e != nil || len(progress) != 0 {
				t.Fatalf("rollback progress %v %v", progress, e)
			}
			if e = run("committed", false); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(handled, expected) {
				t.Fatalf("dispatch order %v want %v", handled, expected)
			}
			for _, id := range []uint64{1, 2} {
				if !reflect.DeepEqual(balanceTags[id], []string{"0x0", "0x1", "0x2"}) {
					t.Fatal("event-scoped client blocks", balanceTags)
				}
			}
			rows, e = backend.Query(ctx, "SELECT id FROM "+table+" ORDER BY ordinal")
			if e != nil {
				t.Fatal(e)
			}
			var got []string
			for _, row := range rows.Rows {
				got = append(got, row["id"].(string))
			}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("persisted order %v", got)
			}
			journal, e := backend.Query(ctx, "SELECT checkpoint, chain_id::text AS chain, row_data->>'id' AS id FROM "+schema.Quote(db.Namespace)+`."_ponder_go_journal" ORDER BY operation_id`)
			if e != nil {
				t.Fatal(e)
			}
			if len(journal.Rows) != 6 {
				t.Fatalf("global transaction journal %v", journal.Rows)
			}
			for i, row := range journal.Rows {
				if row["id"] != expected[i] {
					t.Fatal("journal insertion order mismatch", row, expected[i])
				}
				if row["checkpoint"] != maxCheckpoint || row["chain"] != fmt.Sprint(cp.ChainID) {
					t.Fatal("caller transaction checkpoint changed", row)
				}
			}

			committedEvent, err := backend.Query(ctx, "SELECT event_id,checkpoint,(operation_end-operation_boundary)::text AS operations FROM "+schema.Quote(db.Namespace)+`."_ponder_go_events"`)
			if err != nil || len(committedEvent.Rows) != 1 {
				t.Fatalf("committed event %v %v", committedEvent, err)
			}
			if row := committedEvent.Rows[0]; row["event_id"] != "committed" || row["checkpoint"] != maxCheckpoint || row["operations"] != "6" {
				t.Fatal("caller event/journal boundary mismatch", row)
			}
			progress, e = db.Progress(ctx)
			if e != nil || len(progress) != 1 || progress[fmt.Sprint(cp.ChainID)] != maxCheckpoint {
				t.Fatalf("caller checkpoint progress %v %v", progress, e)
			}
		})
	}
}
