package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/devstatus"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestDevMetricSamples(t *testing.T) {
	wire := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID uint64 }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"0x1"}`, req.ID)
	}))
	defer wire.Close()
	first := rpc.New(wire.URL)
	first.RequestsPerSecond = 0
	request := func() {
		t.Helper()
		if err := first.Call(t.Context(), "eth_chainId", []any{}, new(string)); err != nil {
			t.Fatal(err)
		}
	}
	request() // This request predates the watched application.
	m := newDevMonitor()
	m.register(&Engine{chain: config.Chain{ID: 1, Name: "1"}, Client: first})
	m.register(&Engine{chain: config.Chain{ID: 2, Name: "2"}, Client: rpc.New()})
	for _, id := range []uint64{1, 2} {
		m.setRange(id, 100, 50)
	}
	rateStart := m.chains[1].requests[0].at
	for i := 0; i < 6; i++ {
		request()
	}
	var rates devstatus.Snapshot
	m.sample(rateStart.Add(2*time.Second), nil, &rates)
	if got := rates.Chains["1"].RequestsPerSecond; got != 3 || rates.Chains["2"].RequestsPerSecond != 0 {
		t.Fatal(rates.Chains)
	}
	now := m.chains[1].backfill[0].at
	sample := func(after time.Duration) devstatus.ChainMetrics {
		var r devstatus.Snapshot
		m.sample(now.Add(after), nil, &r)
		return r.Chains["1"]
	}
	if p := sample(0).Backfill; p.Completed != 50 || p.ETASeconds != nil {
		t.Fatal(p)
	}
	for i := 0; i < 5; i++ {
		m.completeBlock(1)
	}
	if p := sample(5 * time.Second).Backfill; p.ETASeconds != nil {
		t.Fatal(p)
	}
	for i := 0; i < 5; i++ {
		m.completeBlock(1)
	}
	p := sample(10 * time.Second).Backfill
	if p.Completed != 60 || p.ETASeconds == nil || math.Abs(*p.ETASeconds-40) > 0.001 {
		t.Fatal(p)
	}
	m.rewind(2, false)
	if sample(11*time.Second).Backfill == nil {
		t.Fatal("isolated rewind discarded peer range")
	}
	m.rewind(2, true)
	if sample(12*time.Second).Backfill != nil {
		t.Fatal("coordinated rewind advertised a stale range")
	}
	m.setRange(1, 80, 20)
	if p = sample(13 * time.Second).Backfill; p.Completed != 20 || p.ETASeconds != nil {
		t.Fatal(p)
	}
	ctx := context.WithValue(t.Context(), devMonitorKey{}, m)
	c := &Context{Event: Event{Name: "Head:block"}}
	stop := errors.New("failed callback")
	if err := invokeHandler(ctx, func(context.Context, *Context) error { return stop }, c); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	var r devstatus.Snapshot
	m.sample(now, nil, &r)
	if r.Events["Head:block"].Count != 0 {
		t.Fatal("failed callback counted")
	}
	for i := 0; i < 100; i++ {
		m.sample(rateStart.Add(time.Duration(i+20)*time.Second), nil, &rates)
	}
	if rates.Chains["1"].RequestsPerSecond != 0 {
		t.Fatal("stale requests stayed in the rate window")
	}

}

// Hold the first chain at the block fetch following genesis. The database is
// free to report a partially completed backfill on either backend.
type devBlockGate chan struct{}

func (gate devBlockGate) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := r.GetBody()
	if err != nil {
		return nil, err
	}
	var req struct {
		Method string
		Params []json.RawMessage
	}
	err = json.NewDecoder(body).Decode(&req)
	body.Close()
	if err != nil {
		return nil, err
	}
	if req.Method == "eth_getBlockByNumber" && len(req.Params) > 0 && string(req.Params[0]) == `"0x1"` {
		select {
		case <-gate:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestApplicationDevMetrics(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "progress")
			t.Setenv("PONDER_COMMAND", "dev")
			t.Setenv(devstatus.FileEnv, path)
			zero := 0
			cfg := config.Config{}
			for i, name := range []string{"a", "b"} {
				id := uint64(i + 1)
				end := id
				url := testanvil.StartChain(t, id, "--no-mining")
				if err := rpc.New(url).Call(ctx, "anvil_mine", []any{Hex(end)}, new(any)); err != nil {
					t.Fatal(err)
				}
				cfg.Chains = append(cfg.Chains, config.Chain{Name: name, ID: id, RPC: []string{url}, ReorgWindow: &zero})
				cfg.Blocks = append(cfg.Blocks, config.BlockSource{Name: "Head", Chain: name, EndBlock: &end, Interval: 1})
			}
			b := stagedBackend(t, ctx, dialect, "memory://")
			defer b.Close()
			db, err := store.New(ctx, b, schema.Schema{}, fmt.Sprintf("ponder_go_devmeter_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			routes := query.New(db)
			routes.ChainNames[1] = "first"
			fns := map[string]Handler{"Head:block": func(ctx context.Context, c *Context) error {
				var result string
				return c.Client.Call(ctx, "eth_chainId", []any{}, &result)
			}}
			for _, want := range []uint64{5, 0} {
				w, err := NewIndexerWithOptions(cfg, db, fns, Options{DisableCache: true})
				if err != nil {
					t.Fatal(err)
				}
				var gate devBlockGate
				if want > 0 {
					gate = make(devBlockGate)
					w.(*orderedIndexer).workers[0].Client.HTTP.Transport = gate
				}
				// A new dev child has a fresh status path and fresh counters.
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				app, err := LaunchApplication(ctx, w, routes, "127.0.0.1", 0)
				if err != nil {
					t.Fatal(err)
				}
				defer app.Shutdown(context.Background())
				body, err := routes.Metrics.Collect(ctx)
				settings := fmt.Sprintf(`ponder_settings_info{command="dev",database="%s",ordering="multichain"} 1`, dialect)
				if err != nil || !strings.Contains(body, settings) {
					t.Fatalf("dev metrics missing %s: %v", settings, err)
				}
				var report devstatus.Snapshot
				for {
					if ctx.Err() != nil {
						t.Fatalf("report did not catch up: %+v", report)
					}
					bytes, err := os.ReadFile(path)
					report = devstatus.Snapshot{}
					if err == nil && json.Unmarshal(bytes, &report) == nil {
						p := report.Chains["first"].Backfill
						if gate != nil && !report.Ready && p != nil && p.Total == 2 && p.Completed == 1 && report.Events["Head:block"].Count > 0 {
							close(gate)
							gate = nil
						}
						if report.Ready {
							break
						}
					}
					select {
					case <-app.done:
						t.Fatalf("application stopped: %v", app.Wait())
					case <-time.After(30 * time.Millisecond):
					}
				}
				if gate != nil {
					t.Fatal("missing partial backfill observation")
				}
				if report.Events["Head:block"].Count != want || (report.Events["Head:block"].AverageMillis > 0) != (want > 0) {
					t.Fatal(report.Events)
				}
				for name, total := range map[string]uint64{"first": 2, "b": 3} {
					stats := report.Chains[name]
					if report.Blocks[name] != total-1 || stats.Backfill == nil || stats.Backfill.Total != total || stats.Backfill.Completed != total || stats.RequestsPerSecond <= 0 || stats.Backfill.ETASeconds != nil {
						t.Fatalf("chain %s: %+v", name, report)
					}
				}
				if err := app.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
