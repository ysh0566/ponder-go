package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestDefaultIndexer(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			const genesis = 1700000000
			a := testanvil.StartChain(t, 101, "--no-mining", "--timestamp", "1700000000")
			b := testanvil.StartChain(t, 202, "--no-mining", "--timestamp", "1700000000")
			gate := func(endpoint string, offline *atomic.Bool) string {
				t.Helper()
				target, err := url.Parse(endpoint)
				if err != nil {
					t.Fatal(err)
				}
				proxy := httputil.NewSingleHostReverseProxy(target)
				gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if offline.Load() {
						http.Error(w, "offline", http.StatusServiceUnavailable)
						return
					}
					proxy.ServeHTTP(w, r)
				}))
				t.Cleanup(gateway.Close)
				return gateway.URL
			}
			var offlineA, offlineB atomic.Bool
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
			mine := func(endpoint string, offset uint64) {
				t.Helper()
				call(endpoint, "evm_setNextBlockTimestamp", genesis+offset)
				call(endpoint, "evm_mine")
			}
			zero := 0
			cfg := config.Config{
				Chains: []config.Chain{{Name: "A", ID: 101, RPC: []string{gate(a, &offlineA)}}, {Name: "B", ID: 202, RPC: []string{gate(b, &offlineB)}, ReorgWindow: &zero}},
				Blocks: []config.BlockSource{{Name: "Head", Chain: "A", StartBlock: 1, Interval: 2}, {Name: "Head", Chain: "B", StartBlock: 1, Interval: 1}},
			}
			model := schema.Schema{Tables: []schema.Table{{Name: "ledger", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "text", NotNull: true}}}}}
			ns := fmt.Sprintf("ponder_go_default_%x", time.Now().UnixNano())
			path := filepath.Join(t.TempDir(), "data")
			var db *store.Store
			var calls []string
			open := func() ApplicationIndexer {
				t.Helper()
				backend := stagedBackend(t, ctx, dialect, path)
				t.Cleanup(func() { _ = backend.Close() })
				var err error
				db, err = store.New(ctx, backend, model, ns, schema.Multichain)
				if err != nil {
					t.Fatal(err)
				}
				w, err := NewIndexer(cfg, db, map[string]Handler{"Head:block": func(ctx context.Context, c *Context) error {
					ts, err := Number(c.Event.Block.Timestamp)
					if err != nil {
						return err
					}
					offset := ts - genesis
					calls = append(calls, fmt.Sprintf("%s%d", c.Chain.Name, offset))
					if c.Chain.Name == "A" {
						if offset == 10 {
							return nil
						}
						return c.DB.Write(ctx, "ledger", store.Row{"id": "branch", "value": "replacement"}, store.Upsert)
					}
					row, err := c.DB.Find(ctx, "ledger", store.Row{"id": "branch"})
					if err != nil {
						return err
					}
					value := "original"
					if row != nil {
						value = fmt.Sprint(row["value"])
					}
					return c.DB.Write(ctx, "ledger", store.Row{"id": "peer" + c.Event.Block.Number, "value": value}, store.Insert)
				}})
				if err != nil {
					t.Fatal(err)
				}
				return w
			}
			w := open()
			poll := func(wantReady bool) {
				t.Helper()
				round, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				var got bool
				err := w.RunIndexing(round, func(ready bool) { got = ready; cancel() })
				if !errors.Is(err, context.Canceled) || got != wantReady {
					t.Fatalf("ready=%t error=%v", got, err)
				}
			}
			assertState := func(wantCalls []string, want map[string]string) {
				t.Helper()
				rows, err := db.Read(ctx, "ledger")
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for _, row := range rows.Rows {
					got[fmt.Sprint(row["id"])] = fmt.Sprint(row["value"])
				}
				if !reflect.DeepEqual(calls, wantCalls) || !reflect.DeepEqual(got, want) {
					t.Fatalf("calls=%v ledger=%v, want %v %v", calls, got, wantCalls, want)
				}
			}
			original := call(a, "evm_snapshot")
			mine(a, 10)
			offlineB.Store(true)
			poll(false)
			assertState([]string{"A10"}, map[string]string{})
			offlineB.Store(false)
			mine(b, 40)
			poll(true)
			assertState([]string{"A10", "B40"}, map[string]string{"peer0x1": "original"})
			if err := db.Backend.Close(); err != nil {
				t.Fatal(err)
			}
			w = open()
			if call(a, "evm_revert", original) != true {
				t.Fatal("restore original fork")
			}
			mine(a, 20)
			offlineA.Store(true)
			offlineB.Store(true)
			poll(false)
			assertState([]string{"A10", "B40"}, map[string]string{"peer0x1": "original"})
			offlineA.Store(false)
			poll(false)
			assertState([]string{"A10", "B40"}, map[string]string{})
			if err := db.Backend.Close(); err != nil {
				t.Fatal(err)
			}
			w = open()
			poll(false)
			assertState([]string{"A10", "B40"}, map[string]string{})
			offlineB.Store(false)
			poll(true)
			wantCalls := []string{"A10", "B40", "A20", "B40"}
			want := map[string]string{"branch": "replacement", "peer0x1": "replacement"}
			assertState(wantCalls, want)
			empty := call(a, "evm_snapshot")
			mine(a, 25) // This block has no matching callback.
			mine(b, 50)
			poll(true)
			wantCalls = append(wantCalls, "B50")
			want["peer0x2"] = "replacement"
			if call(a, "evm_revert", empty) != true {
				t.Fatal("restore empty block fork")
			}
			mine(a, 27)
			poll(true)
			assertState(wantCalls, want)
			if err := db.Backend.Close(); err != nil {
				t.Fatal(err)
			}
			w = open()
			poll(true)
			assertState(wantCalls, want)
			// A peer's finality proof still binds when its business receipt
			// has been undone and its RPC was down during the rewind.
			forkA, forkB := call(a, "evm_snapshot"), call(b, "evm_snapshot")
			mine(a, 60)
			mine(b, 70)
			poll(true)
			if call(a, "evm_revert", forkA) != true {
				t.Fatal("restore chain A")
			}
			mine(a, 65)
			offlineB.Store(true)
			poll(false)
			if call(b, "evm_revert", forkB) != true {
				t.Fatal("restore chain B")
			}
			mine(b, 75)
			offlineB.Store(false)
			round, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := w.RunIndexing(round, func(bool) {}); err == nil || !strings.Contains(err.Error(), "before safe checkpoint") {
				t.Fatalf("changed finalized peer hash: %v", err)
			}

		})
	}
}
