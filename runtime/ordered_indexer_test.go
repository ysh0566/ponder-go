package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
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
