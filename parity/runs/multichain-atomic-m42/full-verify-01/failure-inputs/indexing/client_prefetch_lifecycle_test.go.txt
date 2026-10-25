package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	ponderabi "ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/rpc"
	"sync"
	"testing"
	"time"
)

type ownedLookupCache struct {
	chain   int
	entered chan struct{}
	once    sync.Once
}

func (c *ownedLookupCache) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	var envelope []json.RawMessage
	if err := json.Unmarshal([]byte(key), &envelope); err != nil {
		return nil, false, err
	}
	var params []any
	if err := json.Unmarshal(envelope[2], &params); err != nil {
		return nil, false, err
	}
	if params[1] != "0x8" {
		return nil, false, nil
	}
	if c.chain == 1 {
		return nil, false, errors.New("lookup failed")
	}
	c.once.Do(func() { close(c.entered) })
	<-ctx.Done()
	return nil, false, ctx.Err()
}
func (c *ownedLookupCache) CachePut(context.Context, string, json.RawMessage) error { return nil }
func (c *ownedLookupCache) BeginCachePut(context.Context, string, json.RawMessage) <-chan error {
	done := make(chan error, 1)
	done <- nil
	close(done)
	return done
}

type ownedLookupRPC struct{}

func (ownedLookupRPC) Call(_ context.Context, _ string, _ any, out any) error {
	raw, _ := json.Marshal(fmt.Sprintf("0x%064x", 41))
	return json.Unmarshal(raw, out)
}

// Fail-fast does not abandon a source-like sibling lookup. Its lifetime remains
// owned by ClientSet and Close cancels/joins it, independently of caller release.
func TestClientPrefetchCloseOwnsFailedSibling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chains := []Object{{"id": 1, "cacheRpcRequests": true}, {"id": 2, "cacheRpcRequests": true}}
	held := &ownedLookupCache{chain: 2, entered: make(chan struct{})}
	clients, err := NewStrictClientSet(chains, []rpc.Requester{ownedLookupRPC{}, ownedLookupRPC{}}, &ownedLookupCache{chain: 1}, held)
	if err != nil {
		t.Fatal(err)
	}
	abi, err := ponderabi.Parse(`[{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]}]`)
	if err != nil {
		t.Fatal(err)
	}
	event := func(chain Object, address string, block int64) Object {
		return Object{"type": "log", "chain": chain, "eventCallback": Object{"name": "sample"}, "event": Object{"log": Object{"address": address}, "block": Object{"number": big.NewInt(block)}}}
	}
	address1 := "0x0000000000000000000000000000000000000001"
	address2 := "0x0000000000000000000000000000000000000002"
	clients.SetEvent(event(chains[0], address1, 7))
	clients.SetEventCounts(map[string]uint64{"sample": 1})
	client, _ := clients.GetClient(chains[0])
	if _, err = client.ReadContract(ctx, ContractCall{ABI: abi, Address: address1, FunctionName: "value"}); err != nil {
		t.Fatal(err)
	}
	if err = clients.Prefetch(ctx, []Object{event(chains[0], address1, 8), event(chains[1], address2, 8)}); err == nil || err.Error() != "lookup failed" {
		t.Fatal("must reject before held sibling releases", err)
	}
	select {
	case <-held.entered:
	case <-ctx.Done():
		t.Fatal("sibling lookup never independently entered")
	}
	if err = clients.Close(ctx); err != nil {
		t.Fatal(err)
	}
	clients.mu.RLock()
	lookups, futures, writes := len(clients.lookupPending), len(clients.prefetchPending), len(clients.pending)
	clients.mu.RUnlock()
	if lookups != 0 || futures != 0 || writes != 0 {
		t.Fatal("Close abandoned owned work", lookups, futures, writes)
	}
}

type singleSettlementCache struct{}

func (singleSettlementCache) CacheGet(context.Context, string) (json.RawMessage, bool, error) {
	return nil, false, nil
}
func (singleSettlementCache) CachePut(context.Context, string, json.RawMessage) error { return nil }
func (singleSettlementCache) BeginCachePut(context.Context, string, json.RawMessage) <-chan error {
	out := make(chan error, 1)
	out <- nil
	return out
}
func (singleSettlementCache) BeginCacheGet(context.Context, string) <-chan rpc.CacheLookupResult {
	out := make(chan rpc.CacheLookupResult, 1)
	out <- rpc.CacheLookupResult{}
	return out
}

// Optional reader completion is received exactly once. Production closes its
// channel, but a valid custom one-settlement future need not close to be drained.
func TestClientPrefetchReaderSingleSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chain := Object{"id": 1, "cacheRpcRequests": true}
	clients, err := NewStrictClientSet([]Object{chain}, []rpc.Requester{ownedLookupRPC{}}, singleSettlementCache{})
	if err != nil {
		t.Fatal(err)
	}
	abi, err := ponderabi.Parse(`[{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]}]`)
	if err != nil {
		t.Fatal(err)
	}
	address := "0x0000000000000000000000000000000000000001"
	event := Object{"type": "log", "chain": chain, "eventCallback": Object{"name": "sample"}, "event": Object{"log": Object{"address": address}, "block": Object{"number": big.NewInt(7)}}}
	clients.SetEvent(event)
	clients.SetEventCounts(map[string]uint64{"sample": 1})
	client, _ := clients.GetClient(chain)
	if _, err = client.ReadContract(ctx, ContractCall{ABI: abi, Address: address, FunctionName: "value"}); err != nil {
		t.Fatal(err)
	}
	event["event"].(Object)["block"].(Object)["number"] = big.NewInt(8)
	done := make(chan error, 1)
	go func() { done <- clients.Prefetch(ctx, []Object{event}) }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("lookup result was read more than once")
	}
	if err = clients.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
