package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"strings"
	"testing"
	"time"

	"os"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
)

type factoryTraceGolden struct {
	factoryTransferGolden
	ABI            json.RawMessage `json:"abi"`
	CallbackNames  []string        `json:"callback_names"`
	ExpectedTraces []struct {
		ChainID uint64
		Branch  string
		Block   uint64
		Events  []struct {
			Name, TransactionHash, Checkpoint string
			TransactionIndex                  uint64
			Args, Result, Value, Output       any
			From, To, Type, Input             string
			TraceAddress                      []int
			HasReceipt                        bool
			ReceiptStatus                     string
		}
	}
}

func factoryTraceGoldens(t *testing.T) factoryTraceGolden {
	t.Helper()
	data, err := os.ReadFile("testdata/factory-traces-17c6d5ba.json")
	if err != nil {
		t.Fatal(err)
	}
	var g factoryTraceGolden
	if err = json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	if g.SourceCommit != "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334" || len(g.CallbackNames) != 4 || len(g.ExpectedTraces) != 14 {
		t.Fatal("missing actual pinned contract trace oracle")
	}
	return g
}
func factoryTraceBuild(t *testing.T, p *finiteRPC, g factoryTraceGolden, start, window uint64, cache, receipts bool, h func(string) indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":%d,"cacheRpcRequests":%t},"two":{"id":2,"rpc":%q,"reorgWindow":%d,"cacheRpcRequests":%t}},"contracts":{"Worker":{"abi":%s,"address":{"address":"0x1111111111111111111111111111111111111111","event":{"type":"event","name":"Created","inputs":[{"name":"child","type":"address","indexed":true}]},"parameter":"child","startBlock":1,"endBlock":2},"chain":{"one":{},"two":{}},"startBlock":%d,"endBlock":4,"includeCallTraces":true,"includeTransactionReceipts":%t}}}`, p.server.URL+"/1", window, cache, p.server.URL+"/2", window, cache, g.ABI, start, receipts)
	if !receipts {
		raw = strings.Replace(raw, `,"includeTransactionReceipts":false`, "", 1)
	}
	cfg, err := config.ParseSourceConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range base.RPCClients() {
		r.RequestsPerSecond = 0
		r.Retries = 0
	}
	functions := []config.SourceIndexingFunction{}
	for _, name := range g.CallbackNames {
		functions = append(functions, config.SourceIndexingFunction{Name: name, Fn: h(name)})
	}
	b, err := config.BuildSourceIndexing(context.Background(), cfg, base, functions)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Normalize only representation differences; expected values come from actual
// pinned buildEvents/decodeEvents, including undefined, tuple names and bigints.
func factoryTraceValue(v any) any {
	if filter.IsUndefined(v) {
		return map[string]any{"$undefined": true}
	}
	switch x := v.(type) {
	case *big.Int:
		return x.String()
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = factoryTraceValue(v)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			out[k] = factoryTraceValue(v)
		}
		return out
	}
	return v
}
func factoryTraceSame(got, want any) bool {
	a, err := json.Marshal(factoryTraceValue(got))
	if err != nil {
		return false
	}
	b, err := json.Marshal(want)
	return err == nil && string(a) == string(b)
}
func factoryTraceHandler(g factoryTraceGolden, receipts bool) func(string) indexing.Handler {
	return func(name string) indexing.Handler {
		return func(ctx context.Context, a *indexing.HandlerArgs) error {
			tx, err := factoryTransferProperty(a, "transaction", "hash")
			if err != nil {
				return err
			}
			for _, block := range g.ExpectedTraces {
				if block.ChainID != finiteChain(a) {
					continue
				}
				for _, e := range block.Events {
					if e.Name != name || e.TransactionHash != tx || e.Checkpoint != a.Event.Get("id") {
						continue
					}
					for key, want := range map[string]any{"args": e.Args, "result": e.Result} {
						got := a.Event.Get(key)
						if !factoryTraceSame(got, want) {
							return fmt.Errorf("actual ABI oracle %s got=%v want=%v", key, got, want)
						}
					}
					for component, fields := range map[string]map[string]any{
						"transaction": {"transactionIndex": e.TransactionIndex},
						"trace":       {"from": e.From, "to": e.To, "type": e.Type, "input": e.Input, "output": e.Output, "value": e.Value},
					} {
						for key, want := range fields {
							got, err := factoryTransferProperty(a, component, key)
							if err != nil {
								return err
							}
							if !factoryTraceSame(got, want) {
								return fmt.Errorf("trace oracle %s.%s got=%v want=%v", component, key, got, want)
							}
						}
					}
					address, err := factoryTransferProperty(a, "trace", "traceAddress")
					if err != nil {
						return err
					}
					if fmt.Sprint(address) != fmt.Sprint(e.TraceAddress) {
						return fmt.Errorf("traceAddress got=%v want=%v", address, e.TraceAddress)
					}
					if receipts {
						status, err := factoryTransferProperty(a, "transactionReceipt", "status")
						if err != nil {
							return err
						}
						if status != e.ReceiptStatus {
							return fmt.Errorf("receipt status got=%v want=%v", status, e.ReceiptStatus)
						}
					} else if !filter.IsUndefined(a.Event.Get("transactionReceipt")) {
						return fmt.Errorf("default false receipt exposed")
					}
					n, err := continuousNumber(a)
					if err != nil {
						return err
					}
					_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("%d:%s:%v", finiteChain(a), name, a.Event.Get("id")), "balance": big.NewInt(int64(n))}, store.Conflict{})
					return err
				}
			}
			return fmt.Errorf("unexpected function event: chain=%d name=%s checkpoint=%v tx=%v", finiteChain(a), name, a.Event.Get("id"), tx)
		}
	}
}
func factoryTraceExpected(g factoryTraceGolden, branches map[uint64]string, start uint64) map[string]string {
	out := map[string]string{}
	for _, block := range g.ExpectedTraces {
		if block.Branch != branches[block.ChainID] || block.Block < start {
			continue
		}
		for _, e := range block.Events {
			out[fmt.Sprintf("%d:%s:%s", block.ChainID, e.Name, e.Checkpoint)] = fmt.Sprint(block.Block)
		}
	}
	return out
}
func TestRuntimeFactoryTraceLifecycle(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			for _, start := range []uint64{1, 3} {
				t.Run(fmt.Sprintf("%s/cache_%t/start_%d", backend, cache, start), func(t *testing.T) {
					continuousRoot(t)
					g := factoryTraceGoldens(t)
					s, _ := factoryTraceRPC(t, 3, g)
					b := factoryTraceBuild(t, s.p, g, start, 1, cache, true, factoryTraceHandler(g, true))
					f := newManagedFiniteFixture(t, backend, b)
					epochs := []uint64{}
					o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "factory-trace-lifecycle", OpenID: "trace-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
					o.Live.OnProgress = func(_ context.Context, p RuntimeLiveProgress) error {
						if p.Type == "block" || p.Type == "ready" {
							s.setLatest(4)
						}
						return nil
					}
					r := runtimeOpen(t, backend, o)
					result, err := r.Run(context.Background())
					if err != nil || !result.Completed || !result.Ready {
						t.Fatalf("factory trace run=%+v err=%v", result, err)
					}
					if err = r.Close(context.Background()); err != nil {
						t.Fatal(err)
					}
					want := factoryTraceExpected(g, map[uint64]string{1: "old", 2: "old"}, start)
					factoryTransferRows(t, f, want)
					o.OpenID = "trace-reopen"
					o.Live.OnProgress = nil
					r = runtimeOpen(t, backend, o)
					result, err = r.Run(context.Background())
					if err != nil || !result.Completed || !result.Ready || !result.Recovered {
						t.Fatalf("factory trace reopen=%+v err=%v", result, err)
					}
					if err = r.Close(context.Background()); err != nil {
						t.Fatal(err)
					}
					factoryTransferRows(t, f, want)
				})
			}
		}
	}
}

