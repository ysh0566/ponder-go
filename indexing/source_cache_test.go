package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

type sourceCacheTimeline struct {
	mu      sync.Mutex
	entries []string
}

func (l *sourceCacheTimeline) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, s)
}
func (l *sourceCacheTimeline) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.entries...)
}

type sourceCacheGate struct {
	log                        *sourceCacheTimeline
	started, release, finished chan struct{}
	lateFailure                bool
	mu                         sync.Mutex
	calls                      []Object
}

func (c *sourceCacheGate) CacheGet(_ context.Context, key string) (json.RawMessage, bool, error) {
	c.log.add("cache_lookup")
	c.mu.Lock()
	c.calls = append(c.calls, Object{"operation": "get", "key": key})
	c.mu.Unlock()
	return nil, false, nil
}
func (c *sourceCacheGate) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	c.log.add("insert_begin")
	c.mu.Lock()
	c.calls = append(c.calls, Object{"operation": "insert", "key": key, "result": string(value)})
	c.mu.Unlock()
	close(c.started)
	defer close(c.finished)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.release:
	}
	if c.lateFailure {
		c.log.add("insert_reject")
		return errors.New("injected late cache insert failure")
	}
	c.log.add("insert_resolve")
	return nil
}

type sourceCacheRequester struct {
	rpc.Requester
	log *sourceCacheTimeline
}

func (r *sourceCacheRequester) Call(ctx context.Context, method string, params any, result any) error {
	r.log.add("rpc_begin")
	err := r.Requester.Call(ctx, method, params, result)
	if err == nil {
		r.log.add("rpc_return")
	}
	return err
}

type sourceCacheUserError struct{ name, message string }

func (e *sourceCacheUserError) Error() string     { return e.message }
func (e *sourceCacheUserError) ErrorName() string { return e.name }

