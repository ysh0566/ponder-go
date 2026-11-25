package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestNativeCallbackOverlap(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			url := testanvil.Start(t, "--no-mining", "--timestamp", "1700000000")
			client := rpc.New(url)
			call := func(method string, args ...any) any {
				t.Helper()
				if args == nil {
					args = []any{}
				}
				var result any
				if err := client.Call(ctx, method, args, &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			left := abi.Event{Type: "event", Name: "Marked", Inputs: []abi.Parameter{{Name: "left", Type: "uint256", Indexed: true}, {Name: "right", Type: "uint256"}}}
			right := abi.Event{Type: "event", Name: "Marked", Inputs: []abi.Parameter{{Name: "left", Type: "uint256"}, {Name: "right", Type: "uint256", Indexed: true}}}
			unindexed := abi.Event{Type: "event", Name: "Marked", Inputs: []abi.Parameter{{Name: "left", Type: "uint256"}, {Name: "right", Type: "uint256"}}}
			burn := abi.Function{Type: "function", Name: "burn", Inputs: []abi.Parameter{{Type: "uint256"}}}
			colliding := abi.Function{Type: "function", Name: "collate_propagate_storage", Inputs: []abi.Parameter{{Type: "bytes16"}}}
			if abi.EventSelector(left) != abi.EventSelector(right) || abi.FunctionSelector(burn) != "0x42966c68" || abi.FunctionSelector(colliding) != "0x42966c68" {
				t.Fatal("fixture selector mismatch")
			}
			address := "0x1000000000000000000000000000000000000001"
			rawABI := func(items ...any) json.RawMessage {
				t.Helper()
				data, err := json.Marshal(items)
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{url}}}, Contracts: []config.Contract{
				{Name: "C", Chain: "local", StartBlock: 1, Address: []string{address}, ABI: rawABI(left, unindexed, right, burn, colliding), IncludeCallTraces: true, IncludeTransactionReceipts: true},
				{Name: "Mirror", Chain: "local", StartBlock: 1, Address: []string{address}, ABI: rawABI(left)},
			}}
			const (
				first  = "C:Marked(uint256 indexed left, uint256 right)"
				second = "C:Marked(uint256 left, uint256 indexed right)"
				mirror = "Mirror:Marked"
				f1     = "C.burn()"
				f2     = "C.collate_propagate_storage()"
				prefix = "0102030405060708090a0b0c0d0e0f10"
			)
			word := prefix + strings.Repeat("0", 32)
			account := call("eth_accounts").([]any)[0]
			mine := func(value byte, ts int) string {
				t.Helper()
				// One transaction emits Marked(indexed 7, data value) and one CALL trace.
				call("anvil_setCode", address, fmt.Sprintf("0x60%02x60005260077f%s60206000a200", value, abi.EventSelector(left)[2:]))
				call("eth_sendTransaction", map[string]any{"from": account, "to": address, "data": "0x42966c68" + word, "gas": "0x20000"})
				call("evm_setNextBlockTimestamp", 1700000000+ts)
				call("evm_mine")
				return call("eth_getBlockByNumber", "latest", false).(map[string]any)["hash"].(string)
			}
			snapshot := call("evm_snapshot")
			hash := mine(11, 10)
			model := schema.Schema{Tables: []schema.Table{{Name: "seen", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "text"}, {Name: "block_hash", Type: "text"}, {Name: "event_id", Type: "text"}}}}}
			ns := fmt.Sprintf("ponder_go_overlap_%x", time.Now().UnixNano())
			path := filepath.Join(t.TempDir(), "db")
			var db *store.Store
			var calls []string
			fault := errors.New("second matching callback failed")
			failOnce := true
			handlers := map[string]Handler{
				"C:Marked(uint256 left, uint256 right)": func(context.Context, *Context) error {
					return errors.New("log does not contain two unindexed words")
				},
			}
			for _, name := range []string{first, second, mirror, f1, f2} {
				handlers[name] = func(ctx context.Context, c *Context) error {
					calls = append(calls, c.Event.Name)
					if (c.Event.Receipt != nil) != (c.Event.Name != mirror) {
						return fmt.Errorf("receipt for %s", name)
					}
					var value string
					if c.Event.Log != nil {
						args := c.Event.Args.(map[string]any)
						value = fmt.Sprintf("%v/%v", args["left"], args["right"])
					} else {
						if c.Event.Trace == nil {
							return fmt.Errorf("missing trace for %s", name)
						}
						value = fmt.Sprint(c.Event.Args.([]any)[0])
					}
					if err := c.DB.Write(ctx, "seen", store.Row{"id": name, "value": value, "block_hash": c.Event.Block.Hash, "event_id": c.Event.ID}, store.Insert); err != nil {
						return err
					}
					if failOnce && name == second {
						failOnce = false
						return fault
					}
					return nil
				}
			}
			open := func() ApplicationIndexer {
				t.Helper()
				b := stagedBackend(t, ctx, dialect, path)
				t.Cleanup(func() { _ = b.Close() })
				var err error
				db, err = store.New(ctx, b, model, ns, schema.Multichain)
				if err != nil {
					t.Fatal(err)
				}
				w, err := NewIndexerWithOptions(cfg, db, handlers, Options{DisableCache: dialect == "pglite"})
				if err != nil {
					t.Fatal(err)
				}
				return w
			}
			poll := func(w ApplicationIndexer) error {
				t.Helper()
				round, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				return w.RunIndexing(round, func(ready bool) {
					if ready {
						cancel()
					}
				})
			}
			w := open()
			if err := poll(w); !errors.Is(err, fault) {
				t.Fatalf("second matching callback: %v, calls=%v", err, calls)
			}
			rows, err := db.Read(ctx, "seen")
			if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["id"] != first || rows.Rows[0]["value"] != "7/11" {
				t.Fatal("partial commit", rows, err)
			}
			if err := db.Backend.Close(); err != nil {
				t.Fatal(err)
			}
			w = open()
			wantCalls := []string{first, second, second, mirror, f1, f2}
			check := func(hash string, v int) {
				t.Helper()
				if err := poll(w); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(calls, wantCalls) {
					t.Fatalf("callbacks %v, want %v", calls, wantCalls)
				}
				num, _ := new(big.Int).SetString(word, 16)
				want := map[string]string{first: fmt.Sprintf("7/%d", v), second: fmt.Sprintf("%d/7", v), mirror: fmt.Sprintf("7/%d", v), f1: num.String(), f2: "0x" + prefix}
				rows, err := db.Read(ctx, "seen")
				if err != nil || len(rows.Rows) != len(want) {
					t.Fatal(rows, err)
				}
				ids := map[string]string{}
				for _, r := range rows.Rows {
					name := fmt.Sprint(r["id"])
					if r["value"] != want[name] || r["block_hash"] != hash {
						t.Fatalf("%s=%v, want %s on %s", name, r, want[name], hash)
					}
					ids[name] = fmt.Sprint(r["event_id"])
				}
				if ids[first] != ids[second] || ids[first] != ids[mirror] || ids[f1] != ids[f2] || ids[first] == ids[f1] {
					t.Fatal("event checkpoints", ids)
				}
			}
			check(hash, 11)
			check(hash, 11)
			if call("evm_revert", snapshot) != true {
				t.Fatal("restore chain")
			}
			hash = mine(13, 20)
			wantCalls = append(wantCalls, first, second, mirror, f1, f2)
			check(hash, 13)
			testOverlapPage(t, client, cfg, db, model, []config.SourceIndexingFunction{{Name: first, Fn: handlers[first]}, {Name: second, Fn: handlers[second]}})
			wantCalls = append(wantCalls, first, second)
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("paged replay calls %v, want %v", calls, wantCalls)
			}
		})
	}
}

