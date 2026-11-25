package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestHeadNotifications(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			const chainID = 931
			endpoint := testanvil.StartChain(t, chainID, "--no-mining")
			wsRPC := rpc.New("ws" + strings.TrimPrefix(endpoint, "http"))
			mine := func() {
				t.Helper()
				var out any
				if err := rpc.New(endpoint).Call(ctx, "evm_mine", []any{}, &out); err != nil {
					t.Fatal(err)
				}
			}
			var offline atomic.Bool
			offline.Store(true)
			// The gateway forwards Anvil's real headers and gives the test control
			// of the notification transport independently of block acquisition.
			accepted := make(chan chan struct{}, 4)
			refused := make(chan struct{}, 1)
			upgrader := websocket.Upgrader{}
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if offline.Load() {
					select {
					case refused <- struct{}{}:
					default:
					}
					http.Error(w, "offline", http.StatusServiceUnavailable)
					return
				}
				ws, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				var req struct {
					Method string
					Params []string
					ID     uint64
				}
				if err := ws.ReadJSON(&req); err != nil || req.Method != "eth_subscribe" || len(req.Params) != 1 || req.Params[0] != "newHeads" {
					t.Errorf("subscription %+v: %v", req, err)
					return
				}
				stream, cancel := context.WithCancel(r.Context())
				defer cancel()
				heads, err := wsRPC.Subscribe(stream, "newHeads")
				if err != nil {
					t.Errorf("Anvil subscription: %v", err)
					return
				}
				if err := ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "sub"}); err != nil {
					return
				}
				cut := make(chan struct{})
				accepted <- cut
				go func() { _, _, _ = ws.ReadMessage(); cancel() }()
				for {
					select {
					case <-cut:
						return
					case <-stream.Done():
						return
					case head, ok := <-heads:
						if !ok {
							return
						}
						params := map[string]any{"subscription": "sub", "result": json.RawMessage(head)}
						if err := ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "eth_subscription", "params": params}); err != nil {
							return
						}
					}
				}
			}))
			t.Cleanup(gateway.Close)
			backend := stagedBackend(t, ctx, dialect, filepath.Join(t.TempDir(), "data"))
			t.Cleanup(func() { _ = backend.Close() })
			ns := fmt.Sprintf("ponder_go_heads_%x", time.Now().UnixNano())
			model := schema.Schema{Tables: []schema.Table{{Name: "blocks", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
			db, err := store.New(ctx, backend, model, ns, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			wsURL := "ws" + strings.TrimPrefix(gateway.URL, "http")
			cfg := config.Config{Chains: []config.Chain{{Name: "A", ID: chainID, RPC: []string{endpoint}, WS: wsURL, PollingIntervalMS: 60000}}, Blocks: []config.BlockSource{{Chain: "A", Name: "Head", Interval: 1, StartBlock: 1}}}
			handlers := map[string]Handler{"Head:block": func(ctx context.Context, c *Context) error {
				return c.DB.Write(ctx, "blocks", store.Row{"id": c.Event.Block.Number}, store.Insert)
			}}
			start := func() (string, func()) {
				t.Helper()
				worker, err := NewIndexer(cfg, db, handlers)
				if err != nil {
					t.Fatal(err)
				}
				run, cancel := context.WithCancel(ctx)
				done := make(chan struct{})
				ready := make(chan struct{}, 1)
				var result error
				go func() {
					defer close(done)
					result = worker.RunIndexing(run, func(v bool) {
						if v {
							select {
							case ready <- struct{}{}:
							default:
							}
						}
					})
				}()
				stop := func() { cancel(); <-done }
				t.Cleanup(stop)
				select {
				case <-ready:
				case <-done:
					t.Fatalf("indexer exited: %v", result)
				case <-time.After(12 * time.Second):
					t.Fatal("indexer did not become ready")
				}
				return worker.(*Engine).filter, func() {
					stop()
					if !errors.Is(result, context.Canceled) {
						t.Fatal(result)
					}
				}
			}
			waitRows := func(count int) {
				t.Helper()
				deadline := time.Now().Add(12 * time.Second)
				for time.Now().Before(deadline) {
					rows, err := db.Read(ctx, "blocks")
					if err != nil {
						t.Fatal(err)
					}
					if len(rows.Rows) == count {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatalf("did not index %d blocks", count)
			}
			connect := func() chan struct{} {
				t.Helper()
				select {
				case cut := <-accepted:
					return cut
				case <-time.After(12 * time.Second):
					t.Fatal("subscription was not accepted")
					return nil
				}
			}
			disconnected := func() {
				t.Helper()
				select {
				case <-refused:
				case <-time.After(12 * time.Second):
					t.Fatal("missing rejected WebSocket handshake")
				}
			}
			identity, stop := start()
			disconnected()
			mine()
			offline.Store(false)
			cut := connect()
			waitRows(1)
			mine()
			waitRows(2)
			offline.Store(true)
			select {
			case <-refused:
			default:
			}
			close(cut)
			disconnected()
			mine()
			offline.Store(false)
			connect()
			mine()
			waitRows(4)
			stop()
			cfg.Chains[0].WS = ""
			httpOnly, err := NewIndexer(cfg, db, handlers)
			if err != nil || httpOnly.(*Engine).filter != identity {
				t.Fatalf("subscription changed the indexed ranges: %v", err)
			}
			cfg.Chains[0].WS = endpoint
			if err := cfg.Validate(); err == nil {
				t.Fatal("HTTP URL accepted for WebSocket notifications")
			}
			cfg.Chains[0].WS = wsURL
			cfg.Chains[0].PollingIntervalMS = 40
			offline.Store(true)
			_, stop = start()
			mine()
			waitRows(5)
			stop()
		})
	}
}
