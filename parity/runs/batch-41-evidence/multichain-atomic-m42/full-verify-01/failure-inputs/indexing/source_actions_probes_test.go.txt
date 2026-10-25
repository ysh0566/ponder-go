package indexing

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	ponderabi "ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// These supplemental probes execute action inputs against fresh real database
// state. Source provider bytes are the only response seeds. Source cache rows,
// action outputs and errors are exclusively comparison targets.
func TestSourceIndexingActionsProbes(t *testing.T) {
	raw, err := os.ReadFile("../testdata/indexing/source-indexing-actions-probes.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		BackendCases map[string][]Object `json:"backend_cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	backends := Object{}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			cases := fixture.BackendCases[backend]
			if len(cases) != 1 {
				t.Fatal("exact single real source probe required")
			}
			backends[backend] = replayIndexingActionProbe(t, backend, cases[0])
		})
	}
	if t.Failed() {
		return
	}
	if output := os.Getenv("PONDER_INDEXING_ACTIONS_PROBES_NATIVE_OUTPUT"); output != "" {
		if len(backends) != 2 {
			t.Fatal("both real backends required for evidence")
		}
		result := Object{"complete": true, "success": true, "source_credit": 0, "real_database": true, "fixture_sha256": hex.EncodeToString(sum[:]), "fixture": "testdata/indexing/source-indexing-actions-probes.json.gz", "error_equivalence": "typed contract revert and zero-data causes; source viem presentation/custom-error decoding not byte-equivalent", "backends": backends}
		data, err = json.MarshalIndent(result, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(output, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

type probeCacheObserver struct {
	cache *store.AsyncSourceRPCRequestCache
	mu    sync.Mutex
	calls []Object
}

func (c *probeCacheObserver) record(v Object) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, v)
}
func (c *probeCacheObserver) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	v, hit, e := c.cache.CacheGet(ctx, key)
	c.record(Object{"operation": "get", "key": key, "hit": hit, "result": string(v)})
	return v, hit, e
}
func (c *probeCacheObserver) CachePut(ctx context.Context, key string, v json.RawMessage) error {
	return c.cache.CachePut(ctx, key, v)
}
func (c *probeCacheObserver) BeginCachePut(ctx context.Context, key string, v json.RawMessage) <-chan error {
	future := c.cache.BeginCachePut(ctx, key, v)
	c.record(Object{"operation": "admitted", "key": key, "result": string(v)})
	return future
}

