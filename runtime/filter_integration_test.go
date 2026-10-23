package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Native end-to-end boundary evidence, not an original Foundry test mapping.
// Real EVM logs include a superset after the source's >50-address widening.
func TestFilterWideRequestRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache_disabled_%t", dialect, disabled), func(t *testing.T) {
				ctx := context.Background()
				client, endpoint := localChain(t)
				backend := stagedBackend(t, ctx, dialect, "memory://")
				defer backend.Close()
				allowed := "0x1000000000000000000000000000000000000001"
				foreign := "0x2000000000000000000000000000000000000001"
				wrongTopic := "0x1000000000000000000000000000000000000002"
				sig := "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
				word := strings.Repeat("0", 64)
				for _, address := range []string{allowed, foreign, wrongTopic} {
					topic := sig
					if address == wrongTopic {
						topic = strings.Repeat("f", 64)
					}
					code := "0x6000356000527f" + word + "7f" + word + "7f" + topic + "60206000a300"
					var ok any
					if err := client.Call(ctx, "anvil_setCode", []any{address, code}, &ok); err != nil {
						t.Fatal(err)
					}
				}
				var accounts []string
				if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil {
					t.Fatal(err)
				}
				send := func(address string, amount int) {
					t.Helper()
					var hash string
					if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": address, "gas": "0x186a0", "data": fmt.Sprintf("0x%064x", amount)}}, &hash); err != nil {
						t.Fatal(err)
					}
					deadline := time.Now().Add(5 * time.Second)
					for {
						var receipt map[string]any
						if err := client.Call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
							t.Fatal(err)
						}
						if receipt != nil {
							if receipt["status"] != "0x1" {
								t.Fatal("actual EVM fixture transaction", receipt)
							}
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("actual EVM fixture was not mined")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				send(allowed, 5)
				send(foreign, 999)
				send(wrongTopic, 17)
				u, _ := url.Parse(endpoint)
				proxy := httputil.NewSingleHostReverseProxy(u)
				var wide atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Observe without changing actual RPC request/response behavior.
					var body struct {
						Method string
						Params []map[string]json.RawMessage
					}
					if r.Body != nil {
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							http.Error(w, err.Error(), 400)
							return
						}
						r.Body = io.NopCloser(bytes.NewReader(raw))
						_ = json.Unmarshal(raw, &body)
					}
					if body.Method == "eth_getLogs" && len(body.Params) == 1 {
						if _, exists := body.Params[0]["address"]; !exists {
							wide.Add(1)
						}
					}
					proxy.ServeHTTP(w, r)
				}))
				defer server.Close()
				addresses := []string{allowed, wrongTopic}
				for i := 0; i < 49; i++ {
					addresses = append(addresses, fmt.Sprintf("0x%040x", i+500))
				}
				cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{server.URL}}}, Contracts: []config.Contract{{Name: "Token", Chain: "local", ABI: []byte(transferABI), Address: addresses, StartBlock: 1}}}
				d, err := store.NewLifecycle(backend, stagedBlockModel(), fmt.Sprintf("ponder_go_wide_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close()
				count := 0
				handlers := map[string]Handler{"Token:Transfer": func(ctx context.Context, c *Context) error {
					count++
					if c.Event.Log.Address != allowed {
						return fmt.Errorf("unmatched log reached handler")
					}
					return c.DB.Write(ctx, "account", store.Row{"id": allowed, "balance": "5"}, store.Insert)
				}}
				engine, err := NewStaged(cfg, d, handlers, StagedOptions{Options: Options{DisableCache: disabled}, BuildID: "native-filter-wide"})
				if err != nil {
					t.Fatal(err)
				}
				engine.Client.RequestsPerSecond = 0
				if err = engine.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				rows, err := d.RuntimeStore().Read(ctx, "account")
				if err != nil || len(rows.Rows) != 1 || count != 1 {
					t.Fatal("local address/topic filtering", rows, count, err)
				}
				if wide.Load() != 3 {
					t.Fatal("expected one widened actual RPC per block1..3", wide.Load())
				}
				if err = engine.Sync(ctx); err != nil || count != 1 {
					t.Fatal("repeat duplicated filtered event", err, count)
				}
			})
		}
	}
}