// Factory trace and account transfer fixtures use the same raw provider protocol.
func factoryTraceRPC(t *testing.T, latest uint64, g factoryTraceGolden, receipts ...bool) (*continuousRPC, func(uint64)) {
	return factoryTransferRPC(t, latest, g.factoryTransferGolden, receipts...)
}

func TestRuntimeFactoryTraceReorg(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/cache_%t", backend, cache), func(t *testing.T) {
				continuousRoot(t)
				g := factoryTraceGoldens(t)
				s, fork := factoryTraceRPC(t, 3, g)
				b := factoryTraceBuild(t, s.p, g, 1, 600, cache, true, factoryTraceHandler(g, true))
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				var r *RuntimeRunner
				forked, reorg := false, 0
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "trace-reorg", OpenID: "old-fork", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				o.Live.OnProgress = func(ctx context.Context, p RuntimeLiveProgress) error {
					if p.Type == "block" && !forked {
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						both := len(c.Cursors) == 2
						for _, h := range c.Cursors {
							cp, err := checkpoint.Decode(h.Checkpoint)
							if err != nil {
								return err
							}
							both = both && cp.BlockNumber >= 3
						}
						if both {
							forked = true
							fork(1)
							s.setLatest(4)
						}
					}
					if p.Type == "reorg" {
						reorg++
						if yes, err := r.Ready(ctx); err != nil || yes {
							return fmt.Errorf("reorg exposed ready: %v %v", yes, err)
						}
					}
					return nil
				}
				r = runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || !forked || reorg != 1 {
					t.Fatalf("trace reorg=%+v forked=%v reorg=%d err=%v", out, forked, reorg, err)
				}
				c, err := r.Control(context.Background())
				if err != nil || len(c.ReplayTargets) != 0 || !c.Serving {
					t.Fatalf("replay not drained: %+v %v", c, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				want := factoryTraceExpected(g, map[uint64]string{1: "fresh", 2: "old"}, 1)
				factoryTransferRows(t, f, want)
				o.OpenID = "canonical-reopen"
				o.Live.OnProgress = nil
				r = runtimeOpen(t, backend, o)
				out, err = r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("reorg reopen=%+v err=%v", out, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				factoryTransferRows(t, f, want)
			})
		}
	}
}