func replayIndexingActionProbe(t *testing.T, backend string, record Object) Object {
	t.Helper()
	ctx := context.Background()
	db := indexingReplayStore(t, backend)
	cache, err := db.EnableProtocolCache(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(cache.Namespace())+" CASCADE")
	})
	chain := indexingObject(indexingUnmark(t, record["chain"]))
	persistent := cache.AsyncSourceRPCRequestCache(1)
	defer persistent.Close(ctx)
	observed := &probeCacheObserver{cache: persistent}
	provider := &indexingReplayProvider{t: t}
	server := httptest.NewServer(http.HandlerFunc(provider.serve))
	defer server.Close()
	requester := rpc.New(server.URL)
	requester.Retries = 0
	requester.RequestsPerSecond = 0
	clients, err := NewStrictClientSet([]Object{chain}, []rpc.Requester{requester}, observed)
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(ctx)
	event := indexingObject(indexingUnmark(t, record["event"]))
	clients.SetEvent(Object{"type": event["type"], "event": Object{"block": Object{"number": event["block"]}}})
	client, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	actions := record["actions"].([]any)
	if len(actions) != 11 {
		t.Fatal("eleven original supplemental inputs required")
	}
	steps := []Object{}
	totalProviders := 0
	for i, value := range actions {
		action := indexingObject(value)
		op := action["operation"].(string)
		providers := indexingObjects(action["providers"])
		provider.mu.Lock()
		provider.expected = providers
		provider.calls = nil
		provider.mu.Unlock()
		observed.mu.Lock()
		observed.calls = nil
		observed.mu.Unlock()
		args := indexingObject(indexingUnmark(t, action["args"].([]any)[0]))
		result, callErr := probeInvoke(t, ctx, client, op, args)
		if err = clients.WaitCacheWrites(ctx); err != nil {
			t.Fatal(err)
		}
		var outcome any
		if action["error"] != nil {
			if callErr == nil {
				t.Fatalf("step %d expected actual contract revert", i)
			}
			outcome = probeError(t, callErr)
			indexingEqual(t, fmt.Sprintf("step %d outer error cause", i), outcome, probeExpected(t, action["error"]))
			expectedError := indexingObject(indexingObject(action["error"])["$error"])
			if expectedError["name"] != "ContractFunctionExecutionError" || expectedError["cause_name"] != "ContractFunctionRevertedError" {
				t.Fatal("unexpected source outer error", expectedError)
			}
		} else {
			if callErr != nil {
				t.Fatalf("step %d: %v", i, callErr)
			}
			outcome = probeResult(t, result)
			indexingEqual(t, fmt.Sprintf("step %d result", i), outcome, probeExpected(t, action["result"]))
		}
		provider.mu.Lock()
		requests := append([]Object{}, provider.calls...)
		provider.mu.Unlock()
		if len(requests) != len(providers) {
			t.Fatalf("step %d providers got%d want%d", i, len(requests), len(providers))
		}
		totalProviders += len(requests)
		rows := probePhysicalRows(t, ctx, db, cache)
		expectedRows := indexingObjects(action["physical_after"])
		sort.Slice(expectedRows, func(i, j int) bool {
			return expectedRows[i]["requestHash"].(string) < expectedRows[j]["requestHash"].(string)
		})
		indexingEqual(t, fmt.Sprintf("step %d persisted rows", i), rows, expectedRows)
		observed.mu.Lock()
		cacheCalls := append([]Object{}, observed.calls...)
		observed.mu.Unlock()
		step := Object{"index": i, "operation": op, "provider_count": len(requests), "requests": requests, "result_or_error": outcome, "physical_rows": rows, "cache_observations": cacheCalls}
		b, _ := json.Marshal(indexingCanonical(step))
		digest := sha256.Sum256(b)
		steps = append(steps, Object{"index": i, "operation": op, "provider_count": len(requests), "digest_input": indexingCanonical(step), "observation_sha256": hex.EncodeToString(digest[:])})
	}
	if totalProviders != 10 {
		t.Fatal("exact10 actual probe requests required", totalProviders)
	}
	return Object{"actions": len(actions), "providers": totalProviders, "steps": steps}
}
func probeContract(t *testing.T, input Object) ContractCall {
	t.Helper()
	raw, e := json.Marshal(input["abi"])
	if e != nil {
		t.Fatal(e)
	}
	abi, e := ponderabi.Parse(string(raw))
	if e != nil {
		t.Fatal(e)
	}
	args, _ := input["args"].([]any)
	return ContractCall{ABI: abi, Address: input["address"].(string), FunctionName: input["functionName"].(string), Args: args}
}
func probeInvoke(t *testing.T, ctx context.Context, c *Client, op string, args Object) (any, error) {
	t.Helper()
	options := BlockOptions{}
	if n, ok := args["blockNumber"].(*big.Int); ok {
		options.BlockNumber = n
	}
	options.Cache, _ = args["cache"].(string)
	switch op {
	case "readContract":
		return c.ReadContract(ctx, probeContract(t, args), ReadContractOptions{BlockOptions: options})
	case "getCode":
		v, e := c.GetCode(ctx, args["address"].(string), options)
		if v == nil {
			return filter.Undefined, e
		}
		return *v, e
	case "multicall":
		contracts := []ContractCall{}
		for _, v := range args["contracts"].([]any) {
			contracts = append(contracts, probeContract(t, indexingObject(v)))
		}
		allow := args["allowFailure"].(bool)
		var batchSize *int
		if n, ok := args["batchSize"].(float64); ok {
			size := int(n)
			batchSize = &size
		}
		return c.Multicall(ctx, contracts, MulticallOptions{BlockOptions: options, MulticallAddress: args["multicallAddress"].(string), AllowFailure: &allow, BatchSize: batchSize})
	default:
		t.Fatal("unknown probe action", op)
		return nil, nil
	}
}
func probeError(t *testing.T, e error) Object {
	var zero *ContractFunctionZeroDataError
	if errors.As(e, &zero) {
		if zero.FunctionName != "balanceOf" {
			t.Fatal("unexpected zero-data function", zero.FunctionName)
		}
		return Object{"$error": "contract_zero_data"}
	}
	t.Helper()
	var revert *ContractCallRevertedError
	if !errors.As(e, &revert) || revert.FunctionName != "missing" || revert.Data != "0x" {
		t.Fatalf("expected genuine missing() aggregate revert, got %T %v", e, e)
	}
	return Object{"$error": "contract_revert"}
}
func probeResult(t *testing.T, v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = probeResult(t, v)
		}
		return out
	case MulticallResult:
		if x.Status == "failure" {
			if x.Result != nil {
				t.Fatalf("failed call must not have a success result: %#v", x.Result)
			}
			return Object{"status": x.Status, "result": Object{"$undefined": true}, "error": probeError(t, x.Error)}
		}
		return Object{"status": x.Status, "result": probeResult(t, x.Result)}
	default:
		return indexingCanonical(v)
	}
}
func probeExpected(t *testing.T, v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = probeExpected(t, v)
		}
		return out
	case map[string]any:
		if e, ok := x["$error"].(map[string]any); ok {
			if e["name"] != "ContractFunctionExecutionError" {
				t.Fatal("unknown source child error", e)
			}
			switch e["cause_name"] {
			case "ContractFunctionRevertedError":
				return Object{"$error": "contract_revert"}
			case "ContractFunctionZeroDataError":
				if !strings.Contains(fmt.Sprint(e["short_message"]), "returned no data") {
					t.Fatal("missing source zero-data message", e)
				}
				return Object{"$error": "contract_zero_data"}
			default:
				t.Fatal("unsupported source error cause", e)
				return nil
			}
		}
		out := Object{}
		for k, v := range x {
			out[k] = probeExpected(t, v)
		}
		return out
	default:
		return v
	}
}
func probePhysicalRows(t *testing.T, ctx context.Context, db *store.Store, cache *store.ProtocolCache) []Object {
	t.Helper()
	result, err := db.Backend.Query(ctx, "SELECT request_hash, chain_id::text AS chain_id, block_number::text AS block_number, result FROM "+schema.Quote(cache.Namespace())+".rpc_request_results ORDER BY request_hash")
	if err != nil {
		t.Fatal(err)
	}
	rows := []Object{}
	for _, r := range result.Rows {
		var block any
		if r["block_number"] != nil {
			block = Object{"$bigint": fmt.Sprint(r["block_number"])}
		}
		rows = append(rows, Object{"requestHash": r["request_hash"], "chainId": Object{"$bigint": fmt.Sprint(r["chain_id"])}, "blockNumber": block, "result": r["result"]})
	}
	return rows
}