// A page limit of one can contain two deliveries of the same log. The live
// worker and page dispatcher must recognize the same durable callback receipts.
func testOverlapPage(t *testing.T, client *rpc.Client, cfg config.Config, db *store.Store, model schema.Schema, functions []config.SourceIndexingFunction) {
	t.Helper()
	ctx := t.Context()
	var block map[string]any
	if err := client.Call(ctx, "eth_getBlockByNumber", []any{"0x1", true}, &block); err != nil {
		t.Fatal(err)
	}
	transaction := block["transactions"].([]any)[0].(map[string]any)
	var receipt map[string]any
	if err := client.Call(ctx, "eth_getTransactionReceipt", []any{transaction["hash"]}, &receipt); err != nil {
		t.Fatal(err)
	}
	cache, err := db.EnableProtocolCache(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := cache.SourceSyncStore(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.InsertBlocks(ctx, 1, []filter.Object{block}); err != nil {
		t.Fatal(err)
	}
	if err := source.InsertTransactions(ctx, 1, []filter.Object{transaction}); err != nil {
		t.Fatal(err)
	}
	if err := source.InsertTransactionReceipts(ctx, 1, []filter.Object{receipt}); err != nil {
		t.Fatal(err)
	}
	log := receipt["logs"].([]any)[0].(map[string]any)
	if err := source.InsertLogs(ctx, 1, []filter.Object{log}); err != nil {
		t.Fatal(err)
	}
	base, err := config.ParseSourceConfiguration([]byte(fmt.Sprintf(`{"chains":{"local":{"id":1,"rpc":%q}}}`, cfg.Chains[0].RPC[0])))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Contracts[0]
	typed := config.CreateTypedSourceConfiguration(base, "C", config.SourceTypedContract[int, int]{Chain: config.SourceOnChain("local"), ABI: c.ABI, Address: config.SourceSome[any](c.Address[0]), StartBlock: 1, EndBlock: 1, IncludeTransactionReceipts: config.SourceSome(true)})
	built, err := config.BuildTypedSourceConfig(typed, config.SourceBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	indexing, err := config.SafeBuildTypedSourceIndexing(ctx, typed, built, functions)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewFromSource(indexing, "local", db, Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	page, err := engine.ReadSourcePage(ctx, source, 1, 1, 1)
	if err != nil || len(page.Events) != 2 || page.Cursor != 1 {
		t.Fatalf("overlap page has %d events, cursor %v: %v", len(page.Events), page.Cursor, err)
	}
	if err := engine.DispatchSourceEvents(ctx, page.Events); err != nil {
		t.Fatal("page after live indexing", err)
	}
	fresh, err := store.New(ctx, db.Backend, model, db.Namespace+"p", schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	engine, err = NewFromSource(indexing, "local", fresh, Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := engine.DispatchSourceEvents(ctx, page.Events); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := fresh.Read(ctx, "seen")
	if err != nil || len(rows.Rows) != 2 {
		t.Fatal(rows, err)
	}
}