func TestRuntimeFactoryTraceFailedCommit(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/cache_%t", backend, cache), func(t *testing.T) {
				continuousRoot(t)
				g := factoryTraceGoldens(t)
				s, _ := factoryTraceRPC(t, 4, g)
				failed := false
				retry := false
				oracle := factoryTraceHandler(g, true)
				h := func(name string) indexing.Handler {
					write := oracle(name)
					return func(ctx context.Context, a *indexing.HandlerArgs) error {
						if err := write(ctx, a); err != nil {
							return err
						}
						n, err := continuousNumber(a)
						if err != nil {
							return err
						}
						if !retry && finiteChain(a) == 1 && n == 3 {
							failed = true
							return fmt.Errorf("controlled trace handler failure after SQL write")
						}
						return nil
					}
				}
				b := factoryTraceBuild(t, s.p, g, 1, 600, cache, true, h)
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "trace-failure", OpenID: "failed-write", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				r := runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if err == nil || out.Completed || out.Ready || !failed {
					t.Fatalf("trace failure accepted=%+v fired=%v err=%v", out, failed, err)
				}
				c, err := r.Control(context.Background())
				if err != nil || store.ManagedLiveReachedBlock(c, 1, 3) {
					t.Fatalf("failed write advanced checkpoint: %+v %v", c, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id LIKE '1:%' AND balance>=3")
					if len(rows) != 0 {
						t.Fatalf("failed trace leaked SQL rows: %v", rows)
					}
				})
				retry = true
				o.OpenID = "retry-write"
				r = runtimeOpen(t, backend, o)
				out, err = r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("failed trace reopen=%+v err=%v", out, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				factoryTransferRows(t, f, factoryTraceExpected(g, map[uint64]string{1: "old", 2: "old"}, 1))
			})
		}
	}
}

