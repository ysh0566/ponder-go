package indexing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	ponderabi "ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/internal/testanvil"
	"ponder.local/ponder-go/rpc"
)

const sourceClientAlice = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"

type sourceChainContracts struct {
	code map[string]string
	abis map[string]ponderabi.ABI
}

func sourceContracts(t *testing.T) sourceChainContracts {
	t.Helper()
	root := os.Getenv("PONDER_UPSTREAM")
	if root == "" {
		root = "../../ponder-upstream"
	}
	fixtures := filepath.Join(root, "packages/core/src/_test")
	c := sourceChainContracts{map[string]string{}, map[string]ponderabi.ABI{}}
	for _, name := range []string{"ERC20", "Revert"} {
		file := filepath.Join(fixtures, "contracts/out", name+".sol", name+".json")
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var artifact struct {
			ABI      json.RawMessage
			Bytecode struct{ Object string }
		}
		if err = json.Unmarshal(raw, &artifact); err != nil {
			t.Fatal(err)
		}
		c.abis[name], err = ponderabi.Parse(string(artifact.ABI))
		if err != nil {
			t.Fatal(err)
		}
		c.code[name] = artifact.Bytecode.Object
	}
	simulation, err := os.ReadFile(filepath.Join(fixtures, "simulate.ts"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?s)abi: multicall3Abi,\s*bytecode:\s*"(0x[0-9a-fA-F]+)"`).FindSubmatch(simulation)
	if len(match) != 2 {
		t.Fatal("missing upstream multicall deployment bytecode")
	}
	c.code["multicall"] = string(match[1])
	return c
}

type sourceClientChain struct {
	t         *testing.T
	rpc       *rpc.Client
	contracts sourceChainContracts
}

func newSourceClientChain(t *testing.T, fixtures sourceChainContracts) *sourceClientChain {
	client := rpc.New(testanvil.Start(t, "--no-mining"))
	client.Retries = 0
	client.RequestsPerSecond = 0
	return &sourceClientChain{t, client, fixtures}
}

func (s *sourceClientChain) call(method string, params []any, out any) {
	s.t.Helper()
	if err := s.rpc.Call(s.t.Context(), method, params, out); err != nil {
		s.t.Fatal(method, err)
	}
}
func (s *sourceClientChain) mine() {
	s.t.Helper()
	var out json.RawMessage
	s.call("anvil_mine", []any{"0x1"}, &out)
}
func (s *sourceClientChain) block() Object {
	s.t.Helper()
	var b Object
	s.call("eth_getBlockByNumber", []any{"latest", true}, &b)
	return b
}
func (s *sourceClientChain) send(to, data string) Object {
	s.t.Helper()
	tx := Object{"from": sourceClientAlice, "data": data, "gas": "0x800000"}
	if to != "" {
		tx["to"] = to
	}
	var hash string
	s.call("eth_sendTransaction", []any{tx}, &hash)
	s.mine()
	var receipt Object
	s.call("eth_getTransactionReceipt", []any{hash}, &receipt)
	if receipt["status"] != "0x1" {
		s.t.Fatalf("unsuccessful test transaction %v", receipt)
	}
	return receipt
}
func (s *sourceClientChain) deploy(name string) string {
	s.t.Helper()
	data := s.contracts.code[name]
	if name == "ERC20" {
		args, err := s.contracts.abis[name].Constructor.Inputs.Pack("name", "symbol", uint8(18))
		if err != nil {
			s.t.Fatal(err)
		}
		data += hex.EncodeToString(args)
	}
	return s.send("", data)["contractAddress"].(string)
}
func (s *sourceClientChain) calldata(address, name string, args ...any) string {
	s.t.Helper()
	abi := s.contracts.abis["ERC20"]
	if name == "revert" {
		abi = s.contracts.abis["Revert"]
	}
	if name == "aggregate3" {
		abi = aggregate3ABI
	}
	data, _, err := encodeContractCall(ContractCall{ABI: abi, Address: address, FunctionName: name, Args: args})
	if err != nil {
		s.t.Fatal(err)
	}
	return data
}
func (s *sourceClientChain) mint(address string) {
	s.t.Helper()
	s.send(address, s.calldata(address, "mint", sourceClientAlice, big.NewInt(1000000000000000000)))
}

type observedClientRequest struct {
	Method string
	Params []any
}

type sourceClientRPC struct {
	rpc   rpc.Requester
	calls []observedClientRequest
	first json.RawMessage
}

func (r *sourceClientRPC) Call(ctx context.Context, method string, params, out any) error {
	r.calls = append(r.calls, observedClientRequest{Method: method, Params: params.([]any)})
	if r.first != nil {
		v := r.first
		r.first = nil
		return json.Unmarshal(v, out)
	}
	return r.rpc.Call(ctx, method, params, out)
}
