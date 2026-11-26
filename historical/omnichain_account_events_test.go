package historical

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type omniAccountChain struct {
	url, wallet, other, worker string
	tx, hash                   map[uint64]string
}

func mineOmniAccountChain(t *testing.T, id uint64, times []uint64) omniAccountChain {
	t.Helper()
	o := omniAccountChain{url: testanvil.StartChain(t, id, "--no-mining", "--timestamp", "1700000000"), worker: "0x1111111111111111111111111111111111111111", tx: map[uint64]string{}, hash: map[uint64]string{}}
	client := rpc.New(o.url)
	call := func(method string, args ...any) any {
		t.Helper()
		if args == nil {
			args = []any{}
		}
		var result any
		if err := client.Call(t.Context(), method, args, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	accounts := call("eth_accounts").([]any)
	o.wallet, o.other = accounts[1].(string), accounts[0].(string)
	// Return the uint256 value 42 for the fixture's payable function.
	call("anvil_setCode", o.worker, "0x602a60005260206000f3")
	for i, offset := range times {
		n := uint64(i + 1)
		call("evm_setNextBlockTimestamp", 1700000000+offset)
		switch n {
		case 1:
			o.tx[n] = call("eth_sendTransaction", map[string]any{"from": o.other, "to": o.wallet, "value": "0xa", "gas": "0x5208"}).(string)
		case 2:
			data := crypto.Keccak256Hash([]byte("record(uint256)")).Hex()[:10] + fmt.Sprintf("%064x", 7)
			o.tx[n] = call("eth_sendTransaction", map[string]any{"from": o.wallet, "to": o.worker, "value": "0xd", "gas": "0x186a0", "data": data}).(string)
		}
		call("evm_mine")
		raw := call("eth_getBlockByNumber", fmt.Sprintf("0x%x", n), false).(map[string]any)
		o.hash[n] = raw["hash"].(string)
	}
	return o
}

func TestOmnichainStaticAccountEvents(t *testing.T) {
	chains := map[uint64]omniAccountChain{101: mineOmniAccountChain(t, 101, []uint64{10, 40, 70}), 202: mineOmniAccountChain(t, 202, []uint64{20, 30, 75})}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			fail := true
			var calls []string
			h := func(name string) indexing.Handler {
				return func(ctx context.Context, a *indexing.HandlerArgs) error {
					id := finiteChain(a)
					n, err := continuousNumber(a)
					if err != nil {
						return err
					}
					c := chains[id]
					cp, err := checkpoint.Decode(fmt.Sprint(a.Event.Get("id")))
					if err != nil || cp.ChainID != id || cp.BlockNumber != n {
						return fmt.Errorf("invalid checkpoint: %+v %v", cp, err)
					}
					if name != "block" {
						hash, err := factoryTransactionProperty(a, "transaction", "hash")
						if err != nil || hash != c.tx[n] {
							return fmt.Errorf("transaction %v, want %s: %v", hash, c.tx[n], err)
						}
						status, err := factoryTransactionProperty(a, "transactionReceipt", "status")
						if err != nil || status != "success" {
							return fmt.Errorf("receipt status %v: %v", status, err)
						}
						switch {
						case name == "call":
							if fmt.Sprint(a.Event.Get("args")) != "[7]" || fmt.Sprint(a.Event.Get("result")) != "42" {
								return fmt.Errorf("contract CALL args=%v result=%v", a.Event.Get("args"), a.Event.Get("result"))
							}
						case strings.HasPrefix(name, "transfer"):
							from, to, value := c.other, c.wallet, "10"
							if n == 2 {
								from, to, value = c.wallet, c.worker, "13"
							}
							transfer, ok := a.Event.Get("transfer").(map[string]any)
							if !ok {
								return fmt.Errorf("missing transfer projection: %T", a.Event.Get("transfer"))
							}
							if !strings.EqualFold(fmt.Sprint(transfer["from"]), from) || !strings.EqualFold(fmt.Sprint(transfer["to"]), to) || fmt.Sprint(transfer["value"]) != value {
								return fmt.Errorf("unexpected transfer: %v", transfer)
							}
						}
					}
					key := fmt.Sprintf("%d/%d/%s", id, n, name)
					row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
					if err != nil {
						return err
					}
					if row == nil {
						_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "value": key}, store.Conflict{})
					} else {
						_, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"value": fmt.Sprint(row["value"]) + "|" + key})
					}
					if err != nil {
						return err
					}
					calls = append(calls, key)
					if fail && key == "101/2/transfer_from" {
						return fmt.Errorf("interrupted outgoing transfer")
					}
					return nil
				}
			}
			build := omniAccountBuild(t, chains, h)
			fixture := newManagedFiniteFixture(t, backend, build)
			omnichainGoldenStorage(&fixture)
			opts := RuntimeOptions{Historical: runtimeFixtureInput(fixture), BuildID: "static-accounts", Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
				var clients []rpc.Requester
				for _, chain := range build.Executable().Chains {
					c := rpc.New(chains[runtimeChainID(chain)].url)
					c.Retries = 0
					c.RequestsPerSecond = 0
					clients = append(clients, c)
				}
				return clients, nil
			}}
			opts.Historical.QuerySize = 1
			var expected []string
			for _, pos := range [][2]uint64{{101, 1}, {202, 1}, {202, 2}, {101, 2}} {
				names := []string{"tx_to", "transfer_to", "block"}
				if pos[1] == 2 {
					names = []string{"tx_from", "call", "transfer_from", "block"}
				}
				for _, name := range names {
					expected = append(expected, fmt.Sprintf("%d/%d/%s", pos[0], pos[1], name))
				}
			}
			committed := 0
			for _, owner := range []string{"interrupted", "retry", "complete"} {
				opts.OpenID = owner
				r := runtimeOpen(t, backend, opts)
				calls = nil
				out, err := r.Run(t.Context())
				if owner == "interrupted" {
					if err == nil || !strings.Contains(err.Error(), "interrupted outgoing transfer") || out.Ready || out.Completed || !slices.Equal(calls, expected[:len(expected)-1]) {
						t.Fatalf("expected rollback, got %+v calls=%v err=%v", out, calls, err)
					}
				} else if err != nil || !out.Recovered || !out.Ready || !out.Completed {
					t.Fatalf("%s: %+v calls=%v err=%v", owner, out, calls, err)
				}
				if owner != "interrupted" {
					want := expected[committed:]
					if !slices.Equal(calls, want) {
						t.Fatalf("%s replay = %v, want %v", owner, calls, want)
					}
					committed = len(expected)
					ctrl, err := r.Control(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					for id, chain := range chains {
						if ctrl.Cursors[id].Hash != chain.hash[2] {
							t.Fatalf("chain%d cursor=%+v want %s", id, ctrl.Cursors[id], chain.hash[2])
						}
					}
				}
				if err := r.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				if owner == "interrupted" {
					managedInspect(t, fixture, func(db store.Backend) {
						rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(fixture.options.Owned.Storage.BusinessNamespace)+".account")
						if len(rows) == 1 {
							prefix := strings.Split(fmt.Sprint(rows[0]["value"]), "|")
							committed = len(prefix)
							if committed > len(expected)-4 || !slices.Equal(prefix, expected[:committed]) || !strings.HasSuffix(prefix[committed-1], "/block") {
								t.Fatalf("invalid committed block prefix: %v", prefix)
							}
						} else if len(rows) > 1 {
							t.Fatalf("partial block survived rollback: %v", rows)
						}
					})
					fail = false
				} else {
					assertOmniLogsSnapshot(t, fixture, store.ManagedLiveControl{}, omniLogsSnapshot{Rows: []map[string]any{{"id": "shared", "value": strings.Join(expected, "|")}}})
				}
			}
		})
	}
}

