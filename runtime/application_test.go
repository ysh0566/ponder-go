package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestApplicationShutdownJoinsHandler(t *testing.T) {
	ctx := t.Context()
	endpoint := testanvil.Start(t, "--no-mining")
	b := stagedBackend(t, ctx, "pglite", "memory://")
	defer b.Close()
	db, err := store.New(ctx, b, schema.Schema{}, "ponder_go_shutdown", schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	finishHandler := sync.OnceFunc(func() { close(release) })
	cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{endpoint}, PollingIntervalMS: 15}}, Blocks: []config.BlockSource{{Name: "head", Chain: "local", Interval: 1, StartBlock: 1}}}
	engine, err := New(cfg, db, map[string]Handler{"head:block": func(ctx context.Context, c *Context) error {
		close(entered)
		<-release
		return ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	routes := query.New(db)
	app, err := StartApplication(ctx, engine, routes, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		finishHandler()
		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(timeout); err != nil {
			t.Error(err)
		}
	}()
	client := rpc.New(endpoint)
	var result any
	if err := client.Call(ctx, "evm_mine", []any{}, &result); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not enter")
	}
	timeout, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := app.Shutdown(timeout); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown returned before handler finished: %v", err)
	}
	if routes.Ready.Load() {
		t.Fatal("stopping app still ready")
	}
	finishHandler()
	if err := app.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationServeOnly(t *testing.T) {
	t.Setenv("PONDER_COMMAND", "")
	t.Setenv("PONDER_HOSTNAME", "127.0.0.1")
	t.Setenv("PONDER_PORT", "0")
	ctx, cancel := context.WithCancel(t.Context())
	b := stagedBackend(t, ctx, "pglite", "memory://")
	defer b.Close()
	db, err := store.New(ctx, b, schema.Schema{}, fmt.Sprintf("ponder_go_serve_%x", time.Now().UnixNano()), schema.Multichain)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	routes := query.New(db)
	app, err := StartApplication(ctx, nil, routes, "invalid host name", -1)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if !routes.Ready.Load() {
		cancel()
		t.Fatal("query-only application did not become ready")
	}
	body, err := routes.Metrics.Collect(ctx)
	if err != nil || !strings.Contains(body, `ponder_settings_info{command="serve",database="pglite"} 1`) {
		cancel()
		t.Fatalf("query-only process settings: %s %v", body, err)
	}
	cancel()
	if err := app.Wait(); err != nil || routes.Ready.Load() {
		t.Fatalf("query-only shutdown: %v", err)
	}
}
