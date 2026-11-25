package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/factory"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestOrderedIndexer(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			const genesis = 1700000000
			first := testanvil.StartChain(t, 101, "--no-mining", "--timestamp", "1700000000")
			second := testanvil.StartChain(t, 202, "--no-mining", "--timestamp", "1700000000")
			call := func(endpoint, method string, args ...any) any {
				t.Helper()
				if args == nil {
					args = []any{}
				}
				var out any
				if err := rpc.New(endpoint).Call(ctx, method, args, &out); err != nil {
					t.Fatal(err)
				}
				return out
			}
			mine := func(endpoint string, offset uint64) {
				t.Helper()
				call(endpoint, "evm_setNextBlockTimestamp", genesis+offset)
				call(endpoint, "evm_mine")
			}
			fork := call(first, "evm_snapshot")
			mine(first, 10)
			mine(first, 30)
			mine(second, 20)
			mine(second, 40)
			cfg := config.Config{Ordering: schema.Omnichain,
				Chains: []config.Chain{{Name: "B", ID: 202, RPC: []string{second}}, {Name: "A", ID: 101, RPC: []string{first}}},
				Blocks: []config.BlockSource{{Name: "Head", Chain: "A", Interval: 1}, {Name: "Head", Chain: "B", Interval: 1}},
			}
			model := schema.Schema{Tables: []schema.Table{{Name: "ledger", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "history", Type: "text", NotNull: true}}}}}
			ns := fmt.Sprintf("ponder_go_ordered_%x", time.Now().UnixNano())
			path := filepath.Join(t.TempDir(), "db")
			open := func() *store.Store {
				t.Helper()
				backend := stagedBackend(t, ctx, dialect, path)
				t.Cleanup(func() { _ = backend.Close() })
				db, err := store.New(ctx, backend, model, ns, schema.Omnichain)
				if err != nil {
					t.Fatal(err)
				}
				return db
			}
			db := open()
			var calls int
			handlers := map[string]Handler{"Head:block": func(ctx context.Context, c *Context) error {
				row, err := c.DB.Find(ctx, "ledger", store.Row{"id": "shared"})
				if err != nil {
					return err
				}
				var history string
				if row != nil {
					history = fmt.Sprint(row["history"]) + ","
				}
				ts, err := Number(c.Event.Block.Timestamp)
				if err != nil {
					return err
				}
				calls++
				return c.DB.Write(ctx, "ledger", store.Row{"id": "shared", "history": fmt.Sprintf("%s%s%d", history, c.Chain.Name, ts-genesis)}, store.Upsert)
			}}
			worker := func() ApplicationIndexer {
				t.Helper()
				w, err := NewIndexerWithOptions(cfg, db, handlers, Options{DisableCache: dialect == "pglite"})
				if err != nil {
					t.Fatal(err)
				}
				return w
			}
			poll := func(w ApplicationIndexer, want string) {
				t.Helper()
				run, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				ready := false
				err := w.RunIndexing(run, func(v bool) {
					if v {
						ready = true
						cancel()
					}
				})
				if !errors.Is(err, context.Canceled) || !ready {
					t.Fatalf("ready %t error %v", ready, err)
				}
				rows, err := db.Read(ctx, "ledger")
				if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["history"] != want {
					t.Fatalf("ledger %+v, want %s: %v", rows, want, err)
				}
			}
			w := worker()
			poll(w, "A0,B0,A10,B20,A30")
			if call(first, "evm_revert", fork) != true {
				t.Fatal("could not restore first chain snapshot")
			}
			mine(first, 25)
			mine(first, 50)
			poll(w, "A0,B0,B20,A25,B40")
			before := calls
			if err := db.Backend.Close(); err != nil {
				t.Fatal(err)
			}
			db = open()
			w = worker()
			poll(w, "A0,B0,B20,A25,B40")
			if calls != before {
				t.Fatalf("cold reopen reran %d callbacks", calls-before)
			}
			mine(second, 60)
			mine(first, 70)
			poll(w, "A0,B0,B20,A25,B40,A50,B60")
		})
	}
}

