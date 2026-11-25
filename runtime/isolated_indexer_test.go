package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestIsolatedIndexer(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			first := testanvil.StartChain(t, 101, "--no-mining")
			second := testanvil.StartChain(t, 202, "--no-mining")
			target, err := url.Parse(second)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			var offline atomic.Bool
			offline.Store(true)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if offline.Load() {
					http.Error(w, "offline", 503)
					return
				}
				proxy.ServeHTTP(w, r)
			}))
			t.Cleanup(gateway.Close)
			call := func(endpoint, method string, args ...any) any {
				t.Helper()
				if args == nil {
					args = []any{}
				}
				var result any
				if err := rpc.New(endpoint).Call(ctx, method, args, &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			snapshot := call(first, "evm_snapshot")
			call(first, "evm_mine")
			call(second, "evm_mine")
			immediate := 0
			cfg := config.Config{Ordering: schema.Isolated,
				Chains: []config.Chain{{Name: "first", ID: 101, RPC: []string{first}, PollingIntervalMS: 20}, {Name: "second", ID: 202, RPC: []string{gateway.URL}, PollingIntervalMS: 20, ReorgWindow: &immediate}},
				Blocks: []config.BlockSource{{Name: "Head", Chain: "first", Interval: 1, StartBlock: 1}, {Name: "Head", Chain: "second", Interval: 1, StartBlock: 1}},
			}
			model := schema.Schema{Tables: []schema.Table{{Name: "height", PrimaryKey: []string{"chain_id", "number"}, Columns: []schema.Column{
				{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "number", Type: "integer", NotNull: true}, {Name: "hash", Type: "text", NotNull: true},
			}}}}
			ns := fmt.Sprintf("ponder_go_isolated_%x", time.Now().UnixNano())
			backend := stagedBackend(t, ctx, dialect, filepath.Join(t.TempDir(), "data"))
			t.Cleanup(func() { backend.Close() })
			db, err := store.New(ctx, backend, model, ns, schema.Isolated)
			if err != nil {
				t.Fatal(err)
			}
			var firstCalls, secondCalls atomic.Int64
			start := func() (*Application, *query.Server) {
				t.Helper()
				routes := query.New(db)
				worker, err := NewIndexer(cfg, db, map[string]Handler{"Head:block": func(ctx context.Context, c *Context) error {
					if c.Chain.ID == 101 {
						firstCalls.Add(1)
					} else {
						secondCalls.Add(1)
					}
					if _, err := c.DB.Find(ctx, "height", store.Row{"chain_id": 999, "number": 1}); err == nil {
						return fmt.Errorf("cross-chain lookup succeeded")
					}
					number, err := Number(c.Event.Block.Number)
					if err != nil {
						return err
					}
					return c.DB.Write(ctx, "height", store.Row{"chain_id": c.Chain.ID, "number": number, "hash": c.Event.Block.Hash}, store.Insert)
				}})
				if err != nil {
					t.Fatal(err)
				}
				app, err := LaunchApplication(ctx, worker, routes, "127.0.0.1", 0)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { app.Shutdown(context.Background()) })
				return app, routes
			}
			app, routes := start()
			rows := func() map[string]string {
				t.Helper()
				result, err := db.Read(ctx, "height")
				if err != nil {
					t.Fatal(err)
				}
				out := map[string]string{}
				for _, row := range result.Rows {
					out[fmt.Sprintf("%v/%v", row["chain_id"], row["number"])] = fmt.Sprint(row["hash"])
				}
				return out
			}
			await := func(ok func() bool) {
				t.Helper()
				deadline := time.Now().Add(12 * time.Second)
				for time.Now().Before(deadline) {
					select {
					case <-app.done:
						t.Fatalf("indexer stopped: %v", app.Wait())
					default:
					}
					if ok() {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatalf("timed out, rows=%v ready=%v", rows(), routes.Ready.Load())
			}
			await(func() bool { return len(rows()) == 1 })
			if routes.Ready.Load() || rows()["101/1"] == "" {
				t.Fatal("healthy chain did not index independently")
			}
			offline.Store(false)
			await(func() bool { return routes.Ready.Load() && len(rows()) == 2 })
			call(first, "evm_mine")
			call(second, "evm_mine")
			await(func() bool { return len(rows()) == 4 })
			// Chain B finalizes immediately while chain A still needs its undo records.
			await(func() bool {
				r, err := backend.Query(ctx, "SELECT checkpoint FROM "+schema.Quote(ns)+"._ponder_go_finalized WHERE chain_id=202")
				if err != nil || len(r.Rows) != 1 {
					return false
				}
				cp, err := checkpoint.Decode(fmt.Sprint(r.Rows[0]["checkpoint"]))
				return err == nil && cp.BlockNumber == 2
			})
			before := rows()
			if call(first, "evm_revert", snapshot) != true {
				t.Fatal("snapshot did not revert")
			}
			call(first, "evm_increaseTime", 300)
			call(first, "evm_mine")
			await(func() bool {
				r := rows()
				return len(r) == 3 && r["101/1"] != "" && r["101/1"] != before["101/1"] && r["101/2"] == "" && r["202/1"] == before["202/1"] && r["202/2"] == before["202/2"]
			})
			if err := app.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			db, err = store.New(ctx, backend, model, ns, schema.Isolated)
			if err != nil {
				t.Fatal(err)
			}
			app, routes = start()
			await(func() bool { return routes.Ready.Load() })
			if firstCalls.Load() != 3 || secondCalls.Load() != 2 || len(rows()) != 3 {
				t.Fatalf("unexpected replay: %d/%d, %v", firstCalls.Load(), secondCalls.Load(), rows())
			}
		})
	}
}

func TestIsolatedBoundedCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	backend := stagedBackend(t, ctx, "pglite", "memory://")
	defer backend.Close()
	db, err := store.New(ctx, backend, schema.Schema{}, "ponder_go_isolated_bounds", schema.Isolated)
	if err != nil {
		t.Fatal(err)
	}
	end, window := uint64(1), 0
	cfg := config.Config{Ordering: schema.Isolated}
	for _, id := range []uint64{301, 302} {
		name := fmt.Sprintf("chain%d", id)
		url := testanvil.StartChain(t, id, "--no-mining")
		for range 3 {
			var result any
			if err := rpc.New(url).Call(ctx, "evm_mine", []any{}, &result); err != nil {
				t.Fatal(err)
			}
		}
		cfg.Chains = append(cfg.Chains, config.Chain{Name: name, ID: id, RPC: []string{url}, ReorgWindow: &window})
		cfg.Blocks = append(cfg.Blocks, config.BlockSource{Name: "Head", Chain: name, StartBlock: 1, EndBlock: &end, Interval: 1})
	}
	var calls atomic.Int64
	worker, err := NewIndexer(cfg, db, map[string]Handler{"Head:block": func(context.Context, *Context) error { calls.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	var ready bool
	if err := worker.RunIndexing(ctx, func(v bool) { ready = v }); err != nil {
		t.Fatal(err)
	}
	progress, err := db.Progress(ctx)
	if err != nil || len(progress) != 2 || calls.Load() != 2 || !ready {
		t.Fatal(progress, err, calls.Load(), ready)
	}
	for _, cp := range progress {
		p, err := checkpoint.Decode(cp)
		if err != nil || p.BlockNumber != end {
			t.Fatal(p, err)
		}
	}
}
