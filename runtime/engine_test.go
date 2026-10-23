package runtime

import (
	"context"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
	"time"
)

const transferABI = `[{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]}]`

func localChain(t *testing.T) (*rpc.Client, string) {
	t.Helper()
	if _, e := exec.LookPath("anvil"); e != nil {
		t.Fatal("existing Anvil is required for this explicit local-chain suite")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "anvil", "--silent", "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--chain-id", "1")
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := rpc.New(url)
	client.Retries = 0
	client.RequestsPerSecond = 0
	deadline := time.Now().Add(10 * time.Second)
	for {
		var chain string
		if client.Call(ctx, "eth_chainId", []any{}, &chain) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Anvil failed to start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return client, url
}
func TestAnvilPostgresVertical(t *testing.T) {
	testAnvilVertical(t, "postgres")
}
func TestAnvilPGliteVertical(t *testing.T) {
	testAnvilVertical(t, "pglite")
}
func TestStagedAnvilVertical(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) { testAnvilVertical(t, dialect, Options{DisableCache: disabled}) })
		}
	}
}
func testAnvilVertical(t *testing.T, dialect string, stagedOptions ...Options) {
	url := os.Getenv("PONDER_TEST_POSTGRES")
	if dialect == "postgres" && url == "" {
		t.Skip("explicit task PostgreSQL URL required; no parity pass")
	}
	ctx := context.Background()
	client, rpcURL := localChain(t)
	var backend store.Backend
	var e error
	if dialect == "postgres" {
		backend, e = store.OpenPostgres(ctx, url)
	} else {
		backend, e = store.OpenPGlite(ctx, "memory://")
	}
	if e != nil {
		t.Fatal(e)
	}
	defer backend.Close()
	model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
	var s *store.Store
	var lifecycle *store.Lifecycle
	ns := fmt.Sprintf("ponder_go_chain_%x", time.Now().UnixNano())
	if len(stagedOptions) > 0 {
		lifecycle, e = store.NewLifecycle(backend, model, ns, schema.Multichain, store.LifecycleOptions{})
		if e == nil {
			s = lifecycle.RuntimeStore()
			defer lifecycle.Close()
		}
	} else {
		s, e = store.New(ctx, backend, model, ns, schema.Multichain)
	}
	if e != nil {
		t.Fatal(e)
	}
	address := "0x1000000000000000000000000000000000000001"
	recipient := "0x00000000000000000000000000000000000000aa"
	sender := "0x00000000000000000000000000000000000000bb"
	word := func(a string) string { return strings.Repeat("0", 24) + strings.TrimPrefix(a, "0x") }
	signature := "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	// A local EVM contract emits an ERC-20 Transfer with a uint256 from calldata.
	code := "0x6000356000527f" + word(recipient) + "7f" + word(sender) + "7f" + signature + "60206000a300"
	var result any
	if e = client.Call(ctx, "anvil_setCode", []any{address, code}, &result); e != nil {
		t.Fatal(e)
	}
	var accounts []string
	if e = client.Call(ctx, "eth_accounts", []any{}, &accounts); e != nil {
		t.Fatal(e)
	}
	var snapshot string
	if e = client.Call(ctx, "evm_snapshot", []any{}, &snapshot); e != nil {
		t.Fatal(e)
	}
	send := func(amount int64) {
		t.Helper()
		var hash string
		if e := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": address, "gas": "0x186a0", "data": fmt.Sprintf("0x%064x", amount)}}, &hash); e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			var receipt map[string]any
			if e := client.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); e != nil {
				t.Fatal(e)
			}
			if receipt != nil {
				if receipt["status"] != "0x1" {
					t.Fatalf("EVM transaction failed: %v", receipt)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("transaction was not mined")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	send(8)
	logCalls, setupCalls, blockCalls := 0, 0, 0
	handlers := map[string]Handler{
		"Token:setup": func(ctx context.Context, c *Context) error {
			setupCalls++
			return c.DB.Write(ctx, "account", store.Row{"id": recipient, "balance": "0"}, store.Insert)
		},
		"Token:Transfer": func(ctx context.Context, c *Context) error {
			logCalls++
			if c.Event.Receipt == nil || c.Event.Receipt["status"] != "0x1" {
				return fmt.Errorf("missing successful receipt")
			}
			args := c.Event.Args.(map[string]any)
			if args["to"] != recipient || args["from"] != sender {
				return fmt.Errorf("address normalization mismatch")
			}
			row, e := c.DB.Find(ctx, "account", store.Row{"id": recipient})
			if e != nil {
				return e
			}
			balance := new(big.Int)
			if row != nil {
				balance.SetString(fmt.Sprint(row["balance"]), 10)
			}
			balance.Add(balance, args["value"].(*big.Int))
			return c.DB.Write(ctx, "account", store.Row{"id": recipient, "balance": balance.String()}, store.Upsert)
		},
		"heads:block": func(context.Context, *Context) error { blockCalls++; return nil },
	}
	cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{rpcURL}}}, Contracts: []config.Contract{{Name: "Token", Chain: "local", ABI: []byte(transferABI), Address: []string{address}, StartBlock: 0, IncludeTransactionReceipts: true}}, Blocks: []config.BlockSource{{Name: "heads", Chain: "local", Interval: 1}}}
	var engine *Engine
	if lifecycle != nil {
		engine, e = NewStaged(cfg, lifecycle, handlers, StagedOptions{Options: stagedOptions[0], BuildID: "native-e2e"})
	} else {
		engine, e = New(cfg, s, handlers)
	}
	if e != nil {
		t.Fatal(e)
	}
	engine.Client.RequestsPerSecond = 0
	var notifications store.NotificationListener
	if lifecycle != nil {
		notifications, e = store.Listen(ctx, backend, lifecycle.Actions().LiveQueryChannel())
		if e != nil {
			t.Fatal(e)
		}
		defer notifications.Close()
	}
	waitChange := func() {
		t.Helper()
		if notifications == nil {
			return
		}
		c, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		n, e := notifications.Wait(c)
		if e != nil || n.Payload != `["account"]` {
			t.Fatal("realtime notification", n, e)
		}
	}
	noChange := func() {
		t.Helper()
		if notifications == nil {
			return
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		n, e := notifications.Wait(c)
		if e != context.DeadlineExceeded {
			t.Fatal("unexpected duplicate/intermediate notification", n, e)
		}
	}
	read := func() string {
		t.Helper()
		r, e := s.Read(ctx, "account")
		if e != nil || len(r.Rows) != 1 {
			t.Fatalf("%v %v", r, e)
		}
		return fmt.Sprint(r.Rows[0]["balance"])
	}
	if e = engine.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if read() != "8" || setupCalls != 1 || logCalls != 1 || blockCalls != 2 {
		t.Fatalf("balance %s setup/log/block %d/%d/%d", read(), setupCalls, logCalls, blockCalls)
	}
	waitChange()
	noChange()
	if lifecycle != nil {
		if engine.Phase() != "realtime" {
			t.Fatal("handoff not complete")
		}
		states, e := lifecycle.Actions().Checkpoints(ctx)
		if e != nil || len(states) != 1 {
			t.Fatal(states, e)
		}
		cp, e := checkpoint.Decode(states[0].SafeCheckpoint)
		if e != nil || cp.BlockNumber != 0 {
			t.Fatal("history safe did not remain before live", states, e)
		}
	}
	if e = engine.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if logCalls != 1 {
		t.Fatal("replay executed duplicate handler")
	}
	noChange()
	checkProtocol := func() string {
		t.Helper()
		if lifecycle != nil && stagedOptions[0].DisableCache {
			if engine.cache != nil {
				t.Fatal("disabled cache attached")
			}
			return "uncached"
		}
		for table, want := range map[string]int{"blocks": 2, "logs": 1, "transactions": 1, "transaction_receipts": 1} {
			rows, err := engine.cache.Rows(ctx, table, 1, 0, 1)
			if err != nil || len(rows.Rows) != want {
				t.Fatalf("actual RPC protocol %s: %#v %v", table, rows, err)
			}
		}
		row, err := backend.Query(ctx, "SELECT block_hash FROM "+schema.Quote(engine.cache.Namespace())+".logs WHERE chain_id=1 AND block_number=1")
		if err != nil || len(row.Rows) != 1 {
			t.Fatal(row, err)
		}
		return fmt.Sprint(row.Rows[0]["block_hash"])
	}
	oldHash := checkProtocol()
	var reverted bool
	if e = client.Call(ctx, "evm_revert", []any{snapshot}, &reverted); e != nil || !reverted {
		t.Fatalf("%v %v", reverted, e)
	}
	send(5)
	if e = engine.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if read() != "5" || logCalls != 2 || setupCalls != 1 {
		t.Fatalf("reorg balance %s calls %d/%d", read(), logCalls, setupCalls)
	}
	waitChange()
	noChange()
	if newHash := checkProtocol(); newHash == oldHash && oldHash != "uncached" {
		t.Fatal("protocol cache retained orphaned log")
	}
	// The same JSON-RPC client also supports WebSocket requests and head subscriptions.
	ws := rpc.New(strings.Replace(rpcURL, "http://", "ws://", 1))
	ws.Retries = 0
	ws.RequestsPerSecond = 0
	var chain string
	if e = ws.Call(ctx, "eth_chainId", []any{}, &chain); e != nil || chain != "0x1" {
		t.Fatalf("WS %s %v", chain, e)
	}
	subctx, cancel := context.WithCancel(ctx)
	defer cancel()
	heads, e := ws.Subscribe(subctx, "newHeads")
	if e != nil {
		t.Fatal(e)
	}
	if e = client.Call(ctx, "evm_mine", []any{}, &result); e != nil {
		t.Fatal(e)
	}
	select {
	case head := <-heads:
		if len(head) == 0 {
			t.Fatal("empty head")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("head subscription timed out")
	}
}
