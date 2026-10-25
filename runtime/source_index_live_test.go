package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type sourceIndexProviderRecord struct{ Request, Response any }
type sourceIndexProgressReplay struct {
	mu       sync.Mutex
	registry *historicalLiveRegistry
	records  []sourceIndexProviderRecord
	used     []bool
}

func (r *sourceIndexProgressReplay) BeginBlockByNumber(ctx context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) <-chan SourceSyncProgressBlockResult {
	future := make(chan SourceSyncProgressBlockResult, 1)
	block, err := r.GetBlockByNumber(ctx, number, transactions, options)
	future <- SourceSyncProgressBlockResult{Block: block, Err: err}
	close(future)
	return future
}
func (r *sourceIndexProgressReplay) GetBlockByNumber(ctx context.Context, number string, transactions bool, options SourceSyncProgressRequestOptions) (filter.Object, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, record := range r.records {
		if r.used[i] {
			continue
		}
		restored, err := r.registry.restore(record.Request)
		if err != nil {
			return nil, err
		}
		list, err := historicalLiveList(restored)
		if err != nil || len(list) != 2 {
			return nil, fmt.Errorf("source request context missing")
		}
		request := list[0].(filter.Object)
		params, err := historicalLiveList(request["params"])
		if err != nil {
			return nil, err
		}
		if request["method"] != "eth_getBlockByNumber" || len(params) != 2 || params[0] != number || params[1] != transactions {
			continue
		}
		contextOptions := list[1].(filter.Object)
		if contextOptions["retryNullBlockRequest"] != options.RetryNullBlockRequest {
			return nil, fmt.Errorf("retry-null request option differs")
		}
		value, err := r.registry.restore(record.Response)
		if err != nil {
			return nil, err
		}
		r.used[i] = true
		return rpc.StandardizeSourceBlock(value.(filter.Object), rpc.ActionRequest{Method: "eth_getBlockByNumber", Params: params})
	}
	return nil, fmt.Errorf("unrecorded progress request %s", number)
}
func sourceIndexOptional(value filter.Object) any {
	if value == nil {
		return filter.Undefined
	}
	return value
}
func sourceIndexRows(values []filter.Object) []any {
	out := []any{}
	for _, v := range values {
		out = append(out, v)
	}
	return out
}

type sourceIndexLiveCall struct {
	Operation           string
	Args, Result, After any
	Providers           []sourceIndexProviderRecord
	Generators          [][]any
	Results             []any
	Steps               []struct {
		Index     int
		Operation string
		Args      any
	}
}

func sourceIndexStoreCall(ctx context.Context, source *store.SourceSyncStore, operation string, args filter.Object) (any, error) {
	chainNumber, err := historicalLiveNumber(args["chainId"])
	if err != nil {
		return nil, err
	}
	chain := uint64(chainNumber)
	switch operation {
	case "store.insertBlocks", "store.insertTransactions", "store.insertLogs":
		key := map[string]string{"store.insertBlocks": "blocks", "store.insertTransactions": "transactions", "store.insertLogs": "logs"}[operation]
		rows, err := historicalLiveObjects(args[key])
		if err != nil {
			return nil, err
		}
		switch operation {
		case "store.insertBlocks":
			err = source.InsertBlocks(ctx, chain, rows)
		case "store.insertTransactions":
			err = source.InsertTransactions(ctx, chain, rows)
		case "store.insertLogs":
			err = source.InsertLogs(ctx, chain, rows)
		}
		return filter.Undefined, err
	case "store.insertChildAddresses":
		input, ok := args["childAddresses"].(historicalLiveMap)
		if !ok {
			return nil, fmt.Errorf("child address Map missing")
		}
		children := []store.SourceFactoryChildren{}
		for _, entry := range input {
			item := store.SourceFactoryChildren{FragmentID: entry.Key.(string), Children: []store.SourceChildAddress{}}
			for _, child := range entry.Value.(historicalLiveMap) {
				number, err := historicalLiveNumber(child.Value)
				if err != nil {
					return nil, err
				}
				item.Children = append(item.Children, store.SourceChildAddress{Address: child.Key.(string), BlockNumber: uint64(number)})
			}
			children = append(children, item)
		}
		return filter.Undefined, source.InsertChildAddresses(ctx, chain, children)
	case "store.getEventData":
		filters, err := historicalLiveObjects(args["filters"])
		if err != nil {
			return nil, err
		}
		from, err := historicalLiveNumber(args["fromBlock"])
		if err != nil {
			return nil, err
		}
		to, err := historicalLiveNumber(args["toBlock"])
		if err != nil {
			return nil, err
		}
		limit, err := historicalLiveNumber(args["limit"])
		if err != nil {
			return nil, err
		}
		data, err := source.GetEventData(ctx, store.SourceEventDataArgs{Filters: filters, FromBlock: from, ToBlock: to, ChainID: chain, Limit: int(limit)})
		if err != nil {
			return nil, err
		}
		return filter.Object{"blocks": sourceIndexRows(data.Blocks), "transactions": sourceIndexRows(data.Transactions), "transactionReceipts": sourceIndexRows(data.TransactionReceipts), "logs": sourceIndexRows(data.Logs), "traces": sourceIndexRows(data.Traces), "cursor": data.Cursor}, nil
	}
	return nil, fmt.Errorf("unknown index source store operation %s", operation)
}

