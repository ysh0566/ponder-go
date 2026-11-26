package historical

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// Hold one ordinary block acquisition until the peer has run its last handler.
func isolatedSlowNode(t *testing.T, endpoint string, release <-chan struct{}) string {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct {
			Method string
			Params []any
		}
		if json.Unmarshal(body, &req) == nil && req.Method == "eth_getBlockByNumber" && len(req.Params) == 2 && req.Params[0] == "0x1" && req.Params[1] == true {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestRuntimeIsolatedFinalized(t *testing.T) {
	nodes := map[uint64]omniAccountChain{
		101: mineOmniAccountChain(t, 101, []uint64{40, 50, 60, 70}),
		202: mineOmniAccountChain(t, 202, []uint64{5, 10, 15}),
	}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			release := make(chan struct{})
			releasePeer := sync.OnceFunc(func() { close(release) })
			firstURL := isolatedSlowNode(t, nodes[101].url, release)
			raw := fmt.Sprintf(`{"ordering":"experimental_isolated","chains":{"first":{"id":101,"rpc":%q,"reorgWindow":0},"second":{"id":202,"rpc":%q,"reorgWindow":0,"cacheRpcRequests":false}},"blocks":{"Head":{"chain":{"first":{"startBlock":0,"endBlock":3},"second":{"startBlock":1,"endBlock":2}},"interval":1}},"accounts":{"Wallet":{"chain":{"first":{},"second":{}},"address":%q,"startBlock":1,"endBlock":2}}}`, firstURL, nodes[202].url, nodes[101].wallet)
			cfg, err := config.ParseSourceConfiguration([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{})
			if err != nil {
				t.Fatal(err)
			}
			reject := true
			calls := map[uint64][]uint64{}
			handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				n, err := continuousNumber(a)
				if err != nil {
					return err
				}
				calls[id] = append(calls[id], n)
				key := store.Row{"chainId": int(id), "height": int(n), "kind": "block"}
				_, err = a.Context.DB.Find(ctx, "seen", store.Row{"chainId": 303, "height": int(n), "kind": "block"})
				var access *store.InvalidStoreAccessError
				if !errors.As(err, &access) {
					return fmt.Errorf("expected chain-scoped lookup, got %v", err)
				}
				if _, err = a.Context.DB.InsertOne(ctx, "seen", key, store.Conflict{}); err != nil {
					return err
				}
				if id == 202 && n == 2 {
					releasePeer()
				}
				if reject && id == 101 && n == 2 {
					_, err := a.Context.DB.Delete(ctx, "seen", store.Row{"chainId": 202, "height": 2, "kind": "block"})
					if !errors.As(err, &access) {
						return fmt.Errorf("expected chain-scoped delete, got %v", err)
					}
					return fmt.Errorf("interrupted isolated callback: %w", err)
				}
				return nil
			})
			txCalls := map[uint64]int{}
			onTransaction := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				txCalls[id]++
				n, err := continuousNumber(a)
				if err != nil {
					return err
				}
				_, err = a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "height": int(n), "kind": "tx"}, store.Conflict{})
				return err
			})
			build, err := config.BuildSourceIndexing(ctx, cfg, base, []config.SourceIndexingFunction{{Name: "Head:block", Fn: handler}, {Name: "Wallet:transaction:to", Fn: onTransaction}})
			if err != nil {
				t.Fatal(err)
			}
			f := newManagedFiniteFixture(t, backend, build)
			f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "seen", PrimaryKey: []string{"chain_id", "height", "kind"}, Columns: []schema.Column{
				{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "height", Type: "integer", NotNull: true}, {Name: "kind", Type: "text", NotNull: true},
			}}}}
			f.options.Owned.Storage.Ordering = schema.Isolated
			opts := RuntimeOptions{BuildID: "isolated-finalized", Historical: runtimeFixtureInput(f), Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
				return []rpc.Requester{rpc.New(firstURL), rpc.New(nodes[202].url)}, nil
			}}
			for _, owner := range []string{"first", "resume", "complete"} {
				opts.OpenID = owner
				r := runtimeOpen(t, backend, opts)
				result, err := r.Run(ctx)
				if reject {
					if err == nil || !strings.Contains(err.Error(), "interrupted isolated callback") || result.Ready {
						t.Fatalf("interruption: %+v, %v", result, err)
					}
				} else if err != nil || !result.Ready || !result.Completed {
					t.Fatalf("reopen: %+v, %v", result, err)
				}
				if owner == "complete" && result.HistoricalBlocks != 0 {
					t.Fatalf("completed range was replayed: %+v", result)
				}
				if !reject {
					c, err := r.Control(ctx)
					if err != nil || c.Version != "managed-live-isolated-finalized-v1" || c.Tail != c.SafeOrdinal || !maps.Equal(c.LogicalCursors, c.LogicalSafe) {
						t.Fatalf("unsealed execution: %+v, %v", c, err)
					}
					for id, end := range map[uint64]uint64{101: 3, 202: 2} {
						cp, err := checkpoint.Decode(c.LogicalSafe[id])
						if err != nil || cp.ChainID != id || cp.BlockNumber != end || c.Cursors[id].Hash != nodes[id].hash[end] {
							t.Fatalf("wrong safe cursor on chain %d: %+v, %v", id, c, err)
						}
					}
				}
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
					rows := managedRows(t, db, "SELECT chain_id::text || '/' || height::text || '/' || kind AS key FROM "+ns+".seen ORDER BY chain_id,height,kind")
					var got []string
					for _, row := range rows {
						got = append(got, fmt.Sprint(row["key"]))
					}
					want := []string{"101/0/block", "101/1/block", "101/1/tx", "101/2/block", "101/3/block", "202/1/block", "202/1/tx", "202/2/block"}
					if reject {
						want = []string{"101/0/block", "101/1/block", "101/1/tx", "202/1/block", "202/1/tx", "202/2/block"}
					}
					if !slices.Equal(got, want) {
						t.Fatalf("durable rows %v, want %v", got, want)
					}
				})
				reject = false
			}
			if !maps.Equal(txCalls, map[uint64]int{101: 1, 202: 1}) {
				t.Fatalf("unexpected transaction callback replay: %v", txCalls)
			}
			if !slices.Equal(calls[101], []uint64{0, 1, 2, 2, 3}) || !slices.Equal(calls[202], []uint64{1, 2}) {
				t.Fatalf("unexpected callback replay: %v", calls)
			}
		})
	}
}

