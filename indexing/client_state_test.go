package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestCallbackHistoricalFeesAndProof(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			node := rpc.New(testanvil.Start(t, "--no-mining"))
			node.RequestsPerSecond = 0
			call := func(method string, params []any, out any) {
				t.Helper()
				if err := node.Call(ctx, method, params, out); err != nil {
					t.Fatalf("%s: %v", method, err)
				}
			}
			var accounts []string
			call("eth_accounts", []any{}, &accounts)
			// The constructor stores 42 in slot zero. Runtime code stores the
			// first calldata word in that slot whenever a transaction calls it.
			value := "0x10000000000000001"
			var tx string
			var ignored any
			call("eth_sendTransaction", []any{Object{"from": accounts[0], "data": "0x602a6000556007601160003960076000f360003560005500", "gas": "0x40000", "value": value}}, &tx)
			call("evm_mine", []any{}, &ignored)
			var receipt struct{ ContractAddress string }
			call("eth_getTransactionReceipt", []any{tx}, &receipt)
			address := receipt.ContractAddress
			var block struct{ StateRoot string }
			call("eth_getBlockByNumber", []any{"0x1", false}, &block)
			var snapshot string
			call("evm_snapshot", []any{}, &snapshot)
			call("eth_sendTransaction", []any{Object{"from": accounts[0], "to": address, "data": fmt.Sprintf("0x%064x", 71), "gas": "0x20000"}}, &tx)
			call("evm_mine", []any{}, &ignored)

			chain := Object{"id": 1, "name": "local", "cacheRpcRequests": true}
			cache := adapterStore(t, dialect)
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{node}, cache)
			if err != nil {
				t.Fatal(err)
			}
			defer clients.Close(context.Background())
			keys := []string{"0x" + strings.Repeat("00", 32)}
			runClientCallback(t, ctx, dialect, chain, clients, func(ctx context.Context, args *HandlerArgs) error {
				c := args.Context.Client
				proof, err := c.GetProof(ctx, address, keys)
				if err != nil {
					return err
				}
				balance, _ := hexutil.DecodeBig(value)
				if proof.Address != address || proof.Nonce != 1 || proof.Balance.Cmp(balance) != 0 || len(proof.AccountProof) == 0 || len(proof.StorageProof) != 1 {
					return fmt.Errorf("event account proof: %+v", proof)
				}
				slot := proof.StorageProof[0]
				if slot.Key != keys[0] || slot.Value.Cmp(big.NewInt(42)) != 0 || len(slot.Proof) == 0 {
					return fmt.Errorf("event storage proof: %+v", slot)
				}
				for _, pair := range [][2]string{{proof.AccountProof[0], block.StateRoot}, {slot.Proof[0], proof.StorageHash}} {
					b, err := hexutil.Decode(pair[0])
					if err != nil || crypto.Keccak256Hash(b).Hex() != pair[1] {
						return fmt.Errorf("unexpected proof root %s: %v", pair[1], err)
					}
				}
				history, err := c.GetFeeHistory(ctx, 1, []float64{0, 50.5, 100})
				if err != nil {
					return err
				}
				if history.OldestBlock.Cmp(big.NewInt(1)) != 0 || len(history.BaseFeePerGas) != 2 || len(history.GasUsedRatio) != 1 || history.GasUsedRatio[0] <= 0 || len(history.Reward) != 1 || len(history.Reward[0]) != 3 {
					return fmt.Errorf("event fee history: %+v", history)
				}
				var direct struct {
					BaseFeePerGas []string
					Reward        [][]string
				}
				if err := node.Call(ctx, "eth_feeHistory", []any{"0x1", "0x1", []float64{0, 50.5, 100}}, &direct); err != nil {
					return err
				}
				if fmt.Sprint(direct.BaseFeePerGas) != fmt.Sprint([]string{hexutil.EncodeBig(history.BaseFeePerGas[0]), hexutil.EncodeBig(history.BaseFeePerGas[1])}) || direct.Reward[0][1] != hexutil.EncodeBig(history.Reward[0][1]) {
					return fmt.Errorf("fee quantities differ from node: %+v", direct)
				}
				atTwo := BlockOptions{BlockNumber: big.NewInt(2)}
				for _, opt := range []BlockOptions{atTwo, {Cache: "immutable"}} {
					p, err := c.GetProof(ctx, address, keys, opt)
					if err != nil || len(p.StorageProof) != 1 || p.StorageProof[0].Value.Cmp(big.NewInt(71)) != 0 {
						return fmt.Errorf("proof of updated slot: %+v, %v", p, err)
					}
					f, err := c.GetFeeHistory(ctx, 1, nil, opt)
					if err != nil || f.OldestBlock.Cmp(big.NewInt(2)) != 0 || len(f.GasUsedRatio) != 1 || f.GasUsedRatio[0] <= 0 {
						return fmt.Errorf("fee history after call: %+v, %v", f, err)
					}
				}
				if err := clients.WaitCacheWrites(ctx); err != nil {
					return err
				}
				var reverted bool
				if err := node.Call(ctx, "evm_revert", []any{snapshot}, &reverted); err != nil || !reverted {
					return fmt.Errorf("chain revert: %t, %v", reverted, err)
				}
				if err := node.Call(ctx, "anvil_dropAllTransactions", []any{}, &ignored); err != nil {
					return err
				}
				if err := node.Call(ctx, "evm_mine", []any{}, &ignored); err != nil {
					return err
				}
				replacement, err := c.GetProof(ctx, address, keys, atTwo)
				if err != nil || len(replacement.StorageProof) != 1 || replacement.StorageProof[0].Value.Cmp(big.NewInt(42)) != 0 {
					return fmt.Errorf("replacement slot: %+v, %v", replacement, err)
				}
				newFees, err := c.GetFeeHistory(ctx, 1, nil, atTwo)
				if err != nil || len(newFees.GasUsedRatio) != 1 || newFees.GasUsedRatio[0] != 0 {
					return fmt.Errorf("replacement fee history: %+v, %v", newFees, err)
				}
				fixed, err := c.GetProof(ctx, address, keys, BlockOptions{Cache: "immutable"})
				if err != nil || len(fixed.StorageProof) != 1 || fixed.StorageProof[0].Value.Cmp(big.NewInt(71)) != 0 {
					return fmt.Errorf("immutable cached proof: %+v, %v", fixed, err)
				}
				accountOnly, err := c.GetProof(ctx, accounts[0], nil)
				if err != nil || len(accountOnly.StorageProof) != 0 || accountOnly.Nonce != 1 {
					return fmt.Errorf("account-only event proof: %+v, %v", accountOnly, err)
				}
				return nil
			})
		})
	}
}

