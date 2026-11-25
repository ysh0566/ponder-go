package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestContinuousFinality(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			const chainID = 391
			endpoint := testanvil.StartChain(t, chainID, "--no-mining", "--timestamp", "1700000000")
			client := rpc.New(endpoint)
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
			mine := func(offset int) {
				t.Helper()
				call("evm_setNextBlockTimestamp", 1700000000+offset)
				call("evm_mine")
			}
			revert := func(snapshot any) {
				t.Helper()
				if call("evm_revert", snapshot) != true {
					t.Fatal("could not revert local chain")
				}
			}
			mine(10)
			mine(20)
			fork := call("evm_snapshot")
			mine(30)

			window := 12
			cfg := config.Config{Chains: []config.Chain{{Name: "A", ID: chainID, RPC: []string{endpoint}, ReorgWindow: &window, PollingIntervalMS: 10}},
				Blocks: []config.BlockSource{{Chain: "A", Name: "Head", StartBlock: 1, Interval: 1}}}
			model := schema.Schema{Tables: []schema.Table{{Name: "seen", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "hash", Type: "text", NotNull: true}}}}}
			ns := fmt.Sprintf("ponder_go_finality_%x", time.Now().UnixNano())
			dir := filepath.Join(t.TempDir(), "data")
			open := func() *store.Store {
				t.Helper()
				b := stagedBackend(t, ctx, dialect, dir)
				t.Cleanup(func() { _ = b.Close() })
				db, err := store.New(ctx, b, model, ns, schema.Multichain)
				if err != nil {
					t.Fatal(err)
				}
				return db
			}
			db := open()
			var duringScan any
			calls := 0
			worker := func() ApplicationIndexer {
				t.Helper()
				w, err := NewIndexer(cfg, db, map[string]Handler{"Head:block": func(ctx context.Context, c *Context) error {
					calls++
					if duringScan != nil && c.Event.Block.Number == "0x4" {
						revert(duringScan)
						duringScan = nil
						mine(48)
						mine(68)
					}
					return c.DB.Write(ctx, "seen", store.Row{"id": c.Event.Block.Number, "hash": c.Event.Block.Hash}, store.Insert)
				}})
				if err != nil {
					t.Fatal(err)
				}
				return w
			}
			poll := func(w ApplicationIndexer) {
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
				if !ready || !errors.Is(err, context.Canceled) {
					t.Fatalf("ready %t: %v", ready, err)
				}
			}
			check := func(latest, final uint64) {
				t.Helper()
				rows, err := db.Read(ctx, "seen")
				if err != nil || len(rows.Rows) != int(latest) {
					t.Fatalf("seen rows %v: %v", rows, err)
				}
				for _, row := range rows.Rows {
					block := call("eth_getBlockByNumber", row["id"], false).(map[string]any)
					if block["hash"] != row["hash"] {
						t.Fatalf("orphan row %+v", row)
					}
				}
				record, err := db.Backend.Query(ctx, "SELECT checkpoint FROM "+schema.Quote(ns)+"._ponder_go_finalized")
				if err != nil || len(record.Rows) != 1 {
					t.Fatalf("finalized %+v: %v", record, err)
				}
				cp, err := checkpoint.Decode(fmt.Sprint(record.Rows[0]["checkpoint"]))
				if err != nil || cp.ChainID != chainID || cp.BlockNumber != final {
					t.Fatalf("finality %+v, want %d: %v", cp, final, err)
				}
				undo, err := db.Backend.Query(ctx, "SELECT checkpoint FROM "+schema.Quote(ns)+"._ponder_go_journal ORDER BY checkpoint")
				if err != nil || len(undo.Rows) != int(latest-final) {
					t.Fatalf("undo %+v: %v", undo, err)
				}
				for i, row := range undo.Rows {
					cp, err := checkpoint.Decode(fmt.Sprint(row["checkpoint"]))
					if err != nil || cp.BlockNumber != final+uint64(i)+1 {
						t.Fatalf("undo entry %+v: %v", cp, err)
					}
				}
			}

			w := worker()
			poll(w)
			check(3, 1)
			revert(fork)
			poll(w)
			check(2, 1)
			mine(33)
			duringScan = call("evm_snapshot")
			poll(w)
			check(3, 2)
			mine(45)
			mine(65)
			poll(w)
			check(5, 4)
			if calls != 7 {
				t.Fatalf("expected 7 callback executions across both forks, got %d", calls)
			}
			if err := db.Backend.Close(); err != nil {
				t.Fatal(err)
			}
			db = open()
			poll(worker())
			check(5, 4)
			if calls != 7 {
				t.Fatalf("cold open repeated callbacks: %d", calls)
			}
		})
	}
}
