package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/store"
)

func TestCallbackTransactionActions(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			tr := rpc.New(testanvil.Start(t, "--no-mining"))
			tr.Retries = 0
			tr.RequestsPerSecond = 0
			var accounts []string
			if err := tr.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
				t.Fatal(err)
			}
			chain := Object{"name": "one", "id": 1, "cacheRpcRequests": true}
			cache := adapterStore(t, dialect)
			missingReceipt := make(chan struct{}, 1)
			requester := admissionRequester(func(ctx context.Context, method string, params, out any) error {
				err := tr.Call(ctx, method, params, out)
				if method == "eth_getTransactionReceipt" && err == nil {
					if raw, ok := out.(*json.RawMessage); ok && string(*raw) == "null" {
						select {
						case missingReceipt <- struct{}{}:
						default:
						}
					}
				}
				return err
			})
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{requester}, cache)
			if err != nil {
				t.Fatal(err)
			}
			defer clients.Close(context.Background())
			handler := Handler(func(ctx context.Context, args *HandlerArgs) error {
				c := args.Context.Client
				count, err := c.GetTransactionCount(ctx, accounts[0])
				if err != nil || count != 0 {
					return fmt.Errorf("initial nonce %d: %v", count, err)
				}
				count, err = c.GetBlockTransactionCount(ctx)
				if err != nil || count != 0 {
					return fmt.Errorf("initial block count %d: %v", count, err)
				}
				var snapshot string
				if err := tr.Call(ctx, "evm_snapshot", []any{}, &snapshot); err != nil {
					return err
				}
				var hash string
				if err := tr.Call(ctx, "eth_sendTransaction", []any{Object{"from": accounts[0], "to": accounts[1], "value": "0x1", "gas": "0x5208"}}, &hash); err != nil {
					return err
				}
				pending, err := c.GetTransaction(ctx, TransactionLookup{Hash: hash})
				if err != nil || pending["hash"] != hash || pending["blockNumber"] != nil {
					return fmt.Errorf("pending transaction %v: %v", pending, err)
				}
				if err := clients.WaitCacheWrites(ctx); err != nil {
					return err
				}
				confirm, err := c.GetTransactionConfirmations(ctx, ConfirmationOptions{Hash: hash})
				if err != nil || confirm.Sign() != 0 {
					return fmt.Errorf("pending confirmations %v: %v", confirm, err)
				}
				once := false
				_, err = c.GetTransactionReceipt(ctx, hash, ReceiptOptions{RetryEmptyResponse: &once})
				var absent *rpc.TransactionReceiptNotFoundError
				if !errors.As(err, &absent) {
					return fmt.Errorf("missing receipt: %v", err)
				}
				<-missingReceipt

				mined := make(chan error, 1)
				go func() {
					select {
					case <-missingReceipt:
					case <-ctx.Done():
						mined <- ctx.Err()
						return
					}
					var result any
					mined <- tr.Call(ctx, "evm_mine", []any{}, &result)
				}()
				receipt, err := c.GetTransactionReceipt(ctx, hash)
				if err != nil {
					return err
				}
				if err := <-mined; err != nil {
					return err
				}
				if receipt["transactionHash"] != hash || receipt["status"] != "0x1" || receipt["blockNumber"] != "0x1" {
					return fmt.Errorf("unexpected receipt %v", receipt)
				}
				blockHash := fmt.Sprint(receipt["blockHash"])
				zero := uint64(0)
				for _, lookup := range []TransactionLookup{{Hash: hash}, {Block: TransactionBlock{BlockHash: blockHash}, Index: &zero}, {Block: TransactionBlock{BlockNumber: big.NewInt(1)}, Index: &zero}, {Index: &zero}} {
					tx, err := c.GetTransaction(ctx, lookup)
					if err != nil || tx["hash"] != hash || tx["blockNumber"] != "0x1" {
						return fmt.Errorf("mined transaction %v: %v", tx, err)
					}
				}
				for _, block := range []TransactionBlock{{BlockNumber: big.NewInt(1)}, {BlockHash: blockHash}, {BlockTag: "latest"}} {
					count, err = c.GetBlockTransactionCount(ctx, block)
					if err != nil || count != 1 {
						return fmt.Errorf("mined block count %d: %v", count, err)
					}
				}
				count, err = c.GetTransactionCount(ctx, accounts[0], TransactionBlock{BlockNumber: big.NewInt(1)})
				if err != nil || count != 1 {
					return fmt.Errorf("nonce in original block 1: %d, %v", count, err)
				}
				if err := clients.WaitCacheWrites(ctx); err != nil {
					return err
				}
				key, err := c.cacheKey("eth_getBlockTransactionCountByHash", []any{blockHash})
				if err != nil {
					return err
				}
				if raw, ok, err := cache.CacheGet(ctx, key); err != nil || !ok || string(raw) != `"0x1"` {
					return fmt.Errorf("persisted count for fixed block hash: %s, %t, %v", raw, ok, err)
				}
				count, err = c.GetTransactionCount(ctx, accounts[0], TransactionBlock{BlockNumber: big.NewInt(0)})
				if err != nil || count != 0 {
					return fmt.Errorf("genesis nonce %d: %v", count, err)
				}
				count, err = c.GetTransactionCount(ctx, accounts[0])
				if err != nil || count != 1 {
					return fmt.Errorf("mined nonce %d: %v", count, err)
				}
				var out any
				if err := tr.Call(ctx, "evm_mine", []any{}, &out); err != nil {
					return err
				}
				count, err = c.GetBlockTransactionCount(ctx)
				if err != nil || count != 0 {
					return fmt.Errorf("current empty block %d: %v", count, err)
				}
				for _, opt := range []ConfirmationOptions{{Hash: hash}, {Receipt: receipt}} {
					confirm, err = c.GetTransactionConfirmations(ctx, opt)
					if err != nil || confirm.Cmp(big.NewInt(2)) != 0 {
						return fmt.Errorf("confirmations %v: %v", confirm, err)
					}
				}
				missing := "0x" + strings.Repeat("aa", 32)
				_, err = c.GetTransaction(ctx, TransactionLookup{Hash: missing, RetryEmptyResponse: &once})
				if !errors.Is(err, ErrTransactionNotFound) {
					return fmt.Errorf("missing transaction: %v", err)
				}
				short, stop := context.WithTimeout(ctx, 40*time.Millisecond)
				defer stop()
				_, err = c.GetTransactionConfirmations(short, ConfirmationOptions{Hash: missing})
				if !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("interrupted retry: %v", err)
				}
				var reverted bool
				if err := tr.Call(ctx, "evm_revert", []any{snapshot}, &reverted); err != nil || !reverted {
					return fmt.Errorf("revert chain: %v, %v", reverted, err)
				}
				_, err = c.GetTransactionReceipt(ctx, hash, ReceiptOptions{RetryEmptyResponse: &once})
				if !errors.As(err, &absent) {
					return fmt.Errorf("orphaned receipt stayed visible: %v", err)
				}
				var canonical Object
				if err := tr.Call(ctx, "eth_getTransactionByHash", []any{hash}, &canonical); err != nil {
					return err
				}
				lookedUp, err := c.GetTransaction(ctx, TransactionLookup{Hash: hash, RetryEmptyResponse: &once})
				if canonical == nil && !errors.Is(err, ErrTransactionNotFound) || canonical != nil && (err != nil || lookedUp["blockHash"] != canonical["blockHash"]) {
					return fmt.Errorf("post-reorg transaction %v, node returned %v: %v", lookedUp, canonical, err)
				}
				if err := tr.Call(ctx, "anvil_dropAllTransactions", []any{}, &out); err != nil {
					return err
				}
				if err := tr.Call(ctx, "evm_mine", []any{}, &out); err != nil {
					return err
				}
				height := TransactionBlock{BlockNumber: big.NewInt(1)}
				count, err = c.GetBlockTransactionCount(ctx, height)
				if err != nil || count != 0 {
					return fmt.Errorf("transaction count in replacement block 1: %d, %v", count, err)
				}
				count, err = c.GetTransactionCount(ctx, accounts[0], height)
				if err != nil || count != 0 {
					return fmt.Errorf("nonce in replacement block 1: %d, %v", count, err)
				}
				_, err = c.GetTransaction(ctx, TransactionLookup{Block: height, Index: &zero, RetryEmptyResponse: &once})
				if !errors.Is(err, ErrTransactionNotFound) {
					return fmt.Errorf("transaction in replacement block 1: %v", err)
				}
				return clients.WaitCacheWrites(ctx)
			})
			runClientCallback(t, ctx, dialect, chain, clients, handler)
		})
	}
}

