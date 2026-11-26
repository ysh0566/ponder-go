package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/factory"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestMixedSourceProviderReorg(t *testing.T) {
	for _, order := range []schema.Ordering{schema.Isolated, schema.Multichain, schema.Omnichain} {
		for _, backend := range []string{"postgres", "pglite"} {
			t.Run(string(order)+"/"+backend, func(t *testing.T) {
				testMixedSourceProviderReorg(t, order, backend)
			})
		}
	}
}

type mixedSourceBranch struct {
	node, child, blockHash, amount string
}

type mixedSourceNetwork struct {
	config                config.Config
	discovery             factory.Config
	original, replacement mixedSourceBranch
	peer, peerHash        string
	primary, standby      *providerGate
}

func startMixedSourceNetwork(t *testing.T, order schema.Ordering) mixedSourceNetwork {
	t.Helper()
	nodeRPC := reorgNodeRPC{t}
	node := func(id uint64) string { return testanvil.StartChain(t, id, "--no-mining", "--timestamp", "1700000000") }
	original, replacement, peer := node(101), node(101), node(202)
	primary, standby := newProviderGate(t, original, http.StatusServiceUnavailable), newProviderGate(t, replacement, http.StatusTooManyRequests)
	standby.offline.Store(true)
	const parent = "0x1000000000000000000000000000000000000001"
	const oldChild = "0x2000000000000000000000000000000000000001"
	const newChild = "0x2000000000000000000000000000000000000002"
	ping := abi.Event{Type: "event", Name: "Ping", Inputs: []abi.Parameter{{Name: "value", Type: "uint256"}}}
	created := abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{{Name: "child", Type: "address"}}}
	run := abi.Function{Type: "function", Name: "run", StateMutability: "payable"}
	contractABI, err := json.Marshal([]any{ping, run})
	if err != nil {
		t.Fatal(err)
	}
	sender := nodeRPC.call(original, "eth_accounts").([]any)[0].(string)
	send := func(endpoint, to, data string, value int) {
		t.Helper()
		nodeRPC.call(endpoint, "eth_sendTransaction", map[string]any{"from": sender, "to": to, "data": data, "gas": "0x493e0", "value": Hex(uint64(value))})
	}
	logCode := func(value int) string {
		return fmt.Sprintf("0x60%02x6000527f%s60206000a100", value, abi.EventSelector(ping)[2:])
	}
	branch := func(endpoint, child string, offset int) string {
		t.Helper()
		// The parent logs the address supplied in calldata. Both children emit a valid
		// Ping and receive a payable CALL on both forks. Only the discovered child qualifies.
		nodeRPC.call(endpoint, "anvil_setCode", parent, "0x6000356000527f"+abi.EventSelector(created)[2:]+"60206000a100")
		nodeRPC.call(endpoint, "anvil_setCode", oldChild, logCode(7))
		nodeRPC.call(endpoint, "anvil_setCode", newChild, logCode(11))
		send(endpoint, parent, "0x"+strings.Repeat("0", 24)+child[2:], 0)
		nodeRPC.mine(endpoint, offset)
		send(endpoint, oldChild, abi.FunctionSelector(run), 7)
		send(endpoint, newChild, abi.FunctionSelector(run), 11)
		nodeRPC.mine(endpoint, offset+10)
		nodeRPC.mine(endpoint, 60)
		return nodeRPC.head(endpoint, 2)["hash"].(string)
	}
	oldHash := branch(original, oldChild, 10)
	newHash := branch(replacement, newChild, 15)
	if oldHash == newHash || nodeRPC.head(original, 0)["hash"] != nodeRPC.head(replacement, 0)["hash"] {
		t.Fatal("expected different branches with shared genesis")
	}
	nodeRPC.call(peer, "anvil_setCode", oldChild, logCode(23))
	send(peer, oldChild, "0x", 0)
	nodeRPC.mine(peer, 30)
	nodeRPC.mine(peer, 70)
	peerHash := nodeRPC.head(peer, 1)["hash"].(string)

	parameter := "child"
	discovery := &factory.Config{Address: parent, Event: created, Parameter: &parameter}
	cfg := config.Config{Ordering: order,
		Chains: []config.Chain{{Name: "A", ID: 101, RPC: []string{primary.url, standby.url}, PollingIntervalMS: 40},
			{Name: "B", ID: 202, RPC: []string{peer}, PollingIntervalMS: 40}},
		Contracts: []config.Contract{
			{Name: "Child", Chain: "A", Factory: discovery, ABI: contractABI, StartBlock: 1, IncludeCallTraces: true, IncludeTransactionReceipts: true},
			{Name: "Peer", Chain: "B", Address: []string{oldChild}, ABI: contractABI, StartBlock: 1, IncludeTransactionReceipts: true}},
		Accounts: []config.AccountSource{{Name: "Wallet", Chain: "A", Factory: discovery, StartBlock: 1}},
	}
	return mixedSourceNetwork{
		config: cfg, discovery: *discovery, primary: primary, standby: standby, peer: peer, peerHash: peerHash,
		original:    mixedSourceBranch{node: original, child: oldChild, blockHash: oldHash, amount: "7"},
		replacement: mixedSourceBranch{node: replacement, child: newChild, blockHash: newHash, amount: "11"},
	}
}

