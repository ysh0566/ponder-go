package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type countedSourceClientCache struct {
	*store.AsyncSourceRPCRequestCache
	reads, writes atomic.Int64
}

func (c *countedSourceClientCache) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	c.reads.Add(1)
	return c.AsyncSourceRPCRequestCache.CacheGet(ctx, key)
}
func (c *countedSourceClientCache) CacheGetMany(ctx context.Context, keys []string) ([]json.RawMessage, error) {
	c.reads.Add(1)
	return c.AsyncSourceRPCRequestCache.CacheGetMany(ctx, keys)
}
func (c *countedSourceClientCache) BeginCachePut(ctx context.Context, key string, v json.RawMessage) <-chan error {
	c.writes.Add(1)
	return c.AsyncSourceRPCRequestCache.BeginCachePut(ctx, key, v)
}
func (c *countedSourceClientCache) BeginCachePutMany(ctx context.Context, entries []rpc.CacheEntry) <-chan error {
	c.writes.Add(1)
	return c.AsyncSourceRPCRequestCache.BeginCachePutMany(ctx, entries)
}

func TestSourceClientRequests(t *testing.T) {
	fixtures := sourceContracts(t)
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			var db store.Backend
			var err error
			if dialect == "postgres" {
				connection := os.Getenv("PONDER_TEST_POSTGRES")
				if connection == "" {
					t.Skip("requires task PostgreSQL")
				}
				db, err = store.OpenPostgres(t.Context(), connection)
			} else {
				db, err = store.OpenPGlite(t.Context(), "memory://")
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			for _, name := range []string{"request() block dependent method", "request() block dependent method without rpc request cache", "request() non-block dependent method", "request() non-cached method", "request() multicall", "request() multicall without rpc request cache", "request() multicall empty", "request() revert", "readContract() action retry", "readContract() with immutable cache", "readContract() with no retry empty response", "getBlock() action retry"} {
				t.Run(name, func(t *testing.T) { sourceClientCase(t, db, fixtures, name) })
			}
		})
	}
}