// runClientCallback dispatches a block-one historical event through the
// indexing service, inside the selected backend's business transaction.
func runClientCallback(t *testing.T, ctx context.Context, dialect string, chain Object, clients *ClientSet, handler Handler) {
	t.Helper()
	var invoked bool
	fn := Handler(func(ctx context.Context, args *HandlerArgs) error {
		invoked = true
		return handler(ctx, args)
	})
	cb := Object{"name": "Token:Transfer", "fn": fn, "filter": Object{"type": "log", "include": DefaultFilterInclude("log", false)}}
	business := adapterStore(t, dialect)
	err := business.Apply(ctx, adapterEvent("callback", 1), func(w *store.Writer) error {
		state := &ErrorState{}
		db := NewDatabase(w.Indexing(), state)
		service := mustSemanticService(t, Options{Build: Build{Chains: []Object{chain}, Contracts: []Object{{}}, EventCallbacks: [][]Object{{cb}}, IndexingFunctions: []Object{{"name": "Token:Transfer"}}}, Database: db, Clients: clients, Errors: state})
		return service.ProcessHistoricalEvents(ctx, []Object{semanticEvent(chain, cb, 1)}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !invoked {
		t.Fatal("callback was not invoked")
	}
}

func TestTransactionLookupGuards(t *testing.T) {
	chain := Object{"id": 1, "name": "local"}
	tr := admissionRequester(func(context.Context, string, any, any) error { t.Fatal("invalid selector reached RPC"); return nil })
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{tr})
	if err != nil {
		t.Fatal(err)
	}
	c, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(t.Context())
	zero := uint64(0)
	for _, opt := range []TransactionLookup{{}, {Hash: "a", Index: &zero}, {Hash: "a", Block: TransactionBlock{BlockTag: "latest"}}, {Index: &zero, Block: TransactionBlock{BlockNumber: big.NewInt(-1)}}, {Index: &zero, Block: TransactionBlock{BlockNumber: big.NewInt(0), BlockHash: "a"}}, {Index: &zero, Block: TransactionBlock{BlockTag: "invalid"}}} {
		if _, err := c.GetTransaction(t.Context(), opt); err == nil {
			t.Fatal(opt)
		}
	}
	if _, err := c.GetTransactionCount(t.Context(), "address", TransactionBlock{BlockHash: "a"}); err == nil {
		t.Fatal("account nonce accepted block hash")
	}
	if _, err := c.GetTransactionConfirmations(t.Context(), ConfirmationOptions{}); err == nil {
		t.Fatal("missing selector")
	}
}
