package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"testing"

	"ponder.local/ponder-go/rpc"
)

// Supplemental control: a supported speculative error is not a failed durable
// cache write. An eventual successful multicall must remain usable in managed mode.
func TestManagedClientSpeculativeFailureAllowsMulticallFallback(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64
	requester := admissionRequester(func(_ context.Context, _ string, _ any, out any) error {
		if calls.Add(1) == 1 {
			return errors.New("speculative RPC failure")
		}
		// ABI ((bool,bytes)[]) with one successful uint256 value 42.
		value := "0x"
		for _, word := range []int{32, 1, 32, 1, 64, 32, 42} {
			value += fmt.Sprintf("%064x", word)
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, out)
	})
	chain := Object{"id": 1}
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{requester})
	if err != nil {
		t.Fatal(err)
	}
	clients.managedRoots = newManagedClientRoots()
	defer func() {
		if err := clients.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	client, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	abi := actionTestABI(t, `[{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]}]`)
	contract := ContractCall{ABI: abi, Address: "0x0000000000000000000000000000000000000001", FunctionName: "value"}
	data, _, err := encodeContractCall(contract)
	if err != nil {
		t.Fatal(err)
	}
	params := []any{Object{"to": contract.Address, "data": data}, "0x1"}
	key, err := client.cacheKey("eth_call", params)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.admitPrefetch(ctx, key, params); err != nil {
		t.Fatal(err)
	}
	if err = clients.WaitPrefetch(ctx); err != nil {
		t.Fatal(err)
	}
	clients.mu.RLock()
	future := client.prefetched[key]
	clients.mu.RUnlock()
	if future == nil || future.err == nil {
		t.Fatal("failed speculative future missing")
	}
	allow, retry := false, false
	results, err := client.Multicall(ctx, []ContractCall{contract}, MulticallOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(1)}, MulticallAddress: "0x0000000000000000000000000000000000000002", AllowFailure: &allow, RetryEmptyResponse: &retry})
	if err != nil || len(results) != 1 {
		t.Fatal(results, err)
	}
	n, ok := results[0].(*big.Int)
	if !ok || n.Int64() != 42 || calls.Load() != 2 {
		t.Fatalf("fallback result=%v calls=%d", results, calls.Load())
	}
	if err := clients.managedRoots.failure(); err != nil {
		t.Fatal("successful fallback poisoned readiness", err)
	}
}
