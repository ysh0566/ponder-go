package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestCallbackMulticallDeploymentAndOverrides(t *testing.T) {
	fixtures := sourceContracts(t)
	view, err := abi.Parse(`[{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]}]`)
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			net := newSourceClientChain(t, fixtures)
			valTarget := "0x000000000000000000000000000000000000c001"
			originTarget := "0x000000000000000000000000000000000000c002"
			word := func(n int) string { return fmt.Sprintf("0x%064x", n) }
			var ignored any
			// Return storage slot zero or tx.origin from inside a real aggregate3.
			net.call("anvil_setCode", []any{valTarget, "0x60005460005260206000f3"}, &ignored)
			net.call("anvil_setStorageAt", []any{valTarget, word(0), word(42)}, &ignored)
			net.call("anvil_setCode", []any{originTarget, "0x3260005260206000f3"}, &ignored)
			multicall := net.deploy("multicall")
			chain := Object{"name": "local", "id": 1, "cacheRpcRequests": true,
				"viemChain": Object{"contracts": Object{"multicall3": Object{"address": multicall, "blockCreated": 1}}}}
			cache := adapterStore(t, dialect)
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{net.rpc}, cache)
			if err != nil {
				t.Fatal(err)
			}
			defer clients.Close(context.Background())
			runClientCallback(t, ctx, dialect, chain, clients, func(ctx context.Context, args *HandlerArgs) error {
				c := args.Context.Client
				calls := []ContractCall{{ABI: view, Address: valTarget, FunctionName: "value"}, {ABI: view, Address: originTarget, FunctionName: "value"}}
				allow := false
				sender, _ := new(big.Int).SetString(sourceClientAlice[2:], 16)
				check := func(opt MulticallOptions, value int, origin *big.Int) error {
					opt.AllowFailure = &allow
					values, err := c.Multicall(ctx, calls, opt)
					if err != nil || len(values) != 2 || fmt.Sprint(values[0]) != fmt.Sprint(value) || fmt.Sprint(values[1]) != origin.String() {
						return fmt.Errorf("multicall values = %v, want [%d %s]: %v", values, value, origin, err)
					}
					return clients.WaitCacheWrites(ctx)
				}
				zero := new(big.Int)
				if err := check(MulticallOptions{}, 42, zero); err != nil {
					return err
				}
				if err := check(MulticallOptions{Account: sourceClientAlice}, 42, sender); err != nil {
					return err
				}
				override := StateOverride{valTarget: {StateDiff: map[string]string{word(0): word(97)}}}
				batchSize := 4 // each selector is four bytes, so send two chunks
				if err := check(MulticallOptions{Account: sourceClientAlice, StateOverride: override, BatchSize: &batchSize}, 97, sender); err != nil {
					return err
				}
				if err := check(MulticallOptions{StateOverride: StateOverride{valTarget: {State: map[string]string{}}}}, 0, zero); err != nil {
					return err
				}
				if err := check(MulticallOptions{MulticallAddress: multicall}, 42, zero); err != nil {
					return err
				}
				data, _, err := encodeContractCall(calls[0])
				if err != nil {
					return err
				}
				rawData := net.calldata(multicall, "aggregate3", []any{Object{"target": valTarget, "allowFailure": true, "callData": data}, Object{"target": originTarget, "allowFailure": true, "callData": data}})
				senderRequest := Object{"to": multicall, "data": rawData, "from": sourceClientAlice}
				plain := Object{"to": multicall, "data": rawData}
				overrideParams, err := override.rpcParams()
				if err != nil {
					return err
				}
				blockByHash := Object{"blockHash": net.block()["hash"], "requireCanonical": true}
				for _, params := range [][]any{{senderRequest, "0x1"}, {plain, "0x1", overrideParams}, {senderRequest, "0x1", overrideParams}, {plain, blockByHash}} {
					var expected json.RawMessage
					if err := net.rpc.Call(ctx, "eth_call", params, &expected); err != nil {
						return err
					}
					actual, err := c.Request(ctx, "eth_call", params)
					if err != nil || string(actual) != string(expected) {
						return fmt.Errorf("raw aggregate differs from node: %s, want %s, %v", actual, expected, err)
					}
				}
				lowGas := []any{Object{"to": multicall, "data": rawData, "gas": "0x5208"}, "0x1"}
				var raw json.RawMessage
				if err := net.rpc.Call(ctx, "eth_call", lowGas, &raw); err == nil {
					return fmt.Errorf("node unexpectedly accepted insufficient gas")
				}
				if _, err := c.Request(ctx, "eth_call", lowGas); err == nil {
					return fmt.Errorf("client dropped the aggregate gas limit")
				}
				return check(MulticallOptions{}, 42, zero)
			})
		})
	}
}

func TestMulticallDeploymentBounds(t *testing.T) {
	address := "0x1000000000000000000000000000000000000001"
	chain := Object{"id": 1, "viemChain": Object{"contracts": Object{"multicall3": Object{"address": address, "blockCreated": 3}}}}
	tr := admissionRequester(func(context.Context, string, any, any) error {
		t.Fatal("unexpected RPC for empty calls or invalid selection")
		return nil
	})
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{tr})
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(t.Context())
	c, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	clients.SetEvent(Object{"type": "setup", "block": 2})
	if _, err := c.Multicall(t.Context(), nil, MulticallOptions{}); err == nil || !strings.Contains(err.Error(), "deployed at block 3") {
		t.Fatalf("before deployment: %v", err)
	}
	for _, opt := range []MulticallOptions{{MulticallAddress: address}, {BlockOptions: BlockOptions{BlockNumber: big.NewInt(3)}}, {BlockOptions: BlockOptions{Cache: "immutable"}}} {
		if _, err := c.Multicall(t.Context(), nil, opt); err != nil {
			t.Fatal(err)
		}
	}
	for _, opt := range []MulticallOptions{{Account: "invalid"}, {StateOverride: StateOverride{address: {State: map[string]string{}, StateDiff: map[string]string{}}}}, {StateOverride: StateOverride{address: {Balance: big.NewInt(-1)}}}} {
		opt.MulticallAddress = address
		if _, err := c.Multicall(t.Context(), nil, opt); err == nil {
			t.Fatal("accepted invalid options")
		}
	}
	nonce := uint64(0)
	code := "0x"
	override, err := (StateOverride{address: {Code: &code, Balance: big.NewInt(32), Nonce: &nonce, State: map[string]string{}}}).rpcParams()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(override)
	want := `{"0x1000000000000000000000000000000000000001":{"balance":"0x20","code":"0x","nonce":"0x0","state":{}}}`
	if err != nil || string(b) != want {
		t.Fatal(string(b), err)
	}
}