// The source enters an async function's synchronous prefix immediately; Go may
// schedule CachePut after GetBalance has returned. We retain the actual native
// timeline, never sort it to imitate that JS initiation order. The shared
// contract checked here is completion independence and release/settlement
// causality, exact request/value semantics, and ignored late insertion errors.
func TestSourceIndexingServiceCache(t *testing.T) {
	data, e := os.ReadFile("../testdata/indexing/source-indexing-service-cache.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []Object `json:"cases"`
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.Cases) != 4 {
		t.Fatal("source cache/brand census changed")
	}
	for _, record := range fixture.Cases {
		record := record
		t.Run(record["name"].(string), func(t *testing.T) {
			name := record["name"].(string)
			if name == "delayed-cache-insert-success" || name == "delayed-cache-insert-failure" {
				replaySourceCacheGate(t, record)
				return
			}
			if name != "user-error-name-is-not-brand" && name != "actual-base-error-identity" {
				t.Fatal("unknown source control", name)
			}
			for _, backend := range []string{"postgres", "pglite"} {
				t.Run(backend, func(t *testing.T) { replaySourceCacheBrand(t, record, backend) })
			}
		})
	}
}
func replaySourceCacheGate(t *testing.T, record Object) {
	t.Helper()
	input := indexingObject(indexingUnmark(t, record["input"]))
	providers := record["providers"].([]any)
	if len(providers) != 1 {
		t.Fatal("expected exactly one provider")
	}
	provider := indexingObject(providers[0])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	log := &sourceCacheTimeline{}
	cache := &sourceCacheGate{log: log, started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}), lateFailure: input["lateFailure"].(bool)}
	var requestMu sync.Mutex
	requests := []Object{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Object
		if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		actual := Object{"method": request["method"], "params": request["params"]}
		requestMu.Lock()
		requests = append(requests, actual)
		requestMu.Unlock()
		_ = json.NewEncoder(w).Encode(Object{"jsonrpc": "2.0", "id": request["id"], "result": provider["response"]})
	}))
	defer server.Close()
	transport := rpc.New(server.URL)
	transport.Retries = 0
	transport.RequestsPerSecond = 0
	requester := &sourceCacheRequester{Requester: transport, log: log}
	chain := indexingObject(input["chain"])
	clients, e := NewClientSet([]Object{chain}, []rpc.Requester{requester}, cache)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		cancel()
		closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if e := clients.Close(closeCtx); e != nil {
			t.Error(e)
		}
	}()
	clients.SetEvent(indexingObject(input["event"]))
	client, e := clients.GetClient(chain)
	if e != nil {
		t.Fatal(e)
	}
	type result struct {
		balance *big.Int
		err     error
	}
	done := make(chan result, 1)
	go func() {
		balance, e := client.GetBalance(ctx, input["address"].(string))
		log.add("getBalance_return")
		done <- result{balance, e}
	}()
	var got result
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("successful getBalance waited for blocked cache insert")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	indexingEqual(t, "source balance", got.balance, indexingUnmark(t, record["result"]))
	select {
	case <-cache.started:
	case <-ctx.Done():
		t.Fatal("owned cache insertion never started")
	}
	select {
	case <-cache.finished:
		t.Fatal("cache insertion settled before release")
	default:
	}
	log.add("release_insert")
	close(cache.release)
	if e = clients.WaitCacheWrites(ctx); e != nil {
		t.Fatal("late insert error escaped successful getBalance", e)
	}
	select {
	case <-cache.finished:
	default:
		t.Fatal("WaitCacheWrites did not join insert")
	}
	requestMu.Lock()
	indexingEqual(t, "raw provider request", requests, []Object{indexingObject(provider["request"])})
	requestMu.Unlock()
	cache.mu.Lock()
	actualCalls := append([]Object{}, cache.calls...)
	cache.mu.Unlock()
	// The source fixture captures raw (possibly checksummed) syncStore request
	// arguments, NOT getCacheKey or persistent MD5 keys. Those source keys use
	// recursively ordered, lowercased JSON. Adapt only this documented native
	// envelope boundary: independently canonicalize params while retaining the
	// envelope's method spelling/chain, full values, result and event block tag.
	// The fixture and the exact raw provider assertion above remain untouched.
	expectedCalls := []Object{}
	for _, raw := range record["cacheCalls"].([]any) {
		call := indexingObject(raw)
		args := indexingObject(call["args"])
		batch := args["requests"].([]any)
		if len(batch) != 1 {
			t.Fatal("unexpected source cache batch")
		}
		entry := indexingObject(batch[0])
		request := entry
		if call["operation"] == "insert" {
			request = indexingObject(entry["request"])
			tag := request["params"].([]any)[1].(string)
			n, ok := new(big.Int).SetString(tag[2:], 16)
			if !ok || float64(n.Int64()) != entry["blockNumber"] {
				t.Fatal("source block number/tag disagree")
			}
		}
		paramsJSON, err := json.Marshal(request["params"])
		if err != nil {
			t.Fatal(err)
		}
		key, err := json.Marshal([]any{args["chainId"], request["method"], json.RawMessage(strings.ToLower(string(paramsJSON)))})
		if err != nil {
			t.Fatal(err)
		}
		expected := Object{"operation": call["operation"], "key": string(key)}
		if call["operation"] == "insert" {
			expected["result"] = entry["result"]
		}
		expectedCalls = append(expectedCalls, expected)
	}
	indexingEqual(t, "source cache calls", actualCalls, expectedCalls)
	timeline := log.snapshot()
	positions := map[string]int{}
	for i, label := range timeline {
		if _, exists := positions[label]; exists {
			t.Fatal("duplicate timeline event", label)
		}
		positions[label] = i
	}
	expectedTimeline := record["timeline"].([]any)
	if len(timeline) != len(expectedTimeline) {
		t.Fatal("timeline event count", timeline)
	}
	for _, label := range expectedTimeline {
		if _, ok := positions[label.(string)]; !ok {
			t.Fatal("missing timeline event", label)
		}
	}
	before := func(a, b string) {
		t.Helper()
		if positions[a] >= positions[b] {
			t.Fatalf("causality %s before %s violated: %v", a, b, timeline)
		}
	}
	before("cache_lookup", "rpc_begin")
	before("rpc_begin", "rpc_return")
	before("rpc_return", "getBalance_return")
	before("rpc_return", "insert_begin")
	before("getBalance_return", "release_insert")
	before("insert_begin", "release_insert")
	settled := "insert_resolve"
	if cache.lateFailure {
		settled = "insert_reject"
	}
	before("release_insert", settled)
	if record["settled"] != true {
		t.Fatal("source did not settle")
	}
	t.Logf("actual native timeline (not reordered): %v", timeline)
}
func replaySourceCacheBrand(t *testing.T, record Object, backend string) {
	t.Helper()
	input := indexingObject(record["input"])
	var thrown error = &sourceCacheUserError{name: input["name"].(string), message: input["message"].(string)}
	if input["isBaseError"] == true {
		thrown = &InvalidEventAccessError{Key: "transaction.gas", Message: input["message"].(string)}
	}
	s := adapterStore(t, backend)
	state := &ErrorState{}
	ctx := context.Background()
	chain := Object{"id": 1, "name": "mainnet", "cacheRpcRequests": true}
	requester := rpc.New("http://127.0.0.1:1")
	requester.Retries = 0
	clients, e := NewClientSet([]Object{chain}, []rpc.Requester{requester})
	if e != nil {
		t.Fatal(e)
	}
	defer clients.Close(ctx)
	e = s.Apply(ctx, adapterEvent("source-brand", 1), func(w *store.Writer) error {
		cb := Object{"name": "Erc20:setup", "chain": chain, "fn": Handler(func(context.Context, *HandlerArgs) error { return thrown })}
		service, e := NewService(Options{Build: Build{Chains: []Object{chain}, Contracts: []Object{{}}, SetupCallbacks: [][]Object{{cb}}, IndexingFunctions: []Object{{"name": "Erc20:setup"}}}, Database: NewDatabase(w.Indexing(), state), Clients: clients, Errors: state})
		if e != nil {
			return e
		}
		caught := service.ProcessSetupEvents(ctx)
		if caught == nil {
			return fmt.Errorf("source error was swallowed")
		}
		named, ok := caught.(interface{ ErrorName() string })
		if !ok {
			return fmt.Errorf("source error class missing: %T", caught)
		}
		_, isFunction := caught.(*IndexingFunctionError)
		observed := Object{"name": named.ErrorName(), "message": caught.Error(), "isBaseError": knownHandlerError(caught), "isIndexingFunctionError": isFunction, "sameIdentity": caught == thrown}
		indexingEqual(t, "source error branding", observed, record["result"])
		indexingEqual(t, "source hasError", service.Metrics().HasError, record["hasError"])
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

// Zero-credit control for the explicit raw-request / canonical-cache-key boundary.
// Literal independent expectations catch accidental lowercasing of the provider
// request as well as failure to lowercase the native cache envelope's params.
func TestSourceIndexingCacheKeyBoundary(t *testing.T) {
	const address = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	const canonical = `[1,"eth_getBalance",["0x70997970c51812dc3a010c7d01b50e0d17dc79c8","0x0"]]`
	const rawEnvelope = `[1,"eth_getBalance",["0x70997970C51812dc3A010C7d01b50e0d17dc79C8","0x0"]]`
	requester := &balanceRequester{}
	cache := &balanceCache{values: map[string]json.RawMessage{}}
	chain := Object{"id": 1, "cacheRpcRequests": true}
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{requester}, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(context.Background())
	clients.SetEvent(Object{"type": "setup", "block": 0})
	client, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	value, err := client.GetBalance(context.Background(), address)
	if err != nil || value.Int64() != 42 {
		t.Fatal(value, err)
	}
	if err = clients.WaitCacheWrites(context.Background()); err != nil {
		t.Fatal(err)
	}
	indexingEqual(t, "provider preserves checksum", requester.params, []any{address, "0x0"})
	if raw, ok := cache.values[canonical]; !ok || string(raw) != `"0x2a"` {
		t.Fatal("canonical cache key/result missing", cache.values)
	}
	if _, ok := cache.values[rawEnvelope]; ok {
		t.Fatal("raw checksum envelope used as canonical key")
	}
	if len(cache.values) != 1 || requester.calls != 1 {
		t.Fatal("unexpected cache/RPC cardinality", len(cache.values), requester.calls)
	}
}
