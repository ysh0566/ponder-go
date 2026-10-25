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
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	ponderabi "ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
)

var sourceActionIDs = []string{"9614c3868c45f1ab5064", "f62a1b73602a98909575", "8244ed823366e5a1da54", "ee3742b77c929d94a8ed", "4dfbaabe2e3b99f00b8d", "175d1d5d0afe91cc20a1", "de123b064923c7ffd321", "4204e282e98522f9eefa"}

// TestSourceIndexingActions replays only source action INPUTS and captured raw
// provider responses. Action returns and physical source rows are checks, never
// execution seeds. Both backends execute real cache migrations, reads and writes.
func TestSourceIndexingActions(t *testing.T) {
	raw, err := os.ReadFile("../testdata/indexing/source-indexing-actions-live.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Targets      []string            `json:"targets"`
		BackendCases map[string][]Object `json:"backend_cases"`
	}
	if err = json.Unmarshal(decoded, &fixture); err != nil {
		t.Fatal(err)
	}
	indexingEqual(t, "exact original target IDs", fixture.Targets, sourceActionIDs)
	observations := []Object{}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			cases := fixture.BackendCases[backend]
			if len(cases) != 23 {
				t.Fatalf("expected complete source 23-case census, got %d", len(cases))
			}
			seen := map[string]bool{}
			for _, record := range cases {
				targeted, _ := record["targeted"].(bool)
				if !targeted {
					continue
				}
				id := record["id"].(string)
				if seen[id] {
					t.Fatal("duplicate source case", id)
				}
				seen[id] = true
				var observation Object
				pass := t.Run(id, func(t *testing.T) { observation = replaySourceAction(t, backend, record) })
				if observation != nil {
					observation["pass"] = pass
					observations = append(observations, observation)
				}
			}
			for _, id := range sourceActionIDs {
				if !seen[id] {
					t.Fatal("missing target", id)
				}
			}
			if len(seen) != 8 {
				t.Fatalf("target count %d", len(seen))
			}
		})
	}
	if output := os.Getenv("PONDER_INDEXING_ACTIONS_NATIVE_OUTPUT"); output != "" {
		if len(observations) != 16 {
			t.Fatalf("native artifact requires all 16 backend cases, got %d", len(observations))
		}
		hash := sha256.Sum256(raw)
		artifact := Object{"source_credit": 0, "source_fixture_sha256": hex.EncodeToString(hash[:]), "cases": observations}
		data, err := json.MarshalIndent(artifact, "", "  ")
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

func replaySourceAction(t *testing.T, backend string, record Object) Object {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s := indexingReplayStore(t, backend)
	cache, err := s.EnableProtocolCache(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cache.Close()
		_ = s.Backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(cache.Namespace())+" CASCADE")
	})
	clientsInput := record["clients"].([]any)
	if len(clientsInput) != 1 {
		t.Fatal("source client count")
	}
	chains := indexingObjects(indexingObject(clientsInput[0])["chains"])
	if len(chains) != 1 {
		t.Fatal("source chain count")
	}
	chain := chains[0]
	chainID, err := clientInteger(chain["id"])
	if err != nil || !chainID.IsUint64() {
		t.Fatal("invalid source chain")
	}
	actions := record["actions"].([]any)
	if len(actions) != 1 {
		t.Fatal("expected one original action")
	}
	action := indexingObject(actions[0])
	providers := indexingObjects(action["providers"])
	var mu sync.Mutex
	actualProviders := []Object{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Object
		if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		index := len(actualProviders)
		actual := Object{"method": request["method"], "params": request["params"]}
		if index >= len(providers) {
			t.Error("unexpected extra provider call", actual)
			w.WriteHeader(500)
			return
		}
		expected := providers[index]
		got, _ := json.Marshal(actual)
		want, _ := json.Marshal(expected["request"])
		actualProviders = append(actualProviders, Object{"request": actual, "response": expected["response"]})
		if string(got) != string(want) {
			t.Errorf("exact RPC request mismatch: got %s want %s", got, want)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(Object{"jsonrpc": "2.0", "id": request["id"], "result": expected["response"]})
	}))
	defer server.Close()
	transport := rpc.New(server.URL)
	transport.Retries = 0
	transport.RequestsPerSecond = 0
	adapter := cache.AsyncSourceRPCRequestCache(chainID.Uint64())
	defer func() {
		if err := adapter.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	clients, err := NewStrictClientSet(chains, []rpc.Requester{transport}, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := clients.Close(context.Background()); e != nil {
			t.Error(e)
		}
	}()
	client, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	event := indexingObject(indexingUnmark(t, action["event"]))
	if event["type"] == "setup" {
		clients.SetEvent(Object{"type": "setup", "block": event["block"]})
	} else {
		clients.SetEvent(Object{"type": event["type"], "event": Object{"block": Object{"number": event["block"]}}})
	}
	args := indexingUnmark(t, action["args"]).([]any)
	if len(args) != 1 {
		t.Fatal("source action args count")
	}
	arg := indexingObject(args[0])
	opt := BlockOptions{}
	if block, ok := arg["blockNumber"].(*big.Int); ok {
		opt.BlockNumber = block
	}
	if policy, ok := arg["cache"].(string); ok {
		opt.Cache = policy
	}
	var result any
	switch action["operation"] {
	case "getBalance":
		result, err = client.GetBalance(ctx, arg["address"].(string), opt)
	case "getCode":
		var code *string
		code, err = client.GetCode(ctx, arg["address"].(string), opt)
		if code != nil {
			result = *code
		}
	case "getStorageAt":
		result, err = client.GetStorageAt(ctx, arg["address"].(string), arg["slot"].(string), opt)
	case "readContract":
		result, err = client.ReadContract(ctx, sourceActionContract(t, arg), ReadContractOptions{BlockOptions: opt})
	case "multicall":
		contracts := []ContractCall{}
		for _, item := range arg["contracts"].([]any) {
			contracts = append(contracts, sourceActionContract(t, indexingObject(item)))
		}
		allow := arg["allowFailure"].(bool)
		var results []any
		results, err = client.Multicall(ctx, contracts, MulticallOptions{BlockOptions: opt, MulticallAddress: arg["multicallAddress"].(string), AllowFailure: &allow})
		normalized := make([]any, len(results))
		for i, value := range results {
			if v, ok := value.(MulticallResult); ok {
				if v.Error != nil {
					t.Fatal(v.Error)
				}
				normalized[i] = Object{"status": v.Status, "result": v.Result}
			} else {
				normalized[i] = value
			}
		}
		result = normalized
	default:
		t.Fatal("unsupported original action", action["operation"])
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = clients.WaitCacheWrites(ctx); err != nil {
		t.Fatal(err)
	}
	indexingEqual(t, "complete source action result", result, indexingUnmark(t, action["result"]))
	mu.Lock()
	actualTape := append([]Object{}, actualProviders...)
	mu.Unlock()
	if len(actualTape) != len(providers) {
		t.Fatalf("RPC count %d want %d", len(actualTape), len(providers))
	}
	indexingEqual(t, "complete provider tape", actualTape, providers)
	// Original assertions are repeated independently from the adapter output check.
	id := record["id"].(string)
	checked := 1
	one := big.NewInt(1000000000000000000)
	switch id {
	case "9614c3868c45f1ab5064":
		want, _ := new(big.Int).SetString("10000000000000000000000", 10)
		if result.(*big.Int).Cmp(want) != 0 {
			t.Fatal("original balance assertion")
		}
	case "f62a1b73602a98909575":
		if result == nil || result.(string) == "" {
			t.Fatal("original code truthy assertion")
		}
	case "8244ed823366e5a1da54":
		n, ok := new(big.Int).SetString(result.(string)[2:], 16)
		if !ok || n.Cmp(one) != 0 {
			t.Fatal("original storage assertion")
		}
	case "ee3742b77c929d94a8ed", "175d1d5d0afe91cc20a1":
		if result.(*big.Int).Cmp(one) != 0 {
			t.Fatal("original totalSupply assertion")
		}
		if id == "175d1d5d0afe91cc20a1" {
			checked++
			if len(actualTape) != 2 {
				t.Fatal("original requestSpy calledTimes(2) assertion")
			}
		}
	case "4dfbaabe2e3b99f00b8d":
		if result.(*big.Int).Sign() != 0 {
			t.Fatal("original blockNumber assertion")
		}
	case "de123b064923c7ffd321":
		if result.([]any)[0].(*big.Int).Cmp(one) != 0 {
			t.Fatal("original multicall first-result assertion")
		}
	case "4204e282e98522f9eefa":
		indexingEqual(t, "original allowFailure snapshot", result, []any{Object{"result": one, "status": "success"}, Object{"result": one, "status": "success"}})
	default:
		t.Fatal("unknown source ID", id)
	}
	if len(record["assertions"].([]any)) != checked {
		t.Fatal("source assertion census changed")
	}
	rows, err := s.Backend.Query(ctx, "SELECT request_hash,chain_id::text AS chain_id,block_number::text AS block_number,result FROM "+schema.Quote(cache.Namespace())+".rpc_request_results ORDER BY request_hash")
	if err != nil {
		t.Fatal(err)
	}
	physical := []Object{}
	for _, row := range rows.Rows {
		chain, ok := new(big.Int).SetString(fmt.Sprint(row["chain_id"]), 10)
		if !ok {
			t.Fatal("invalid physical chain")
		}
		var block any
		if row["block_number"] != nil {
			n, ok := new(big.Int).SetString(fmt.Sprint(row["block_number"]), 10)
			if !ok {
				t.Fatal("invalid physical block")
			}
			block = n
		}
		physical = append(physical, Object{"requestHash": row["request_hash"], "chainId": chain, "blockNumber": block, "result": row["result"]})
	}
	expectedRows := indexingObjects(indexingUnmark(t, record["physical_after"]))
	sort.Slice(expectedRows, func(i, j int) bool {
		return expectedRows[i]["requestHash"].(string) < expectedRows[j]["requestHash"].(string)
	})
	indexingEqual(t, "physical MD5 chain/block/result cache rows", physical, expectedRows)
	digest := indexingCanonical(Object{"result": result, "providers": actualTape, "physical_after": physical})
	digestBytes, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(digestBytes)
	return Object{"backend": backend, "id": id, "name": record["full_name"], "source_assertions_checked": checked, "digest_input": digest, "observation_sha256": hex.EncodeToString(hash[:]), "rpc_calls": len(actualTape), "cache_rows": len(physical)}
}

func sourceActionContract(t *testing.T, arg Object) ContractCall {
	t.Helper()
	raw, err := json.Marshal(arg["abi"])
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ponderabi.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	args, _ := arg["args"].([]any)
	return ContractCall{ABI: parsed, Address: arg["address"].(string), FunctionName: arg["functionName"].(string), Args: args}
}
