package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestApplicationRPCRecovery(t *testing.T) {
	ctx := t.Context()
	chain := testanvil.Start(t, "--no-mining")
	target, err := url.Parse(chain)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var offline atomic.Bool
	offline.Store(true)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "temporarily unavailable", 503)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	backend := stagedBackend(t, ctx, "pglite", "memory://")
	defer backend.Close()
	db, err := store.New(ctx, backend, schema.Schema{}, "ponder_go_rpc_resume", schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	var handled atomic.Int64
	cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{gateway.URL}, PollingIntervalMS: 20}}, Blocks: []config.BlockSource{{Name: "head", Chain: "local", Interval: 1, StartBlock: 1}}}
	engine, err := New(cfg, db, map[string]Handler{"head:block": func(context.Context, *Context) error { handled.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	engine.Client.Retries = 0
	routes := query.New(db)
	app, err := LaunchApplication(ctx, engine, routes, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown(context.Background())
	await := func(path string, want int) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			r, e := http.Get(fmt.Sprintf("http://%s%s", app.Addr(), path))
			if e != nil {
				t.Fatal(e)
			}
			r.Body.Close()
			if r.StatusCode == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s did not return %d", path, want)
	}
	await("/health", 200)
	await("/ready", 503)
	await("/status", 200)
	offline.Store(false)
	await("/ready", 200)
	offline.Store(true)
	await("/ready", 503)
	var mined any
	if err := rpc.New(chain).Call(ctx, "evm_mine", []any{}, &mined); err != nil {
		t.Fatal(err)
	}
	offline.Store(false)
	await("/ready", 200)
	if handled.Load() != 1 {
		t.Fatalf("recovered handler executions: %d", handled.Load())
	}
}