func sourceClientCase(t *testing.T, db store.Backend, fixtures sourceChainContracts, name string) {
	t.Helper()
	ctx := t.Context()
	net := newSourceClientChain(t, fixtures)
	transport := &sourceClientRPC{rpc: net.rpc}
	st, err := store.New(ctx, db, schema.Schema{}, fmt.Sprintf("ponder_go_client_%x", time.Now().UnixNano()), schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := st.EnableProtocolCache(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer protocol.Close()
	cache := &countedSourceClientCache{AsyncSourceRPCRequestCache: protocol.AsyncSourceRPCRequestCache(1)}
	defer cache.Close(context.Background())
	enabled := !strings.Contains(name, "without rpc request cache")
	chain := Object{"id": 1, "name": "mainnet", "cacheRpcRequests": enabled}
	set, err := NewStrictClientSet([]Object{chain}, []rpc.Requester{transport}, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	c, err := set.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	var token, reverter string
	isToken := strings.HasPrefix(name, "readContract") || name == "request() non-block dependent method" || name == "request() revert"
	if isToken {
		if name == "request() revert" {
			reverter = net.deploy("Revert")
		}
		token = net.deploy("ERC20")
		net.mint(token)
	} else {
		net.mine()
	}
	block := net.block()
	eventName, eventType := "Blocks:block", "block"
	if isToken {
		eventName, eventType = "Erc20:Transfer(address indexed from, address indexed to, uint256 amount)", "log"
	}
	internalBlock, err := events.SyncBlockToInternal(block)
	if err != nil {
		t.Fatal(err)
	}
	set.SetEventCounts(map[string]uint64{eventName: 0})
	set.SetEvent(Object{"type": "event", "chain": chain, "eventCallback": Object{"name": eventName, "type": eventType}, "event": Object{"block": internalBlock}})

	read := func(method string, params []any) json.RawMessage {
		t.Helper()
		v, err := c.Request(ctx, method, params)
		if err != nil || len(v) == 0 || bytes.Equal(v, []byte("null")) {
			t.Fatalf("%s: %s %v", method, v, err)
		}
		if err = set.WaitCacheWrites(ctx); err != nil {
			t.Fatal(err)
		}
		return v
	}
	reset := func() { cache.reads.Store(0); cache.writes.Store(0); transport.calls = nil }
	count := func(gets, puts, calls int) {
		t.Helper()
		if cache.reads.Load() != int64(gets) || cache.writes.Load() != int64(puts) || len(transport.calls) != calls {
			t.Fatalf("cache reads, writes, RPC calls = %d, %d, %d; want %d, %d, %d", cache.reads.Load(), cache.writes.Load(), len(transport.calls), gets, puts, calls)
		}
	}
	switch {
	case strings.Contains(name, "dependent method"):
		method, params := "eth_getBlockByNumber", []any{block["number"], false}
		if isToken {
			method, params = "eth_getTransactionByHash", []any{block["transactions"].([]any)[0].(Object)["hash"]}
		}
		first := read(method, params)
		if enabled {
			reset()
		}
		second := read(method, params)
		if !bytes.Equal(first, second) {
			var a, b Object
			if json.Unmarshal(first, &a) != nil || json.Unmarshal(second, &b) != nil {
				t.Fatal("invalid RPC object")
			}
			left, _ := json.Marshal(a)
			right, _ := json.Marshal(b)
			if !bytes.Equal(left, right) {
				t.Fatal("cached result differs", string(first), string(second))
			}
		}
		if enabled {
			count(1, 0, 0)
		} else {
			count(0, 0, 2)
		}
	case name == "request() non-cached method":
		read("eth_blockNumber", nil)
		count(0, 0, 1)
	case strings.HasPrefix(name, "request() multicall"):
		address := net.deploy("multicall")
		calls := []any{}
		if name != "request() multicall empty" {
			token = net.deploy("ERC20")
			net.mint(token)
			calls = append(calls, Object{"target": token, "allowFailure": false, "callData": net.calldata(token, "totalSupply")})
		}
		aggregate := func() {
			t.Helper()
			params := []any{Object{"to": address, "data": net.calldata(address, "aggregate3", calls)}, "latest"}
			v := read("eth_call", params)
			raw, err := decodeHexResponse(v)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeContractResult(aggregateFunction, raw)
			if err != nil {
				t.Fatal(err)
			}
			rows := decoded.([]any)
			if len(rows) != len(calls) {
				t.Fatalf("got %d results, want %d", len(rows), len(calls))
			}
			for _, row := range rows {
				entry := row.(Object)
				if len(entry) != 2 || entry["success"] != true || entry["returnData"] != "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000" {
					t.Fatal(entry)
				}
			}
		}
		aggregate()
		if name == "request() multicall" {
			reset()
			calls = append(calls, Object{"target": token, "allowFailure": false, "callData": net.calldata(token, "balanceOf", sourceClientAlice)})
			aggregate()
			count(1, 1, 1)
		} else if enabled {
			count(0, 0, 0)
		} else {
			count(0, 0, 1)
		}
	case name == "request() revert":
		_, err := c.Request(ctx, "eth_call", []any{Object{"to": reverter, "data": net.calldata(reverter, "revert", true)}, "0x1"})
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("expected revert, got %v", err)
		}
	case strings.HasPrefix(name, "readContract"):
		options := ReadContractOptions{}
		if name == "readContract() with immutable cache" {
			options.Cache = "immutable"
		} else {
			transport.first = json.RawMessage(`"0x"`)
		}
		if name == "readContract() with no retry empty response" {
			retry := false
			options.RetryEmptyResponse = &retry
		}
		v, err := c.ReadContract(ctx, ContractCall{Address: token, ABI: fixtures.abis["ERC20"], FunctionName: "totalSupply"}, options)
		switch name {
		case "readContract() with immutable cache":
			if err != nil || fmt.Sprint(v) != "1000000000000000000" || len(transport.calls) != 1 || transport.calls[0].Method != "eth_call" || transport.calls[0].Params[1] != "latest" {
				t.Fatal(v, err, transport.calls)
			}
		case "readContract() action retry":
			if err != nil || len(transport.calls) != 2 {
				t.Fatal(err, transport.calls)
			}
		default:
			var empty *ContractFunctionZeroDataError
			if !errors.As(err, &empty) || len(transport.calls) != 1 {
				t.Fatal(err, transport.calls)
			}
		}
	case name == "getBlock() action retry":
		transport.first = json.RawMessage("null")
		b, err := c.GetBlock(ctx, GetBlockOptions{BlockNumber: big.NewInt(1)})
		if err != nil || b["number"] != "0x1" || len(transport.calls) != 2 {
			t.Fatal(b, err, transport.calls)
		}
	}
	if err := set.WaitCacheWrites(ctx); err != nil {
		t.Fatal(err)
	}
}

// An aggregate's per-call failure flag must survive a cache miss. Rewriting a
// required failure to an optional one changes a reverted RPC into a success.
func TestRawAggregateFailurePolicy(t *testing.T) {
	net := newSourceClientChain(t, sourceContracts(t))
	address := net.deploy("multicall")
	reverter := net.deploy("Revert")
	transport := &sourceClientRPC{rpc: net.rpc}
	chain := Object{"id": 1}
	set, err := NewClientSet([]Object{chain}, []rpc.Requester{transport})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	client, err := set.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	for _, allow := range []bool{false, true} {
		input := net.calldata(address, "aggregate3", []any{Object{"target": reverter, "allowFailure": allow, "callData": net.calldata(reverter, "revert", true)}})
		raw, err := client.Request(t.Context(), "eth_call", []any{Object{"to": address, "data": input}, "latest"})
		if !allow {
			var reverted *rpc.Error
			if !errors.As(err, &reverted) {
				t.Fatalf("required call returned %s, %v", raw, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := decodeHexResponse(raw)
		if err != nil {
			t.Fatal(err)
		}
		results, err := decodeContractResult(aggregateFunction, encoded)
		if err != nil {
			t.Fatal(err)
		}
		if result := results.([]any)[0].(Object); result["success"] != false {
			t.Fatalf("optional failure: %v", result)
		}
	}
}