func TestRuntimeIsolatedPreflight(t *testing.T) {
	for _, mode := range []string{"live", "setup", "source-finality", "schema"} {
		t.Run(mode, func(t *testing.T) {
			p := newFiniteRPC(t, 5)
			handler := indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil })
			b := runtimeBuild(t, p, 2, 1, handler)
			x := b.Executable()
			x.Ordering = string(schema.Isolated)
			var epochs []uint64
			o := RuntimeOptions{BuildID: "preflight", OpenID: "admission", Requesters: runtimeFactory(p, &epochs),
				Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: store.IndependentStorageOptions{
					PostgresURL: "invalid-before-connection", BusinessNamespace: "isolated_business", SyncNamespace: "isolated_sync",
				}}}}
			var reason string
			switch mode {
			case "live":
				o.Live = &RuntimeLiveOptions{}
				reason = "bounded finalized"
			case "setup":
				x.SetupCallbacks[0] = []indexing.Object{{"name": "Token:setup", "fn": handler}}
				reason = "per-chain bootstrap"
			case "source-finality":
				x.EventCallbacks[0][0]["filter"].(map[string]any)["toBlock"] = float64(5)
				reason = "exceeds chain finality"
			case "schema":
				o.Historical.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
				reason = "required 'chainId' column"
			}
			_, err := OpenRuntimePostgres(t.Context(), o)
			if err == nil || !strings.Contains(err.Error(), reason) {
				t.Fatalf("expected %q before opening storage, got %v", reason, err)
			}
			if (mode == "live" || mode == "setup") && len(epochs) != 0 {
				t.Fatalf("requested RPC for unsupported %s", mode)
			}
		})
	}
}