type sourceIndexLiveCase struct {
	ID, Title  string
	SourceFile string `json:"source_file"`
	SourceSHA  string `json:"source_sha256"`
	TestSource string `json:"test_source"`
	Assertions []json.RawMessage
	Calls      []sourceIndexLiveCall
}

func sourceIndexValidateCases(cases, ledger []sourceIndexLiveCase) error {
	expected := map[string][]string{
		"f2217665390b66eeefdf": {"getLocalSyncProgress"}, "902707265a2f99df393f": {"getLocalSyncProgress"},
		"928495e24f9b9eec3013": {"getCachedBlock"}, "c429e0be63d1e2a5621c": {"getCachedBlock", "getCachedBlock"}, "4096c895c425e67b73c8": {"getCachedBlock"}, "0ad0d344d7cb00ef36b7": {"getCachedBlock", "getCachedBlock", "getCachedBlock"}, "02613908644ac30deaea": {"getCachedBlock", "getCachedBlock"},
		"cfc3d65d78ce2707a366": {"getRequiredIntervals", "getRequiredIntervals", "getRequiredIntervals"}, "f6920d1d20a903b4f5a4": {"getRequiredIntervalsWithFilters", "getRequiredIntervalsWithFilters"}, "1469b5672695375f7fe2": {"getRequiredIntervalsWithFilters"}, "34b296bcde7be28a3324": {"getRequiredIntervalsWithFilters"}, "82f711b0ecef2ba1a7c9": {"getRequiredIntervals", "getRequiredIntervals"},
		"489c3d1a0dd13ea3e901": {"mergeAsyncGeneratorsWithEventOrder"},
		"d083d94dc3d8e4ae3d9e": {"store.insertBlocks", "store.insertTransactions", "store.insertLogs", "store.getEventData", "syncLogToInternal"},
		"c4cb917df935980a68d3": {"store.insertChildAddresses", "getChildAddresses"},
	}
	originals := map[string]sourceIndexLiveCase{}
	for _, entry := range ledger {
		if entry.SourceFile == "packages/core/src/runtime/index.test.ts" {
			originals[entry.ID] = entry
		}
	}
	if len(cases) != 15 || len(originals) != 15 {
		return fmt.Errorf("all fifteen original cases required")
	}
	assertions, calls := 0, 0
	seen := map[string]bool{}
	for _, record := range cases {
		entry, ok := originals[record.ID]
		if !ok || seen[record.ID] {
			return fmt.Errorf("unknown or duplicate original case %s", record.ID)
		}
		seen[record.ID] = true
		if record.Title != entry.Title || record.SourceFile != entry.SourceFile || record.SourceSHA != entry.SourceSHA || record.TestSource != entry.TestSource || len(record.Assertions) != len(entry.Assertions) {
			return fmt.Errorf("original source body/metadata differs for %s", record.ID)
		}
		for i, a := range entry.Assertions {
			var source, native any
			if json.Unmarshal(a, &source) != nil || json.Unmarshal(record.Assertions[i], &native) != nil {
				return fmt.Errorf("invalid original assertion")
			}
			left, _ := json.Marshal(source)
			right, _ := json.Marshal(native)
			if !bytes.Equal(left, right) {
				return fmt.Errorf("original assertion differs")
			}
		}
		operations := expected[record.ID]
		if len(record.Calls) != len(operations) {
			return fmt.Errorf("original call count differs for %s", record.ID)
		}
		for i, call := range record.Calls {
			if call.Operation != operations[i] {
				return fmt.Errorf("original operation sequence differs for %s", record.ID)
			}
			if call.Operation == "getLocalSyncProgress" && len(call.Providers) != 1 {
				return fmt.Errorf("original provider request missing")
			}
			if call.Operation == "mergeAsyncGeneratorsWithEventOrder" && (len(call.Generators) != 2 || len(call.Generators[0]) != 3 || len(call.Generators[1]) != 3 || len(call.Results) != 3 || len(call.Steps) != 6) {
				return fmt.Errorf("original merge input/next/output census differs")
			}
		}
		assertions += len(record.Assertions)
		calls += len(record.Calls)
	}
	if assertions != 30 || calls != 28 {
		return fmt.Errorf("original thirty assertions/twenty-eight calls required")
	}
	return nil
}

