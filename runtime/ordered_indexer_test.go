package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
			// A stale proof from one chain must roll back the entire finalization transaction.
			state := func() string {
				t.Helper()
				rows, err := db.Backend.Query(ctx, "SELECT chain_id::text,checkpoint,safe_checkpoint FROM "+schema.Quote(ns)+"._ponder_go_finalized ORDER BY chain_id")
				if err != nil {
					t.Fatal(err)
				}
				undo, err := db.Backend.Query(ctx, "SELECT count(*)::text AS n FROM "+schema.Quote(ns)+"._ponder_go_journal")
				if err != nil {
					t.Fatal(err)
				}
				return fmt.Sprint(rows.Rows, undo.Rows)
			}
			blockA, err := db.SyncedBlock(ctx, 101, 1)
			if err != nil || blockA == nil {
				t.Fatal("missing first anchor", err)
			}
			blockB, err := db.SyncedBlock(ctx, 202, 1)
			if err != nil || blockB == nil {
				t.Fatal("missing second anchor", err)
			}
			blockB.Hash = "orphan"
			prior := state()
			if err := db.FinalizeOmnichainThrough(ctx, checkpoint.Max, []store.SyncBlock{*blockA, *blockB}); !errors.Is(err, store.ErrIndexedBlockChanged) || state() != prior {
				t.Fatal("partial finalization", err)
			}
			deepFork := call(first, "evm_snapshot")
			mine(second, 60)
			mine(first, 70)
			poll(w, "A0,B0,B20,A25,B40,A50,B60")
			mine(first, 330)
			mine(second, 340)
			stable := "A0,B0,B20,A25,B40,A50,B60,A70,A330"
			poll(w, stable)
			if call(first, "evm_revert", deepFork) != true {
				t.Fatal("deep fork setup")
			}
			mine(first, 55)
			run, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := w.RunIndexing(run, func(bool) {}); err == nil || !strings.Contains(err.Error(), "before safe checkpoint") {
				t.Fatalf("deep reorg was not fenced: %v", err)
			}
			rows, err := db.Read(ctx, "ledger")
			if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["history"] != stable {
				t.Fatalf("deep reorg altered published state: %v %v", rows, err)
			}
		})
	}
}

func TestOrderedMixedSources(t *testing.T) { testMixedSources(t, schema.Omnichain) }

func TestDefaultMixedSources(t *testing.T) { testMixedSources(t, schema.Multichain) }

func TestIsolatedMixedSources(t *testing.T) { testMixedSources(t, schema.Isolated) }

func testMixedSources(t *testing.T, ordering schema.Ordering) {
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
			cfg := config.Config{Ordering: ordering,
				Chains: []config.Chain{{Name: "B", ID: 202, RPC: []string{second}, ReorgWindow: &zero}, {Name: "A", ID: 101, RPC: []string{first}, ReorgWindow: &zero}},
				Contracts: []config.Contract{
					{Name: "Child", Chain: "A", ABI: encode(ping, run), Factory: discovery, EndBlock: &firstEnd, IncludeTransactionReceipts: true, IncludeCallTraces: true},
					{Name: "Fixed", Chain: "B", ABI: encode(ping), Address: []string{child}, StartBlock: 1, EndBlock: &secondEnd, IncludeTransactionReceipts: true}},
				Accounts: []config.AccountSource{{Name: "Wallet", Chain: "A", Factory: discovery, EndBlock: &firstEnd}},
				Blocks:   []config.BlockSource{{Name: "Head", Chain: "A", StartBlock: 1, EndBlock: &firstEnd, Interval: 1}},
			}
			callsTable := schema.Table{Name: "calls", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}
			if ordering == schema.Isolated {
				callsTable.Columns[0].Primary = false
				callsTable.Columns = append(callsTable.Columns, schema.Column{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true})
				callsTable.PrimaryKey = []string{"chain_id", "id"}
			}
			model := schema.Schema{Tables: []schema.Table{callsTable}}
			ns := fmt.Sprintf("ponder_go_ordmix_%x", time.Now().UnixNano())
			path := filepath.Join(t.TempDir(), "data")
			backend := stagedBackend(t, ctx, dialect, path)
			t.Cleanup(func() { _ = backend.Close() })
			db, err := store.New(ctx, backend, model, ns, ordering)
			if err != nil {
				t.Fatal(err)
			}
			var calls []string
			var callsMu sync.Mutex
			checkCalls := func(want []string) {
				t.Helper()
				got := slices.Clone(calls)
				if ordering == schema.Isolated {
					// Workers on different chains may interleave. Preserve each chain's exact callback order.
					byChain := func(a, b string) int {
						first, _, _ := strings.Cut(a, "/")
						second, _, _ := strings.Cut(b, "/")
						return strings.Compare(first, second)
					}
					want = slices.Clone(want)
					slices.SortStableFunc(got, byChain)
					slices.SortStableFunc(want, byChain)
				}
				if !slices.Equal(got, want) {
					t.Fatalf("mixed source order %v, want %v", got, want)
				}
			}
			write := func(ctx context.Context, c *Context, id string) error {
				row := store.Row{"id": id}
				if ordering == schema.Isolated {
					row["chain_id"] = c.Chain.ID
				}
				return c.DB.Write(ctx, "calls", row, store.Insert)
			}
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
							if err := write(ctx, c, "interrupted"); err != nil {
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
					callsMu.Lock()
					calls = append(calls, key)
					callsMu.Unlock()
					return write(ctx, c, key)
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
			if ordering != schema.Omnichain {
				firstCalls = append(firstCalls, "B/0x1/Fixed:Ping")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("first scan: %v", err)
			}
			checkCalls(firstCalls)
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
			db, err = store.New(ctx, backend, model, ns, ordering)
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
			want := firstCalls
			if ordering == schema.Omnichain {
				want = append(want, "B/0x1/Fixed:Ping")
			}
			want = append(want, "A/0x2/Wallet:transaction:to", "A/0x2/Child:Ping", "A/0x2/Child.run()", "A/0x2/Wallet:transfer:to", "A/0x2/Head:block")
			checkCalls(want)
			rows, err := db.Read(ctx, "calls")
			if err != nil || len(rows.Rows) != len(want) {
				t.Fatal(rows, err)
			}
			persisted := make([]string, 0, len(rows.Rows))
			chainIDs := map[string]string{"A": "101", "B": "202"}
			for _, row := range rows.Rows {
				id := fmt.Sprint(row["id"])
				if ordering == schema.Isolated {
					chain, _, _ := strings.Cut(id, "/")
					if fmt.Sprint(row["chain_id"]) != chainIDs[chain] {
						t.Fatalf("callback stored under the wrong chain: %v", row)
					}
				}
				persisted = append(persisted, id)
			}
			expected := slices.Clone(want)
			slices.Sort(expected)
			slices.Sort(persisted)
			if !slices.Equal(persisted, expected) {
				t.Fatalf("committed callbacks %v, want %v", persisted, expected)
			}
			journal, err := backend.Query(ctx, "SELECT count(*)::text AS count FROM "+schema.Quote(ns)+"._ponder_go_journal")
			if err != nil || fmt.Sprint(journal.Rows[0]["count"]) != "0" {
				t.Fatal(journal, err)
			}
			if err := worker.RunIndexing(finish, func(bool) {}); err != nil {
				t.Fatal("bounded replay", err)
			}
			checkCalls(want)
		})
	}
}