type factoryTraceCrashSpec struct {
	continuousCrashSpec
	Cache bool
}

func factoryTraceCrashChild(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec factoryTraceCrashSpec
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	g := factoryTraceGoldens(t)
	p := &finiteRPC{server: &httptest.Server{URL: spec.RPC}}
	b := factoryTraceBuild(t, p, g, 1, 600, spec.Cache, true, factoryTraceHandler(g, true))
	epochs := []uint64{}
	var r *RuntimeRunner
	forked := false
	o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: spec.Storage, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}, BuildID: "trace-crash", OpenID: "app-child", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
	o.Live.OnDurableReorg = func(ctx context.Context, e store.ManagedLiveDomainStage) error {
		if e.Phase != spec.Phase {
			return nil
		}
		if yes, err := r.Ready(ctx); err != nil || yes {
			os.Exit(86)
		}
		marker, _ := json.Marshal(map[string]any{"phase": e.Phase, "operation": e.OperationID, "pid": os.Getpid(), "exit": 83, "postgres_engine_stays_running": true})
		if err := os.WriteFile(spec.Marker, marker, 0600); err != nil {
			os.Exit(87)
		}
		os.Exit(83)
		return nil
	}
	o.Live.OnProgress = func(ctx context.Context, e RuntimeLiveProgress) error {
		if e.Type != "block" || forked {
			return nil
		}
		c, err := r.Control(ctx)
		if err != nil {
			return err
		}
		both := len(c.Cursors) == 2
		for _, h := range c.Cursors {
			cp, err := checkpoint.Decode(h.Checkpoint)
			if err != nil {
				return err
			}
			both = both && cp.BlockNumber >= 3
		}
		if both {
			forked = true
			client := rpc.New(spec.RPC + "/1")
			client.Retries = 0
			client.RequestsPerSecond = 0
			var yes bool
			return client.Call(ctx, "ponder_test_factory_transfer_fork", []any{}, &yes)
		}
		return nil
	}
	r, err = OpenRuntimePostgres(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background())
	t.Fatalf("factory app child did not exit at %s: %v", spec.Phase, err)
}