// All records originate in the unchanged original fifteen test bodies. The
// explicit-loopback startup adaptation is recorded separately, not pristine.
func TestSourceRuntimeIndexFullFile(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/source-runtime-index-live.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases        []sourceIndexLiveCase
		BackendCases map[string][]sourceIndexLiveCase `json:"backend_cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	ledgerRaw, err := os.ReadFile("../parity/test-ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct{ Tests []sourceIndexLiveCase }
	if err = json.Unmarshal(ledgerRaw, &ledger); err != nil {
		t.Fatal(err)
	}
	if err = sourceIndexValidateCases(fixture.Cases, ledger.Tests); err != nil {
		t.Fatal(err)
	}
	if len(fixture.BackendCases) != 2 {
		t.Fatal("both complete source backend captures required")
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			cases := fixture.BackendCases[dialect]
			if err := sourceIndexValidateCases(cases, ledger.Tests); err != nil {
				t.Fatal(err)
			}
			for _, c := range cases {
				t.Run(c.Title, func(t *testing.T) {
					ctx := context.Background()
					registry := newSourceLiveRegistry()
					var source *store.SourceSyncStore
					ensureStore := func() *store.SourceSyncStore {
						if source != nil {
							return source
						}
						if dialect == "postgres" && os.Getenv("PONDER_TEST_POSTGRES") == "" {
							t.Fatal("explicit task PostgreSQL required")
						}
						backend := stagedBackend(t, ctx, dialect, "memory://")
						t.Cleanup(func() { backend.Close() })
						db, err := store.New(ctx, backend, schema.Schema{Tables: []schema.Table{{Name: "index_probe", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}, fmt.Sprintf("ponder_go_index_%x", time.Now().UnixNano()), schema.Multichain)
						if err != nil {
							t.Fatal(err)
						}
						cache, err := db.EnableProtocolCache(ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							cache.Close()
							for _, namespace := range []string{cache.Namespace(), db.SyncNamespace, db.Namespace} {
								if err := backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(namespace)+" CASCADE"); err != nil {
									t.Errorf("remove owned runtime index test schema: %v", err)
								}
							}
						})
						source, err = cache.SourceSyncStore(0)
						if err != nil {
							t.Fatal(err)
						}
						return source
					}
					for index, call := range c.Calls {
						if call.Operation == "mergeAsyncGeneratorsWithEventOrder" {
							if err := sourceIndexReplayMerge(ctx, registry, call); err != nil {
								t.Fatal(err)
							}
							continue
						}
						restored, err := registry.restore(call.Args)
						if err != nil {
							t.Fatal(err)
						}
						args, ok := restored.(filter.Object)
						if strings.HasPrefix(call.Operation, "store.") {
							list, err := historicalLiveList(restored)
							if err != nil || len(list) != 1 {
								t.Fatal("store argument required")
							}
							args, ok = list[0].(filter.Object)
						}
						if !ok {
							t.Fatal("object argument required")
						}
						var result any
						switch call.Operation {
						case "getCachedBlock", "getRequiredIntervals", "getRequiredIntervalsWithFilters":
							result, err = sourceIndexReplayPure(call.Operation, args)
						case "getLocalSyncProgress":
							filters, e := historicalLiveObjects(args["filters"])
							if e != nil {
								t.Fatal(e)
							}
							cached, e := sourceIndexPureCache(args["cachedIntervals"])
							if e != nil {
								t.Fatal(e)
							}
							chain, e := historicalLiveNumber(args["chainID"])
							if e != nil {
								t.Fatal(e)
							}
							replay := &sourceIndexProgressReplay{registry: registry, records: call.Providers, used: make([]bool, len(call.Providers))}
							progress, e := GetLocalSourceSyncProgress(ctx, uint64(chain), replay, filters, args["finalizedBlock"].(filter.Object), cached)
							err = e
							if err == nil {
								result = filter.Object{"start": progress.Start, "end": sourceIndexOptional(progress.End), "current": sourceIndexOptional(progress.Current), "finalized": progress.Finalized}
							}
							for _, used := range replay.used {
								if !used {
									t.Fatal("source progress provider request omitted")
								}
							}
						case "getChildAddresses":
							filters, e := historicalLiveObjects(args["filters"])
							if e != nil {
								t.Fatal(e)
							}
							children, e := GetSourceChildAddresses(ctx, args["cacheRpcRequests"] == true, filters, ensureStore())
							err = e
							if err == nil {
								pairs := historicalLiveMap{}
								for _, item := range children.Snapshot() {
									addresses := historicalLiveMap{}
									for _, address := range item.Entries {
										addresses = append(addresses, historicalLivePair{Key: address.Address, Value: float64(address.Block)})
									}
									pairs = append(pairs, historicalLivePair{Key: item.ID, Value: addresses})
								}
								result = pairs
							}
						case "syncLogToInternal":
							result, err = protocol.SyncLogToInternal(args["log"].(filter.Object))
						default:
							result, err = sourceIndexStoreCall(ctx, ensureStore(), call.Operation, args)
						}
						if err != nil {
							t.Fatalf("call %d %s: %v", index, call.Operation, err)
						}
						if err = registry.compare(result, call.Result, call.Operation+".result"); err != nil {
							t.Fatal(err)
						}
						if err = registry.compare(restored, call.After, call.Operation+".input_after"); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func sourceIndexReplayMerge(ctx context.Context, registry *historicalLiveRegistry, call sourceIndexLiveCall) error {
	generators := []SourceEventGenerator{}
	nextOrder := []int{}
	for generatorIndex, steps := range call.Generators {
		batches := []*SourceEventBatch{}
		for _, step := range steps {
			value, err := registry.restore(step)
			if err != nil {
				return err
			}
			record := value.(filter.Object)
			if record["done"] == true {
				continue
			}
			batch := record["value"].(filter.Object)
			events, err := historicalLiveObjects(batch["events"])
			if err != nil {
				return err
			}
			span, err := historicalLiveSpan(batch["blockRange"])
			if err != nil {
				return err
			}
			batches = append(batches, &SourceEventBatch{Events: events, Checkpoint: batch["checkpoint"].(string), BlockRange: span})
		}
		sequence := sourceMergeSequence(batches...)
		generators = append(generators, SourceEventGenerator{Next: func(ctx context.Context) <-chan SourceEventStep {
			nextOrder = append(nextOrder, generatorIndex)
			return sequence.Next(ctx)
		}})
	}
	count := 0
	for group, err := range MergeSourceEventsWithEventOrder(ctx, generators) {
		if err != nil {
			return err
		}
		if count >= len(call.Results) {
			return fmt.Errorf("extra native merge group")
		}
		values := []any{}
		for _, item := range group {
			values = append(values, filter.Object{"chainId": float64(item.ChainID), "events": sourceIndexRows(item.Events), "checkpoint": item.Checkpoint, "blockRange": []any{item.BlockRange[0], item.BlockRange[1]}})
		}
		if err = registry.compare(values, call.Results[count], "merge.result"); err != nil {
			return err
		}
		count++
	}
	if len(nextOrder) != len(call.Steps) {
		return fmt.Errorf("merge next count differs")
	}
	for i, step := range call.Steps {
		args, ok := step.Args.([]any)
		if !ok || len(args) != 0 || step.Operation != "next" || step.Index != nextOrder[i] {
			return fmt.Errorf("merge next order differs at %d", i)
		}
	}
	if count != len(call.Results) {
		return fmt.Errorf("native merge omitted groups")
	}
	return nil
}

// Fail-closed fixture validation is supplemental evidence, not source credit.
func TestSourceRuntimeIndexEvidenceCompleteness(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/source-runtime-index-live.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Cases []sourceIndexLiveCase }
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../parity/test-ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct{ Tests []sourceIndexLiveCase }
	if err = json.Unmarshal(raw, &ledger); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func([]sourceIndexLiveCase) []sourceIndexLiveCase{
		"missing_case":  func(c []sourceIndexLiveCase) []sourceIndexLiveCase { return c[:14] },
		"missing_calls": func(c []sourceIndexLiveCase) []sourceIndexLiveCase { c[0].Calls = nil; return c },
		"wrong_operation": func(c []sourceIndexLiveCase) []sourceIndexLiveCase {
			c[0].Calls[0].Operation = "getCachedBlock"
			return c
		},
		"changed_source_body": func(c []sourceIndexLiveCase) []sourceIndexLiveCase { c[0].TestSource += " "; return c },
		"changed_source_hash": func(c []sourceIndexLiveCase) []sourceIndexLiveCase { c[0].SourceSHA = "wrong"; return c },
		"missing_provider":    func(c []sourceIndexLiveCase) []sourceIndexLiveCase { c[0].Calls[0].Providers = nil; return c },
		"missing_merge_steps": func(c []sourceIndexLiveCase) []sourceIndexLiveCase {
			for i := range c {
				if c[i].ID == "489c3d1a0dd13ea3e901" {
					c[i].Calls[0].Steps = nil
				}
			}
			return c
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(fixture.Cases)
			if err != nil {
				t.Fatal(err)
			}
			var copy []sourceIndexLiveCase
			if err = json.Unmarshal(data, &copy); err != nil {
				t.Fatal(err)
			}
			if err = sourceIndexValidateCases(mutate(copy), ledger.Tests); err == nil {
				t.Fatal("incomplete/changed source evidence accepted")
			}
		})
	}
}
