package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestCallbackSimulateContractAndEstimate(t *testing.T) {
	fixtures := sourceContracts(t)
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			node := newSourceClientChain(t, fixtures)
			const probe = "0x000000000000000000000000000000000000cccc"
			const recipient = "0x000000000000000000000000000000000000dddd"
			var ignored any
			// Return the NUMBER opcode as a single ABI word.
			node.call("anvil_setCode", []any{probe, "0x4360005260206000f3"}, &ignored)
			token := node.deploy("ERC20")
			var snapshot string
			node.call("evm_snapshot", []any{}, &snapshot)
			node.mint(token)
			chain := Object{"name": "local", "id": 1, "cacheRpcRequests": true}
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{node.rpc}, adapterStore(t, backend))
			if err != nil {
				t.Fatal(err)
			}
			defer clients.Close(context.Background())
			runClientCallback(t, ctx, backend, chain, clients, func(ctx context.Context, args *HandlerArgs) error {
				c := args.Context.Client
				supply := ContractCall{Address: token, ABI: fixtures.abis["ERC20"], FunctionName: "totalSupply"}
				supplyTx := Object{"to": token, "data": node.calldata(token, "totalSupply")}
				transfer := ContractCall{Address: token, ABI: fixtures.abis["ERC20"], FunctionName: "transfer", Args: []any{recipient, big.NewInt(1)}}
				transferTx := Object{"from": sourceClientAlice, "to": token, "data": node.calldata(token, "transfer", recipient, big.NewInt(1))}
				atTwo := CallOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(2)}}
				checkCall := func(tx Object, opt CallOptions, want *big.Int) error {
					data, err := c.Call(ctx, tx, opt)
					if err != nil || data == nil || *data != fmt.Sprintf("0x%064x", want) {
						return fmt.Errorf("eth_call returned %v, want %v: %v", data, want, err)
					}
					return nil
				}
				if err := checkCall(supplyTx, CallOptions{}, big.NewInt(0)); err != nil {
					return err
				}
				minted := big.NewInt(1_000_000_000_000_000_000)
				if err := checkCall(supplyTx, atTwo, minted); err != nil {
					return err
				}
				if err := checkCall(Object{"to": probe}, CallOptions{BlockOverrides: Object{"number": "0x11"}}, big.NewInt(17)); err != nil {
					return err
				}
				callerCode := "0x3460005260206000f3" // return CALLVALUE
				overridden := CallOptions{StateOverride: StateOverride{probe: {Code: &callerCode}}, BlockOverrides: Object{"number": "0x11"}}
				if err := checkCall(Object{"to": probe, "from": sourceClientAlice, "value": "0x10000000000000001"}, overridden, new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1))); err != nil {
					return err
				}
				if err := checkCall(Object{"to": probe, "data": aggregateSelector}, CallOptions{BlockOptions: BlockOptions{Cache: "immutable"}}, big.NewInt(2)); err != nil {
					return err
				}
				if err := checkCall(Object{"to": probe}, CallOptions{}, big.NewInt(1)); err != nil {
					return err
				}
				empty, err := c.Call(ctx, Object{"to": recipient})
				if err != nil || empty != nil {
					return fmt.Errorf("empty EVM response %v: %v", empty, err)
				}

				var executionError *rpc.Error
				if _, err := c.EstimateGas(ctx, transferTx); !errors.As(err, &executionError) {
					return fmt.Errorf("expected estimate to revert before mint: %v", err)
				}
				if _, err := c.SimulateContract(ctx, transfer, SimulateContractOptions{Transaction: Object{"from": sourceClientAlice}}); !errors.As(err, &executionError) {
					return fmt.Errorf("expected simulation to revert before mint: %v", err)
				}
				fields := Object{"from": sourceClientAlice}
				sim, err := c.SimulateContract(ctx, transfer, SimulateContractOptions{CallOptions: atTwo, Transaction: fields, DataSuffix: "0x1234"})
				if err != nil {
					return err
				}
				if sim.Result != true || sim.Request["from"] != sourceClientAlice || sim.Request["to"] != token || sim.Request["data"] != transferTx["data"].(string)+"1234" || len(fields) != 1 {
					return fmt.Errorf("simulation output or caller's transaction fields: %+v, %v", sim, fields)
				}
				estimate, err := c.EstimateGas(ctx, sim.Request, atTwo)
				if err != nil {
					return err
				}
				var direct string
				if err := node.rpc.Call(ctx, "eth_estimateGas", []any{sim.Request, "0x2"}, &direct); err != nil {
					return err
				}
				if estimate.Cmp(big.NewInt(21000)) <= 0 || hexutil.EncodeBig(estimate) != direct {
					return fmt.Errorf("estimated %v, node %s", estimate, direct)
				}
				balanceTx := Object{"to": token, "data": node.calldata(token, "balanceOf", recipient)}
				if err := checkCall(balanceTx, atTwo, big.NewInt(0)); err != nil {
					return err
				}
				mint := ContractCall{ABI: supply.ABI, Address: token, FunctionName: "mint", Args: []any{recipient, big.NewInt(800)}}
				void, err := c.SimulateContract(ctx, mint)
				if err != nil || void.Result != nil {
					return fmt.Errorf("void simulation: %+v, %v", void, err)
				}
				if err := checkCall(supplyTx, CallOptions{}, big.NewInt(0)); err != nil {
					return err
				}

				code := "0x602a60005260206000f3" // return 42
				opts := CallOptions{StateOverride: StateOverride{token: {Code: &code}}}
				forced, err := c.SimulateContract(ctx, supply, SimulateContractOptions{CallOptions: opts})
				if err != nil || fmt.Sprint(forced.Result) != "42" {
					return fmt.Errorf("override simulation: %+v, %v", forced, err)
				}
				overrideParams, err := opts.StateOverride.rpcParams()
				if err != nil {
					return err
				}
				estimate, err = c.EstimateGas(ctx, transferTx, opts)
				if err != nil {
					return err
				}
				if err := node.rpc.Call(ctx, "eth_estimateGas", []any{transferTx, "0x1", overrideParams}, &direct); err != nil {
					return err
				}
				if hexutil.EncodeBig(estimate) != direct {
					return fmt.Errorf("overridden gas %s, node %s", estimate, direct)
				}
				unknown := supply
				unknown.Address = recipient
				noRetry := false
				var zeroData *ContractFunctionZeroDataError
				if _, err := c.SimulateContract(ctx, unknown, SimulateContractOptions{RetryEmptyResponse: &noRetry}); !errors.As(err, &zeroData) {
					return fmt.Errorf("undeployed ABI result: %v", err)
				}
				short, stop := context.WithTimeout(ctx, 40*time.Millisecond)
				_, err = c.SimulateContract(short, unknown)
				stop()
				if !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("cancelled simulation: %v", err)
				}
				var reverted bool
				if err := node.rpc.Call(ctx, "evm_revert", []any{snapshot}, &reverted); err != nil || !reverted {
					return fmt.Errorf("snapshot: %t %v", reverted, err)
				}
				if err := node.rpc.Call(ctx, "anvil_dropAllTransactions", []any{}, &ignored); err != nil {
					return err
				}
				if err := node.rpc.Call(ctx, "evm_mine", []any{}, &ignored); err != nil {
					return err
				}
				if err := checkCall(supplyTx, atTwo, big.NewInt(0)); err != nil {
					return err
				}
				if _, err := c.EstimateGas(ctx, sim.Request, atTwo); !errors.As(err, &executionError) {
					return fmt.Errorf("estimated an orphaned transfer: %v", err)
				}
				if _, err := c.SimulateContract(ctx, transfer, SimulateContractOptions{CallOptions: atTwo, Transaction: fields}); !errors.As(err, &executionError) {
					return fmt.Errorf("simulated an orphaned transfer: %v", err)
				}
				return nil
			})
		})
	}
}