func TestOrderedMixedSources(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			first := testanvil.StartChain(t, 101, "--no-mining", "--timestamp", "1700000000")
			second := testanvil.StartChain(t, 202, "--no-mining", "--timestamp", "1700000000")
			call := func(endpoint, method string, args ...any) any {
				t.Helper()
				if args == nil {
					args = []any{}
				}
				var out any
				if err := rpc.New(endpoint).Call(ctx, method, args, &out); err != nil {
					t.Fatal(err)
				}
				return out
			}
			mine := func(endpoint string, offset int) {
				t.Helper()
				call(endpoint, "evm_setNextBlockTimestamp", 1700000000+offset)
				call(endpoint, "evm_mine")
			}
			ping := abi.Event{Type: "event", Name: "Ping", Inputs: []abi.Parameter{{Name: "value", Type: "uint256"}}}
			created := abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{{Name: "child", Type: "address"}}}
			run := abi.Function{Type: "function", Name: "run", StateMutability: "payable"}
			parent := "0x1000000000000000000000000000000000000001"
			child := "0x2000000000000000000000000000000000000001"
			sender := call(first, "eth_accounts").([]any)[0].(string)
			call(first, "anvil_setCode", parent, "0x7f"+strings.Repeat("0", 24)+child[2:]+"6000527f"+abi.EventSelector(created)[2:]+"60206000a100")
			logCode := func(value int) string {
				return fmt.Sprintf("0x60%02x6000527f%s60206000a100", value, abi.EventSelector(ping)[2:])
			}
			call(first, "anvil_setCode", child, logCode(7))
			call(second, "anvil_setCode", child, logCode(9))
			send := func(endpoint, to, data string, value int) {
				t.Helper()
				call(endpoint, "eth_sendTransaction", map[string]any{"from": sender, "to": to, "data": data, "gas": "0x493e0", "value": Hex(uint64(value))})
			}
			send(first, parent, "0x", 0)
			mine(first, 10)
			send(second, child, "0x", 0)
			mine(second, 20)
			mine(second, 35)

			zero := 0
			firstEnd, secondEnd := uint64(2), uint64(1)
			parameter := "child"
			discovery := &factory.Config{Address: parent, Event: created, Parameter: &parameter}
			encode := func(entries ...any) json.RawMessage {
				t.Helper()
				raw, err := json.Marshal(entries)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			cfg := config.Config{Ordering: schema.Omnichain,
				Chains: []config.Chain{{Name: "B", ID: 202, RPC: []string{second}, ReorgWindow: &zero}, {Name: "A", ID: 101, RPC: []string{first}, ReorgWindow: &zero}},
				Contracts: []config.Contract{
					{Name: "Child", Chain: "A", ABI: encode(ping, run), Factory: discovery, EndBlock: &firstEnd, IncludeTransactionReceipts: true, IncludeCallTraces: true},
					{Name: "Fixed", Chain: "B", ABI: encode(ping), Address: []string{child}, StartBlock: 1, EndBlock: &secondEnd, IncludeTransactionReceipts: true}},
				Accounts: []config.AccountSource{{Name: "Wallet", Chain: "A", Factory: discovery, EndBlock: &firstEnd}},
				Blocks:   []config.BlockSource{{Name: "Head", Chain: "A", StartBlock: 1, EndBlock: &firstEnd, Interval: 1}},
			}
			model := schema.Schema{Tables: []schema.Table{{Name: "calls", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
			ns := fmt.Sprintf("ponder_go_ordmix_%x", time.Now().UnixNano())
			path := filepath.Join(t.TempDir(), "data")
			backend := stagedBackend(t, ctx, dialect, path)
			t.Cleanup(func() { _ = backend.Close() })
			db, err := store.New(ctx, backend, model, ns, schema.Omnichain)
			if err != nil {
				t.Fatal(err)
			}
			var calls []string
			injected := errors.New("stop inside the first contract call")
			failCall := true
			handlers := map[string]Handler{}
			for _, name := range []string{"Child:setup", "Head:block", "Child:Ping", "Child.run()", "Wallet:transaction:to", "Wallet:transfer:to", "Fixed:Ping"} {
				handlers[name] = func(ctx context.Context, c *Context) error {
					key := fmt.Sprintf("%s/%s/%s", c.Chain.Name, c.Event.Block.Number, c.Event.Name)
					switch c.Event.Name {
					case "Child:Ping", "Fixed:Ping":
						want := "7"
						if c.Chain.ID == 202 {
							want = "9"
						}
						if fmt.Sprint(c.Event.Args.(map[string]any)["value"]) != want || c.Event.Receipt["status"] != "0x1" {
							return fmt.Errorf("log %s: args %v receipt %v", key, c.Event.Args, c.Event.Receipt)
						}
					case "Child.run()":
						if failCall {
							failCall = false
							// The transaction must roll this back before retrying the same receipt.
							if err := c.DB.Write(ctx, "calls", store.Row{"id": "interrupted"}, store.Insert); err != nil {
								return err
							}
							return injected
						}
						if c.Event.Trace == nil || c.Event.Receipt["status"] != "0x1" {
							return fmt.Errorf("missing call trace or receipt: %s", key)
						}
					case "Wallet:transfer:to":
						if c.Event.Transfer == nil {
							return fmt.Errorf("missing transfer: %s", key)
						}
					case "Wallet:transaction:to":
						if c.Event.Transaction["to"] != child {
							return fmt.Errorf("unexpected child transaction %v", c.Event.Transaction)
						}
					}
					calls = append(calls, key)
					return c.DB.Write(ctx, "calls", store.Row{"id": key}, store.Insert)
				}
			}
			worker, err := NewIndexerWithOptions(cfg, db, handlers, Options{DisableCache: dialect == "pglite"})
			if err != nil {
				t.Fatal(err)
			}
			round, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			err = worker.RunIndexing(round, func(ready bool) {
				if ready {
					cancel()
				}
			})
			firstCalls := []string{"A/0x0/Child:setup", "A/0x1/Head:block"}
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(calls, firstCalls) {
				t.Fatalf("first scan: %v, %v", calls, err)
			}
			send(first, child, abi.FunctionSelector(run), 7)
			mine(first, 30)
			finish, stop := context.WithTimeout(ctx, 20*time.Second)
			defer stop()
			if err := worker.RunIndexing(finish, func(bool) {}); !errors.Is(err, injected) {
				t.Fatalf("expected the injected callback failure, got %v", err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			backend = stagedBackend(t, ctx, dialect, path)
			db, err = store.New(ctx, backend, model, ns, schema.Omnichain)
			if err != nil {
				t.Fatal(err)
			}
			worker, err = NewIndexerWithOptions(cfg, db, handlers, Options{DisableCache: dialect == "pglite"})
			if err != nil {
				t.Fatal(err)
			}
			if err := worker.RunIndexing(finish, func(bool) {}); err != nil {
				t.Fatal(err)
			}
			want := append(firstCalls, "B/0x1/Fixed:Ping", "A/0x2/Wallet:transaction:to", "A/0x2/Child:Ping", "A/0x2/Child.run()", "A/0x2/Wallet:transfer:to", "A/0x2/Head:block")
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("mixed source order %v, want %v", calls, want)
			}
			rows, err := db.Read(ctx, "calls")
			if err != nil || len(rows.Rows) != len(want) {
				t.Fatal(rows, err)
			}
			journal, err := backend.Query(ctx, "SELECT count(*)::text AS count FROM "+schema.Quote(ns)+"._ponder_go_journal")
			if err != nil || fmt.Sprint(journal.Rows[0]["count"]) != "0" {
				t.Fatal(journal, err)
			}
			if err := worker.RunIndexing(finish, func(bool) {}); err != nil || !reflect.DeepEqual(calls, want) {
				t.Fatal("bounded replay", calls, err)
			}
		})
	}
}