func testMixedSourceProviderReorg(t *testing.T, order schema.Ordering, backend string) {
	ctx := t.Context()
	network := startMixedSourceNetwork(t, order)
	model := schema.Schema{Tables: []schema.Table{{Name: "deliveries", PrimaryKey: []string{"chain_id", "event"}, Columns: []schema.Column{
		{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "event", Type: "text", NotNull: true},
		{Name: "block_hash", Type: "text", NotNull: true}, {Name: "payload", Type: "text", NotNull: true},
	}}}}
	var mu sync.Mutex
	var chainCalls, peerCalls []string
	names := []string{"Wallet:transaction:to", "Child:Ping", "Child.run()", "Wallet:transfer:to"}
	handlers := map[string]Handler{}
	for _, name := range append(slices.Clone(names), "Peer:Ping") {
		handlers[name] = func(ctx context.Context, c *Context) error {
			event := c.Event
			var payload string
			switch event.Name {
			case "Child:Ping", "Peer:Ping":
				if event.Receipt["status"] != "0x1" {
					return fmt.Errorf("missing log receipt for %s", event.Name)
				}
				payload = fmt.Sprint(event.Args.(map[string]any)["value"])
			case "Child.run()":
				if event.Receipt["status"] != "0x1" {
					return fmt.Errorf("missing CALL receipt")
				}
				payload = fmt.Sprint(event.Trace["to"])
			case "Wallet:transaction:to":
				payload = fmt.Sprint(event.Transaction["to"])
			case "Wallet:transfer:to":
				payload = fmt.Sprint(event.Transfer["value"])
			}
			if event.Name == "Peer:Ping" && order != schema.Isolated {
				// Global rollback must rerun this read against the replacement branch.
				row, err := c.DB.Find(ctx, "deliveries", store.Row{"chain_id": 101, "event": "Child:Ping"})
				if err != nil {
					return err
				}
				if row == nil {
					return fmt.Errorf("peer callback ran before the child log")
				}
				payload += "/" + fmt.Sprint(row["payload"])
			}
			if err := c.DB.Write(ctx, "deliveries", store.Row{"chain_id": c.Chain.ID, "event": event.Name, "block_hash": event.Block.Hash, "payload": payload}, store.Insert); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if c.Chain.ID == 101 {
				chainCalls = append(chainCalls, event.Name+"@"+event.Block.Hash)
			} else {
				peerCalls = append(peerCalls, event.Block.Hash)
			}
			return nil
		}
	}

	ns := fmt.Sprintf("ponder_go_mixfork_%x", time.Now().UnixNano())
	dir := filepath.Join(t.TempDir(), "db")
	var db *store.Store
	var app *Application
	var routes *query.Server
	open := func() {
		t.Helper()
		b := stagedBackend(t, ctx, backend, dir)
		t.Cleanup(func() { _ = b.Close() })
		var err error
		db, err = store.New(ctx, b, model, ns, order)
		if err != nil {
			t.Fatal(err)
		}
		indexer, err := NewIndexerWithOptions(network.config, db, handlers, Options{DisableCache: backend == "pglite"})
		if err != nil {
			t.Fatal(err)
		}
		routes = query.New(db)
		app, err = LaunchApplication(ctx, indexer, routes, "127.0.0.1", 0)
		if err != nil {
			t.Fatal(err)
		}
		instance := app
		t.Cleanup(func() { _ = instance.Shutdown(context.Background()) })
	}
	awaitReady := func(want bool) {
		t.Helper()
		deadline := time.NewTimer(45 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for routes.Ready.Load() != want {
			select {
			case <-app.done:
				t.Fatalf("indexer stopped: %v", app.Wait())
			case <-deadline.C:
				t.Fatalf("indexer readiness did not reach %t", want)
			case <-tick.C:
			}
		}
	}
	check := func(branch mixedSourceBranch, branches []string) map[string]string {
		t.Helper()
		peerPayload := "23"
		wantPeerCalls := []string{network.peerHash}
		if order != schema.Isolated {
			peerPayload += "/" + branch.amount
			if len(branches) == 2 {
				wantPeerCalls = append(wantPeerCalls, network.peerHash)
			}
		}
		want := map[string]string{"Wallet:transaction:to": branch.child, "Child:Ping": branch.amount, "Child.run()": branch.child, "Wallet:transfer:to": branch.amount, "Peer:Ping": peerPayload}
		rows, err := db.Read(ctx, "deliveries")
		if err != nil || len(rows.Rows) != len(want) {
			t.Fatalf("committed deliveries: %v, %v", rows, err)
		}
		for _, row := range rows.Rows {
			event := fmt.Sprint(row["event"])
			wantChain, wantHash := "101", branch.blockHash
			if event == "Peer:Ping" {
				wantChain, wantHash = "202", network.peerHash
			}
			payload, expected := want[event]
			if !expected || fmt.Sprint(row["chain_id"]) != wantChain || row["block_hash"] != wantHash || row["payload"] != payload {
				t.Fatalf("wrong delivery %v, want chain %s, hash %s, payload %q", row, wantChain, wantHash, payload)
			}
			delete(want, event)
		}
		from := uint64(1)
		for _, name := range []string{"Wallet", "Child"} {
			f := network.discovery
			f.ChainID, f.SourceID, f.FromBlock = 101, name, &from
			filter, err := factory.Build(f)
			if err != nil {
				t.Fatal(err)
			}
			children, err := db.FactoryChildren(ctx, filter.ID, 101, 2)
			if err != nil || len(children) != 1 || children[branch.child] != 1 {
				t.Fatalf("factory %s children %v, want %s at block one: %v", name, children, branch.child, err)
			}
		}
		progress, err := db.Progress(ctx)
		if err != nil || len(progress) != 2 {
			t.Fatalf("progress: %v, %v", progress, err)
		}
		for chain, provider := range map[string]string{"101": branch.node, "202": network.peer} {
			cp, err := checkpoint.Decode(progress[chain])
			if err != nil || fmt.Sprint(cp.ChainID) != chain {
				t.Fatalf("wrong checkpoint for chain %s: %v, %v", chain, cp, err)
			}
			block := (reorgNodeRPC{t}).head(provider, cp.BlockNumber)
			timestamp, err := Number(block["timestamp"].(string))
			if err != nil || timestamp != cp.BlockTimestamp {
				t.Fatalf("checkpoint time differs from chain %s at %d", chain, cp.BlockNumber)
			}
			saved, err := db.SyncedBlock(ctx, cp.ChainID, cp.BlockNumber)
			if err != nil || saved == nil || saved.Hash != block["hash"] {
				t.Fatalf("wrong persisted header at %s/%d: %v, %v", chain, cp.BlockNumber, saved, err)
			}
		}
		var wantCalls []string
		for _, block := range branches {
			for _, name := range names {
				wantCalls = append(wantCalls, name+"@"+block)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(chainCalls, wantCalls) || !slices.Equal(peerCalls, wantPeerCalls) {
			t.Fatalf("chain calls %v, want %v, peer calls %v, want %v", chainCalls, wantCalls, peerCalls, wantPeerCalls)
		}
		return progress
	}

	open()
	awaitReady(true)
	initialBranch := []string{network.original.blockHash}
	initial := check(network.original, initialBranch)
	primaryRejections := network.primary.rejected.Load()
	standbyRejections := network.standby.rejected.Load()
	network.primary.offline.Store(true)
	awaitReady(false)
	if after := check(network.original, initialBranch); !maps.Equal(initial, after) || network.primary.rejected.Load() == primaryRejections || network.standby.rejected.Load() == standbyRejections {
		t.Fatal("pool outage or unchanged checkpoints not observed")
	}
	network.standby.offline.Store(false)
	awaitReady(true)
	replayed := []string{network.original.blockHash, network.replacement.blockHash}
	after := check(network.replacement, replayed)
	if network.standby.accepted.Load() == 0 || initial["202"] != after["202"] {
		t.Fatal("provider switch or per-chain checkpoints differ", initial, after)
	}
	if err := app.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Backend.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	awaitReady(true)
	if final := check(network.replacement, replayed); !maps.Equal(after, final) {
		t.Fatal("cold reopen changed checkpoints", after, final)
	}
}