func TestSimulationRequestAndRetry(t *testing.T) {
	fixtures := sourceContracts(t)
	chain := Object{"id": 1}
	var sent []Object
	var methods []string
	tr := admissionRequester(func(ctx context.Context, method string, params, out any) error {
		p := params.([]any)
		if p[1] != "0x7" {
			return fmt.Errorf("wrong block %v", p[1])
		}
		sent = append(sent, p[0].(Object))
		methods = append(methods, method)
		value := "0x"
		if len(sent) > 1 {
			value = fmt.Sprintf("0x%064x", 902)
		}
		if method == "eth_estimateGas" {
			value = "0x10000000000000001"
		}
		return json.Unmarshal([]byte(fmt.Sprintf("%q", value)), out)
	})
	s, err := NewClientSet([]Object{chain}, []rpc.Requester{tr})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(t.Context())
	s.SetEvent(Object{"type": "setup", "block": 7})
	c, err := s.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	contract := ContractCall{ABI: fixtures.abis["ERC20"], Address: sourceClientAlice, FunctionName: "totalSupply"}
	sim, err := c.SimulateContract(t.Context(), contract)
	if err != nil || fmt.Sprint(sim.Result) != "902" || len(sent) != 2 || !reflect.DeepEqual(sent[0], sent[1]) {
		t.Fatalf("retried simulation %+v, %v, %v", sim, methods, err)
	}
	tx := Object{"from": sourceClientAlice, "data": "0x", "accessList": []any{}, "nonce": "0x0", "maxFeePerGas": "0xff"}
	estimate, err := c.EstimateGas(t.Context(), tx)
	if err != nil || estimate.BitLen() != 65 || !reflect.DeepEqual(sent[2], tx) {
		t.Fatal(estimate, err, sent)
	}
	for _, opt := range []SimulateContractOptions{{DataSuffix: "0x1"}, {DataSuffix: "xyz"}, {Transaction: Object{"data": "0x00"}}, {Transaction: Object{"input": "0x00"}}, {Transaction: Object{"to": sourceClientAlice}}} {
		if _, err := c.SimulateContract(t.Context(), contract, opt); err == nil {
			t.Fatalf("accepted %+v", opt)
		}
	}
	if _, err := c.EstimateGas(t.Context(), tx, CallOptions{BlockOverrides: Object{"number": "0x1"}}); err == nil || !strings.Contains(err.Error(), "eth_estimateGas") {
		t.Fatal(err)
	}
	for _, invalid := range []Object{{"to": "bad"}, {"from": "0xx"}, {"gasPrice": "0x1", "maxFeePerGas": "0x2"}, {"maxFeePerGas": "0x1", "maxPriorityFeePerGas": "0x3"}} {
		if _, err := c.Call(t.Context(), invalid); err == nil {
			t.Fatal("accepted invalid transaction", invalid)
		}
	}
	if len(sent) != 3 {
		t.Fatalf("invalid requests reached transport: %v", methods)
	}
}

