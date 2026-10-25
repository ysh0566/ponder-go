package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	ponderabi "ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/rpc"
	"testing"
)

// Actual Service dispatch owns counts; no precomputed profile or result is
// injected. Historical raw events profile without touching proxy read counters,
// then a recovered next-event request is consumed by realtime dispatch.
func TestClientProfileServiceContext(t *testing.T) {
	serviceSemanticStore(t, func(t *testing.T, db *Database, state *ErrorState) {
		ctx := context.Background()
		abi, err := ponderabi.Parse(`[{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]}]`)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var input Object
			if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
				t.Error(e)
				return
			}
			_ = json.NewEncoder(w).Encode(Object{"jsonrpc": "2.0", "id": input["id"], "result": fmt.Sprintf("0x%064x", 41)})
		}))
		defer server.Close()
		transport := rpc.New(server.URL)
		transport.Retries = 0
		transport.RequestsPerSecond = 0
		requester := &prefetchCountingRequester{rpc: transport}
		chain := Object{"id": 1, "name": "one", "cacheRpcRequests": false}
		clients, err := NewStrictClientSet([]Object{chain}, []rpc.Requester{requester})
		if err != nil {
			t.Fatal(err)
		}
		defer clients.Close(ctx)
		address := "0x0000000000000000000000000000000000000041"
		seen := 0
		handler := Handler(func(ctx context.Context, args *HandlerArgs) error {
			value, e := args.Context.Client.ReadContract(ctx, ContractCall{ABI: abi, Address: address, FunctionName: "value"})
			if e != nil {
				return e
			}
			if value.(*big.Int).Cmp(big.NewInt(41)) != 0 {
				t.Fatal("actual RPC result")
			}
			seen++
			return nil
		})
		cb := Object{"name": "Token:Transfer", "fn": handler, "filter": Object{"type": "log", "include": DefaultFilterInclude("log", false)}}
		opts := Options{Build: Build{Chains: []Object{chain}, Contracts: []Object{{}}, EventCallbacks: [][]Object{{cb}}, IndexingFunctions: []Object{{"name": "Token:Transfer"}}}, Database: db, Clients: clients, Errors: state}
		service := mustSemanticService(t, opts)
		event := func(n int64) Object {
			e := semanticEvent(chain, cb, n)
			e["event"].(Object)["log"] = Object{"address": address}
			return e
		}
		if err = service.ProcessHistoricalEvents(ctx, []Object{event(1), event(2)}, nil); err != nil {
			t.Fatal(err)
		}
		profiles := clients.Profiles("Token:Transfer")
		if len(profiles) != 1 || profiles[0].Count != 1 || service.EventCount()["Token:Transfer"] != 2 {
			t.Fatal("must sample real second dispatch only", profiles, service.EventCount())
		}
		indexingEqual(t, "actual log address derivation", profiles[0].Recorded.Pattern.Address, ProfileValue{Type: "derived", Value: []string{"log", "address"}})
		next := event(3)
		if err = clients.Prefetch(ctx, []Object{next}); err != nil {
			t.Fatal(err)
		}
		if err = service.ProcessRealtimeEvents(ctx, []Object{next}); err != nil {
			t.Fatal(err)
		}
		if seen != 3 || service.EventCount()["Token:Transfer"] != 3 {
			t.Fatal("actual successful dispatch count")
		}
		requester.mu.Lock()
		calls := append([]Object{}, requester.calls...)
		requester.mu.Unlock()
		if len(calls) != 3 {
			t.Fatalf("prefetched realtime should not refetch: %v", calls)
		}
		for i, call := range calls {
			params := call["request"].(Object)["params"].([]any)
			if params[1] != fmt.Sprintf("0x%x", i+1) {
				t.Fatal("event recovery block binding", calls)
			}
		}
		clients.Clear()
		if len(clients.Profiles("Token:Transfer")) != 1 {
			t.Fatal("clear discarded service profile")
		}
	})
}