// This terminates the application, bypassing defers; its owned PG server lives on.
// PGlite and database-engine crash acceptance are deliberately separate work.
func TestRuntimeFactoryTraceCrash(t *testing.T) {
	if path := os.Getenv("PONDER_M45_FACTORY_TRACE_CRASH_SPEC"); path != "" {
		factoryTraceCrashChild(t, path)
		return
	}
	for _, cache := range []bool{true, false} {
		for _, phase := range []string{"prepare", "business", "ack"} {
			t.Run(fmt.Sprintf("postgres/cache_%t/%s", cache, phase), func(t *testing.T) {
				continuousRoot(t)
				g := factoryTraceGoldens(t)
				s, _ := factoryTraceRPC(t, 3, g)
				b := factoryTraceBuild(t, s.p, g, 1, 600, cache, true, factoryTraceHandler(g, true))
				f := newManagedFiniteFixture(t, "postgres", b)
				dir, err := os.MkdirTemp(os.Getenv("PONDER_M45_TEST_ROOT"), "factory-trace-app-crash-")
				if err != nil {
					t.Fatal(err)
				}
				resolved, err := filepath.EvalSymlinks(dir)
				if err != nil || !strings.HasPrefix(resolved, "/root/repo/") {
					t.Fatalf("crash evidence path %s %v", resolved, err)
				}
				marker := filepath.Join(dir, "exit-marker.json")
				spec := factoryTraceCrashSpec{continuousCrashSpec: continuousCrashSpec{RPC: s.p.server.URL, Phase: phase, Marker: marker, Storage: f.options.Owned.Storage}, Cache: cache}
				raw, err := json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "child-spec.json")
				if err = os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRuntimeFactoryTraceCrash$", "-test.v")
				cmd.Env = append(os.Environ(), "PONDER_M45_FACTORY_TRACE_CRASH_SPEC="+path)
				output, err := cmd.CombinedOutput()
				if e := os.WriteFile(filepath.Join(dir, "child.log"), output, 0600); e != nil {
					t.Fatal(e)
				}
				var exited *exec.ExitError
				if !errors.As(err, &exited) || exited.ExitCode() != 83 || ctx.Err() != nil {
					t.Fatalf("factory app exit=%v ctx=%v log=%s", err, ctx.Err(), output)
				}
				raw, err = os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				var proof struct {
					Phase, Operation string
					Exit             int
				}
				if err = json.Unmarshal(raw, &proof); err != nil || proof.Phase != phase || proof.Exit != 83 || proof.Operation == "" {
					t.Fatalf("missing crash boundary: %s %v", raw, err)
				}
				epochs := []uint64{}
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "trace-crash", OpenID: "app-parent", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				r := runtimeOpen(t, "postgres", o)
				c, err := r.Control(context.Background())
				if err != nil || c.Recoveries != 1 || c.Serving || len(c.ReplayTargets) == 0 {
					t.Fatalf("crash recovery=%+v err=%v", c, err)
				}
				out, err := r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("crash replay=%+v err=%v", out, err)
				}
				evidence, err := r.domains.DomainEvidence(context.Background())
				if err != nil || !strings.Contains(string(evidence), proof.Operation) {
					t.Fatalf("crashed intent absent: %s %v", evidence, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				factoryTransferRows(t, f, factoryTraceExpected(g, map[uint64]string{1: "fresh", 2: "old"}, 1))
			})
		}
	}
}

func TestRuntimeFactoryTraceAcquisitionRetry(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			for _, method := range []string{"debug_traceBlockByHash", "eth_getTransactionReceipt"} {
				t.Run(fmt.Sprintf("%s/cache_%t/%s", backend, cache, method), func(t *testing.T) {
					continuousRoot(t)
					g := factoryTraceGoldens(t)
					s, _ := factoryTraceRPC(t, 3, g)
					b := factoryTraceBuild(t, s.p, g, 3, 1, cache, true, factoryTraceHandler(g, true))
					f := newManagedFiniteFixture(t, backend, b)
					epochs := []uint64{}
					armed, fired, retries := false, false, 0
					var r *RuntimeRunner
					s.gate = func(_ context.Context, _ uint64, m string, _ []any) error {
						s.mu.Lock()
						defer s.mu.Unlock()
						if method == "eth_getTransactionReceipt" && m == "eth_getBlockReceipts" {
							return fmt.Errorf("controlled block receipts method unavailable; require source receipt fallback")
						}
						if armed && !fired && m == method {
							fired = true
							return fmt.Errorf("controlled trace acquisition failure")
						}
						return nil
					}
					o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "trace-acquisition-" + method, OpenID: "trace-acquisition-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, RetryInterval: time.Millisecond}}
					o.Live.OnProgress = func(ctx context.Context, e RuntimeLiveProgress) error {
						if e.Type == "ready" {
							s.mu.Lock()
							armed = true
							s.mu.Unlock()
							s.setLatest(4)
						}
						if e.Type == "fetch-retry" {
							retries++
							if yes, err := r.Ready(ctx); err != nil || yes {
								return fmt.Errorf("trace fetch retry exposed ready: %v", err)
							}
						}
						return nil
					}
					r = runtimeOpen(t, backend, o)
					out, err := r.Run(context.Background())
					if err != nil || !out.Completed || !out.Ready {
						t.Fatalf("trace acquisition run=%+v err=%v", out, err)
					}
					if err = r.Close(context.Background()); err != nil {
						t.Fatal(err)
					}
					s.mu.Lock()
					didFail := fired
					s.mu.Unlock()
					if !didFail || retries < 1 {
						t.Fatalf("transfer fault not exercised: fired=%v retries=%d", didFail, retries)
					}
					want := factoryTraceExpected(g, map[uint64]string{1: "old", 2: "old"}, 3)
					factoryTransferRows(t, f, want)
					o.OpenID = "trace-acquisition-reopen"
					o.Live.OnProgress = nil
					r = runtimeOpen(t, backend, o)
					out, err = r.Run(context.Background())
					if err != nil || !out.Completed || !out.Ready || !out.Recovered {
						t.Fatalf("trace acquisition reopen=%+v err=%v", out, err)
					}
					if err = r.Close(context.Background()); err != nil {
						t.Fatal(err)
					}
					factoryTransferRows(t, f, want)
				})
			}
		}
	}
}

