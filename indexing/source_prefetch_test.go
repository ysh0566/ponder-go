package indexing

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"sort"
	"sync"
	"testing"
	"time"
)

// This observer forwards actual standalone read-job admission synchronously;
// its completion log does not substitute a goroutine-start handshake.
type profileCacheObserver struct{ *probeCacheObserver }

func (c *profileCacheObserver) BeginCacheGet(ctx context.Context, key string) <-chan rpc.CacheLookupResult {
	completion := c.cache.BeginCacheGet(ctx, key)
	out := make(chan rpc.CacheLookupResult, 1)
	go func() {
		result := <-completion
		c.record(Object{"operation": "get", "key": key, "hit": result.Hit, "result": string(result.Value)})
		out <- result
		close(out)
	}()
	return out
}

type prefetchCountingRequester struct {
	rpc       *rpc.Client
	mu        sync.Mutex
	operation int
	calls     []Object
}

func (r *prefetchCountingRequester) record(method string, params any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, Object{"request": Object{"method": method, "params": params}, "operation_index": r.operation})
}
func (r *prefetchCountingRequester) Call(ctx context.Context, m string, p, out any) error {
	r.record(m, p)
	return r.rpc.Call(ctx, m, p, out)
}
func (r *prefetchCountingRequester) BeginCall(ctx context.Context, m string, p, out any) <-chan error {
	r.record(m, p)
	return r.rpc.BeginCall(ctx, m, p, out)
}
func profileCounts(t *testing.T, value any) map[string]uint64 {
	t.Helper()
	counts := map[string]uint64{}
	for k, v := range indexingObject(value) {
		n, e := clientInteger(v)
		if e != nil || !n.IsUint64() {
			t.Fatal("invalid actual eventCount", k, v)
		}
		counts[k] = n.Uint64()
	}
	return counts
}
func TestSourceIndexingPrefetch(t *testing.T) {
	raw, err := os.ReadFile("../testdata/indexing/source-indexing-profile.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	r, e := gzip.NewReader(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(r)
	r.Close()
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		BackendCases map[string][]Object `json:"backend_cases"`
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	observations := []Object{}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			count := 0
			for _, record := range fixture.BackendCases[backend] {
				id, _ := record["id"].(string)
				if id != "ff30deb8b9aca842519b" && id != "4ace7b36bca44245e4b7" {
					continue
				}
				count++
				var observation Object
				pass := t.Run(id, func(t *testing.T) { observation = replaySourcePrefetch(t, backend, record) })
				if observation != nil {
					observation["pass"] = pass
					observations = append(observations, observation)
				}
			}
			if count != 2 {
				t.Fatal("exact two original prefetch cases required")
			}
		})
	}
	appendSourceProfileArtifact(t, "PONDER_INDEXING_PROFILE_NATIVE_OUTPUT", raw, observations)
}
func replaySourcePrefetch(t *testing.T, backend string, record Object) Object {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := indexingReplayStore(t, backend)
	cache, err := db.EnableProtocolCache(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cache.Close()
		_ = db.Backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(cache.Namespace())+" CASCADE")
	})
	clientsInput := indexingObjects(record["clients"])
	if len(clientsInput) != 1 {
		t.Fatal("original one client")
	}
	chains := indexingObjects(clientsInput[0]["chains"])
	if len(chains) != 1 {
		t.Fatal("original one chain")
	}
	id, err := clientInteger(chains[0]["id"])
	if err != nil {
		t.Fatal(err)
	}
	providers := indexingObjects(record["providers"])
	provider := &indexingReplayProvider{t: t}
	for _, p := range providers {
		provider.expected = append(provider.expected, Object{"request": p["request"], "response": p["response"]})
	}
	server := httptest.NewServer(http.HandlerFunc(provider.serve))
	defer server.Close()
	transport := rpc.New(server.URL)
	transport.Retries = 0
	transport.RequestsPerSecond = 0
	requester := &prefetchCountingRequester{rpc: transport}
	adapter := cache.AsyncSourceRPCRequestCache(id.Uint64())
	defer adapter.Close(context.Background())
	observed := &profileCacheObserver{&probeCacheObserver{cache: adapter}}
	clients, err := NewStrictClientSet(chains, []rpc.Requester{requester}, observed)
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(context.Background())
	client, err := clients.GetClient(chains[0])
	if err != nil {
		t.Fatal(err)
	}
	clients.SetEventCounts(profileCounts(t, clientsInput[0]["eventCount"]))
	steps := []Object{}
	storeCalls := []Object{}
	results := []any{}
	for i, operation := range indexingObjects(record["operations"]) {
		requester.mu.Lock()
		requester.operation = i
		beforeRPC := len(requester.calls)
		requester.mu.Unlock()
		observed.mu.Lock()
		beforeDB := len(observed.calls)
		observed.mu.Unlock()
		clients.SetEventCounts(profileCounts(t, operation["eventCount"]))
		var result any
		switch operation["operation"] {
		case "setEvent":
			clients.SetEvent(indexingObject(indexingUnmark(t, operation["event"])))
		case "readContract":
			args := indexingObject(indexingUnmark(t, operation["args"]))
			call := sourceActionContract(t, args)
			call.ABIValue = args["abi"]
			result, err = client.ReadContract(ctx, call)
			if err != nil {
				t.Fatal(err)
			}
			indexingEqual(t, "original actual returned supply", result, indexingUnmark(t, operation["result"]))
			if result.(*big.Int).Cmp(big.NewInt(1000000000000000000)) != 0 {
				t.Fatal("original parseEther(1) assertion")
			}
			results = append(results, result)
		case "prefetch":
			err = clients.Prefetch(ctx, indexingObjects(indexingUnmark(t, operation["events"])))
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("unexpected original operation", operation["operation"])
		}
		// This only joins persistent writes, never admitted RPC prefetch. It gives
		// the independent standalone DB cache its documented settled boundary.
		if err = clients.WaitCacheWrites(ctx); err != nil {
			t.Fatal(err)
		}
		requester.mu.Lock()
		afterRPC := len(requester.calls)
		requester.mu.Unlock()
		observed.mu.Lock()
		actualDB := append([]Object{}, observed.calls[beforeDB:]...)
		observed.mu.Unlock()
		for _, call := range actualDB {
			storeCalls = append(storeCalls, Object{"operation_index": i, "call": call})
		}
		if i == 4 {
			if afterRPC-beforeRPC != 0 {
				t.Fatal("original zero RPC spy assertion")
			}
			for _, call := range actualDB {
				if call["operation"] == "get" {
					t.Fatal("original zero get spy assertion")
				}
			}
		}
		steps = append(steps, Object{"index": i, "operation": operation["operation"], "result": result, "rpc_admissions": afterRPC - beforeRPC, "cache_calls": actualDB})
	}
	if err = clients.WaitPrefetch(ctx); err != nil {
		t.Fatal(err)
	}
	if err = clients.WaitCacheWrites(ctx); err != nil {
		t.Fatal(err)
	}
	requester.mu.Lock()
	calls := append([]Object{}, requester.calls...)
	requester.mu.Unlock()
	expectedCalls := []Object{}
	for _, p := range providers {
		expectedCalls = append(expectedCalls, Object{"request": p["request"], "operation_index": p["operation_index"]})
	}
	indexingEqual(t, "strict original RPC admission tape", calls, expectedCalls)
	provider.mu.Lock()
	actualTape := append([]Object{}, provider.calls...)
	provider.mu.Unlock()
	expectedRequests := []Object{}
	for _, p := range providers {
		expectedRequests = append(expectedRequests, indexingObject(p["request"]))
	}
	indexingEqual(t, "strict original raw transport tape", actualTape, expectedRequests)
	disabled := record["id"] == "4ace7b36bca44245e4b7"
	checked := 4
	if disabled {
		checked = 5
		if len(storeCalls) != 0 {
			t.Fatal("original disabled cache insert/get spy assertions")
		}
	}
	if len(record["assertions"].([]any)) != checked {
		t.Fatal("original assertion census")
	}
	rows, err := db.Backend.Query(ctx, "SELECT request_hash,chain_id::text AS chain_id,block_number::text AS block_number,result FROM "+schema.Quote(cache.Namespace())+".rpc_request_results ORDER BY request_hash")
	if err != nil {
		t.Fatal(err)
	}
	physical := []Object{}
	for _, row := range rows.Rows {
		chain, _ := new(big.Int).SetString(fmt.Sprint(row["chain_id"]), 10)
		var block any
		if row["block_number"] != nil {
			block, _ = new(big.Int).SetString(fmt.Sprint(row["block_number"]), 10)
		}
		physical = append(physical, Object{"requestHash": row["request_hash"], "chainId": chain, "blockNumber": block, "result": row["result"]})
	}
	expectedRows := indexingObjects(indexingUnmark(t, record["physical_after"]))
	sort.Slice(expectedRows, func(i, j int) bool {
		return expectedRows[i]["requestHash"].(string) < expectedRows[j]["requestHash"].(string)
	})
	indexingEqual(t, "original actual persistent cache rows", physical, expectedRows)
	if len(results) != 2 {
		t.Fatal("original two actual action results")
	}
	digest := indexingCanonical(Object{"steps": steps, "providers": actualTape, "admissions": calls, "physical_after": physical})
	b, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return Object{"backend": backend, "id": record["id"], "name": record["full_name"], "source_assertions_checked": checked, "digest_input": digest, "observation_sha256": hex.EncodeToString(sum[:]), "rpc_calls": len(calls), "cache_rows": len(physical), "operations": len(steps)}
}