func omniAccountBuild(t *testing.T, chains map[uint64]omniAccountChain, h func(string) indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"omnichain","chains":{"one":{"id":101,"rpc":%q,"reorgWindow":0},"two":{"id":202,"rpc":%q,"reorgWindow":0}},"blocks":{"Head":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":2,"interval":1}},"accounts":{"Wallet":{"chain":{"one":{},"two":{}},"address":%q,"startBlock":1,"endBlock":2,"includeTransactionReceipts":true}},"contracts":{"Worker":{"chain":{"one":{},"two":{}},"address":%q,"abi":[{"type":"function","name":"record","inputs":[{"name":"amount","type":"uint256"}],"outputs":[{"type":"uint256","name":"value"}],"stateMutability":"payable"}],"startBlock":1,"endBlock":2,"includeTransactionReceipts":true,"includeCallTraces":true}}}`, chains[101].url, chains[202].url, chains[101].wallet, chains[101].worker)
	cfg, err := config.ParseSourceConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range base.RPCClients() {
		r.RequestsPerSecond = 0
		r.Retries = 0
	}
	build, err := config.BuildSourceIndexing(t.Context(), cfg, base, []config.SourceIndexingFunction{
		{Name: "Head:block", Fn: h("block")}, {Name: "Wallet:transaction:to", Fn: h("tx_to")}, {Name: "Wallet:transaction:from", Fn: h("tx_from")},
		{Name: "Wallet:transfer:to", Fn: h("transfer_to")}, {Name: "Wallet:transfer:from", Fn: h("transfer_from")}, {Name: "Worker.record()", Fn: h("call")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return build
}

func TestOmnichainAccountPartition(t *testing.T) {
	h := checkpoint.Block(100, 101, 2, true)
	head := store.ManagedLiveHead{Checkpoint: checkpoint.MustEncode(h)}
	event := func(kind string, typ int, tx, idx uint64) indexing.Object {
		cp := h
		cp.EventType, cp.TransactionIndex, cp.EventIndex = typ, tx, idx
		return indexing.Object{"type": kind, "checkpoint": checkpoint.MustEncode(cp)}
	}
	closeBlock := event("block", checkpoint.Blocks, checkpoint.Max.TransactionIndex, 0)
	for _, tc := range []struct {
		name   string
		events []indexing.Object
		ok     bool
	}{
		{"overlapping_call_transfer", []indexing.Object{event("transaction", checkpoint.Transactions, 0, 0), event("trace", checkpoint.Traces, 0, 0), event("transfer", checkpoint.Traces, 0, 0), closeBlock}, true},
		{"transaction_as_trace", []indexing.Object{event("transaction", checkpoint.Traces, 0, 0), closeBlock}, false},
		{"transaction_event_index", []indexing.Object{event("transaction", checkpoint.Transactions, 0, 1), closeBlock}, false},
		{"transaction_sentinel", []indexing.Object{event("transaction", checkpoint.Transactions, checkpoint.Max.TransactionIndex, 0), closeBlock}, false},
		{"transfer_as_log", []indexing.Object{event("transfer", checkpoint.Logs, 0, 0), closeBlock}, false},
		{"call_as_log", []indexing.Object{event("trace", checkpoint.Logs, 0, 0), closeBlock}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateOmnichainBlockPartition(tc.events, head); (err == nil) != tc.ok {
				t.Fatalf("valid=%v err=%v", tc.ok, err)
			}
		})
	}
}