func TestRuntimeFactoryTraceDefaultReceipts(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/cache_%t", backend, cache), func(t *testing.T) {
				continuousRoot(t)
				g := factoryTraceGoldens(t)
				s, _ := factoryTraceRPC(t, 3, g, false)
				b := factoryTraceBuild(t, s.p, g, 1, 1, cache, false, factoryTraceHandler(g, false))
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "trace-default-receipts", OpenID: "trace-default-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				o.Live.OnProgress = func(_ context.Context, e RuntimeLiveProgress) error {
					if e.Type == "block" || e.Type == "ready" {
						s.setLatest(4)
					}
					return nil
				}
				r := runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready {
					t.Fatalf("default receipt run=%+v err=%v", out, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				want := factoryTraceExpected(g, map[uint64]string{1: "old", 2: "old"}, 1)
				factoryTransferRows(t, f, want)
				o.OpenID = "trace-default-reopen"
				o.Live.OnProgress = nil
				r = runtimeOpen(t, backend, o)
				out, err = r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("default receipt reopen=%+v err=%v", out, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				factoryTransferRows(t, f, want)
			})
		}
	}
}

// Rejected options must fail before requester ownership or storage DDL.
func TestRuntimeFactoryTraceCapability(t *testing.T) {
	for _, kind := range []string{"opt_out", "account_trace", "non_call", "missing_source_checkpoints", "missing_live"} {
		t.Run(kind, func(t *testing.T) {
			g := factoryTraceGoldens(t)
			s, _ := factoryTraceRPC(t, 3, g)
			b := factoryTraceBuild(t, s.p, g, 1, 1, true, true, factoryTraceHandler(g, true))
			if kind == "account_trace" || kind == "non_call" {
				for _, group := range b.Executable().EventCallbacks {
					for _, cb := range group {
						if kind == "account_trace" {
							cb["type"] = "account"
						} else {
							cb["filter"].(map[string]any)["callType"] = "STATICCALL"
						}
					}
				}
			}
			requested := false
			o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: Options{Build: b}, BuildID: "trace-capability", OpenID: "trace-capability", Live: &RuntimeLiveOptions{}, Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
				requested = true
				return nil, fmt.Errorf("unexpected requester creation")
			}}
			want := "require opt-in log, transaction or transfer"
			switch kind {
			case "opt_out":
				o.FactorySources = false
			case "missing_source_checkpoints":
				o.SourceCheckpoints = false
				want = "require source checkpoints and continuous lifecycle"
			case "missing_live":
				o.Live = nil
				want = "require source checkpoints and continuous lifecycle"
			}
			r, err := OpenRuntimePostgres(context.Background(), o)
			if r != nil || err == nil || !strings.Contains(err.Error(), want) || requested {
				t.Fatalf("unsupported %s escaped preflight: runtime=%v err=%v requested=%v", kind, r, err, requested)
			}
		})
	}
}