func TestFeeAndProofResponseShapes(t *testing.T) {
	chain := Object{"id": 1}
	var reply string
	handler := admissionRequester(func(ctx context.Context, method string, params, out any) error {
		return json.Unmarshal([]byte(reply), out)
	})
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{handler})
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(t.Context())
	clients.SetEvent(Object{"type": "setup", "block": 1})
	c, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	reply = `{"oldestBlock":"0x1","baseFeePerGas":["0x10000000000000001","0x0"],"gasUsedRatio":[0.25]}`
	h, err := c.GetFeeHistory(t.Context(), 1, nil)
	if err != nil || h.Reward != nil || h.BaseFeePerGas[0].BitLen() != 65 {
		t.Fatalf("missing optional reward or large fee: %+v, %v", h, err)
	}
	for _, reply = range []string{"null", `{"baseFeePerGas":["0xzz"],"oldestBlock":"0x1","gasUsedRatio":[]}`} {
		if _, err := c.GetFeeHistory(t.Context(), 1, nil); err == nil {
			t.Fatalf("accepted malformed fee history %s", reply)
		}
	}
	for _, reply = range []string{"null", `{"address":"a","codeHash":"c","storageHash":"s","accountProof":[],"balance":"0x0","nonce":"0x1","storageProof":[{"key":"k","proof":[]}]}`} {
		if _, err := c.GetProof(t.Context(), "account", nil); err == nil {
			t.Fatalf("accepted malformed proof %s", reply)
		}
	}
}