// Public EIP-7702 test vector from the pinned viem signAuthorization action
// for chain 1, nonce zero and the delegate below.
func signedTestAuthorization() Object {
	return Object{
		"chainId": "0x1", "nonce": "0x0", "yParity": "0x0",
		"address": "0x000000000000000000000000000000000000dddd",
		"r":       "0xe845782ce01ab7fe5cdd611138e44b5b055f472383f55290a48f064d03f3aa01",
		"s":       "0x40bc531666196ba8ca9add8cbaac37419b4355592c7909af4a5efb308c2ad818",
	}
}

func TestAuthorizationGasOnNode(t *testing.T) {
	ctx := t.Context()
	net := newSourceClientChain(t, sourceChainContracts{})
	net.mine()
	ch := Object{"id": 1}
	s, err := NewClientSet([]Object{ch}, []rpc.Requester{net.rpc})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	s.SetEvent(Object{"type": "setup", "block": 1})
	c, err := s.GetClient(ch)
	if err != nil {
		t.Fatal(err)
	}
	auth := signedTestAuthorization()
	const authority = "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"
	tx := Object{"from": sourceClientAlice, "authorizationList": []Object{auth}}
	value, err := c.EstimateGas(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tx["to"]; ok {
		t.Fatal("estimation mutated caller's transaction")
	}
	var base, balance string
	net.call("eth_estimateGas", []any{Object{"from": sourceClientAlice, "to": authority, "authorizationList": tx["authorizationList"]}, "0x1"}, &base)
	net.call("eth_getBalance", []any{sourceClientAlice, "latest"}, &balance)
	var extra string
	err = net.rpc.Call(ctx, "eth_estimateGas", []any{Object{"from": sourceClientAlice, "to": auth["address"], "value": balance}, "0x1"}, &extra)
	supplement := big.NewInt(100_000)
	if err == nil {
		supplement, err = hexutil.DecodeBig(extra)
		if err != nil {
			t.Fatal(err)
		}
	}
	want, err := hexutil.DecodeBig(base)
	if err != nil {
		t.Fatal(err)
	}
	want.Add(want, supplement.Lsh(supplement, 1))
	if value.Cmp(want) != 0 {
		t.Fatalf("authorization gas %s, want %s", value, want)
	}
}

func TestAuthorizationGasAdjustment(t *testing.T) {
	ctx := t.Context()
	ch := Object{"id": 1}
	auth := signedTestAuthorization()
	var calls int
	var rejectSecondary bool
	tr := admissionRequester(func(_ context.Context, method string, params, out any) error {
		calls++
		p := params.([]any)
		var result string
		switch method {
		case "eth_estimateGas":
			tx := p[0].(Object)
			if p[1] != "0x7" || len(p) != 3 {
				return fmt.Errorf("lost event block or overrides %v", p)
			}
			if tx["authorizationList"] != nil {
				if tx["to"] != "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf" {
					return fmt.Errorf("inferred authority %v", tx["to"])
				}
				result = "0x100"
			} else {
				if tx["value"] != "0x123" || tx["to"] != auth["address"] || tx["data"] != "0x1234" {
					return fmt.Errorf("secondary estimate transaction %v", tx)
				}
				if rejectSecondary {
					return errors.New("provider declined secondary estimate")
				}
				result = "0xa"
			}
		case "eth_getBalance":
			if !reflect.DeepEqual(p, []any{sourceClientAlice, "latest"}) {
				return fmt.Errorf("unexpected balance request: %v", p)
			}
			result = "0x123"
		default:
			return fmt.Errorf("unexpected method %s", method)
		}
		return json.Unmarshal([]byte(fmt.Sprintf("%q", result)), out)
	})
	s, err := NewClientSet([]Object{ch}, []rpc.Requester{tr})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	s.SetEvent(Object{"type": "setup", "block": 7})
	c, err := s.GetClient(ch)
	if err != nil {
		t.Fatal(err)
	}
	tx := Object{"from": sourceClientAlice, "data": "0x1234", "authorizationList": []Object{auth, auth}}
	for _, want := range []int64{296, 400256} {
		v, err := c.EstimateGas(ctx, tx, CallOptions{StateOverride: StateOverride{}})
		if err != nil || v.Cmp(big.NewInt(want)) != 0 {
			t.Fatal(v, err, want)
		}
		rejectSecondary = true
	}
	auth["yParity"] = "0x2"
	if _, err := c.EstimateGas(ctx, tx); err == nil || calls != 8 {
		t.Fatal(err, calls)
	}
}

func TestDeploylessEVMCalls(t *testing.T) {
	ctx := t.Context()
	net := newSourceClientChain(t, sourceChainContracts{})
	const factory = "0x000000000000000000000000000000000000fa12"
	// Creation code returns a runtime that returns the uint256 value 42.
	const initCode = "0x600a600c600039600a6000f3602a60005260206000f3"
	// The factory copies its embedded creation code to memory and calls CREATE.
	factoryCode := "0x60166010600039601660006000f05000" + initCode[2:]
	var ignored any
	net.call("anvil_setCode", []any{factory, factoryCode}, &ignored)
	net.mine()
	var nonce hexutil.Uint64
	net.call("eth_getTransactionCount", []any{factory, "0x1"}, &nonce)
	target := crypto.CreateAddress(common.HexToAddress(factory), uint64(nonce)).Hex()
	ch := Object{"id": 1}
	s, err := NewClientSet([]Object{ch}, []rpc.Requester{net.rpc})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	s.SetEvent(Object{"type": "setup", "block": 1})
	c, err := s.GetClient(ch)
	if err != nil {
		t.Fatal(err)
	}
	contract := ContractCall{ABI: sourceContracts(t).abis["ERC20"], FunctionName: "totalSupply"}
	for _, val := range []struct {
		target string
		deploy DeploylessCall
	}{
		{"", DeploylessCall{Code: initCode}},
		{target, DeploylessCall{Factory: factory, FactoryData: "0x"}},
	} {
		opt := CallOptions{Deployless: &val.deploy}
		tx := Object{"data": "0x12345678"}
		if val.target != "" {
			tx["to"] = val.target
		}
		result, err := c.Call(ctx, tx, opt)
		if err != nil || result == nil || *result != fmt.Sprintf("0x%064x", 42) {
			t.Fatal("deployless call", result, err)
		}
		if tx["data"] != "0x12345678" {
			t.Fatal("input calldata changed")
		}
		contract.Address = val.target
		sim, err := c.SimulateContract(ctx, contract, SimulateContractOptions{CallOptions: opt})
		if err != nil || fmt.Sprint(sim.Result) != "42" || sim.Request["to"] != nil || sim.Request["data"] == tx["data"] {
			t.Fatal("deployless simulation", sim, err)
		}
		replayed, err := c.Call(ctx, sim.Request)
		if err != nil || replayed == nil || *replayed != *result {
			t.Fatal("prepared wrapper", replayed, err)
		}
	}
	var deployed string
	net.call("eth_getCode", []any{target, "latest"}, &deployed)
	if deployed != "0x" {
		t.Fatal("simulated deployment persisted", deployed)
	}
}
