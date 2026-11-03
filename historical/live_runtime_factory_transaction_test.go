package historical

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type factoryTransactionGolden struct {
	SourceCommit                     string `json:"source_commit"`
	Old, Fresh                       []map[string]any
	ReceiptsOld, ReceiptsFresh       [][]map[string]any
	FactoryLogsOld, FactoryLogsFresh [][]map[string]any
	ExpectedTransactions             []struct {
		ChainID uint64
		Branch  string
		Block   uint64
		Events  []struct {
			Name, TransactionHash, Checkpoint string
			TransactionIndex                  uint64
			From                              string
			To                                any
		}
	}
}

func factoryTransactionGoldens(t *testing.T) factoryTransactionGolden {
	t.Helper()
	raw, err := os.ReadFile("testdata/factory-transactions-17c6d5ba.json")
	if err != nil {
		t.Fatal(err)
	}
	var g factoryTransactionGolden
	if err = json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.SourceCommit != "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334" || len(g.ExpectedTransactions) != 14 {
		t.Fatalf("missing actual upstream oracle: %+v", g)
	}
	return g
}

func factoryTransactionRPC(t *testing.T, latest uint64, g factoryTransactionGolden) (*continuousRPC, func(uint64)) {
	t.Helper()
	s := newContinuousRPC(t, latest)
	receipts := map[string][]map[string]any{}
	logs := map[string][]map[string]any{}
	byTransaction := map[string]map[string]any{}
	for i, branch := range [][]map[string]any{g.Old, g.Fresh} {
		rr, ll := g.ReceiptsOld, g.FactoryLogsOld
		if i == 1 {
			rr, ll = g.ReceiptsFresh, g.FactoryLogsFresh
		}
		for n, b := range branch {
			hash := fmt.Sprint(b["hash"])
			s.byHash[hash] = b
			receipts[hash], logs[hash] = rr[n], ll[n]
			for _, r := range rr[n] {
				byTransaction[fmt.Sprint(r["transactionHash"])] = r
			}
			if i == 0 {
				for _, id := range []uint64{1, 2} {
					s.canonical[id][uint64(n)] = b
				}
			}
		}
	}
	fork := func(id uint64) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for n := 2; n < len(g.Fresh); n++ {
			s.canonical[id][uint64(n)] = g.Fresh[n]
		}
	}
	original := s.p.server.Config.Handler
	s.p.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var q struct {
			ID     json.RawMessage
			Method string
			Params []any
		}
		if err = json.Unmarshal(raw, &q); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if q.Method == "ponder_test_factory_transaction_fork" {
			fork(1)
			s.setLatest(4)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": true})
			return
		}
		if q.Method != "eth_getLogs" && q.Method != "eth_getBlockReceipts" && q.Method != "eth_getTransactionReceipt" {
			original.ServeHTTP(w, r)
			return
		}
		id, _ := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/"), 10, 64)
		s.mu.Lock()
		gate := s.gate
		s.mu.Unlock()
		if gate != nil {
			err = gate(r.Context(), id, q.Method, q.Params)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		var out any
		if err == nil {
			switch q.Method {
			case "eth_getBlockReceipts":
				out = receipts[fmt.Sprint(q.Params[0])]
			case "eth_getTransactionReceipt":
				out = byTransaction[fmt.Sprint(q.Params[0])]
			case "eth_getLogs":
				f := q.Params[0].(map[string]any)
				candidates := []map[string]any{}
				if h, ok := f["blockHash"]; ok {
					candidates = append(candidates, logs[fmt.Sprint(h)]...)
				} else {
					from, _ := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(f["fromBlock"]), "0x"), 16, 64)
					to, _ := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(f["toBlock"]), "0x"), 16, 64)
					for n := from; n <= to; n++ {
						if b := s.canonical[id][n]; b != nil {
							candidates = append(candidates, logs[fmt.Sprint(b["hash"])]...)
						}
					}
				}
				matches := func(value any, actual string) bool {
					if value == nil {
						return true
					}
					switch v := value.(type) {
					case string:
						return strings.EqualFold(v, actual)
					case []any:
						for _, x := range v {
							if strings.EqualFold(fmt.Sprint(x), actual) {
								return true
							}
						}
					}
					return false
				}
				selected := []any{}
				for _, l := range candidates {
					if !matches(f["address"], fmt.Sprint(l["address"])) {
						continue
					}
					if topics, ok := f["topics"].([]any); ok && len(topics) > 0 && !matches(topics[0], fmt.Sprint(l["topics"].([]any)[0])) {
						continue
					}
					selected = append(selected, l)
				}
				out = selected
			}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": q.ID}
		if err != nil {
			response["error"] = map[string]any{"code": -32000, "message": err.Error()}
		} else {
			response["result"] = out
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
	return s, fork
}

func factoryTransactionBuild(t *testing.T, p *finiteRPC, start, window uint64, cache bool, h func(string) indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":%d,"cacheRpcRequests":%t},"two":{"id":2,"rpc":%q,"reorgWindow":%d,"cacheRpcRequests":%t}},"accounts":{"Wallet":{"address":{"address":"0x1111111111111111111111111111111111111111","event":{"type":"event","name":"Created","inputs":[{"name":"child","type":"address","indexed":true}]},"parameter":"child","startBlock":1,"endBlock":2},"chain":{"one":{},"two":{}},"startBlock":%d,"endBlock":4}}}`, p.server.URL+"/1", window, cache, p.server.URL+"/2", window, cache, start)
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
	for _, direction := range []string{"to", "from"} {
		functions = append(functions, config.SourceIndexingFunction{Name: "Wallet:transaction:" + direction, Fn: h("transaction:" + direction)})
	}
	b, err := config.BuildSourceIndexing(context.Background(), cfg, base, functions)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func factoryTransactionProperty(a *indexing.HandlerArgs, component, key string) (any, error) {
	if p, ok := a.Event.Property(component); ok {
		return p.Get(key)
	}
	m, ok := a.Event.Get(component).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing %s event projection", component)
	}
	return m[key], nil
}
func factoryTransactionHandler(name string) indexing.Handler {
	return func(ctx context.Context, a *indexing.HandlerArgs) error {
		hash, err := factoryTransactionProperty(a, "transaction", "hash")
		if err != nil {
			return err
		}
		status, err := factoryTransactionProperty(a, "transactionReceipt", "status")
		if err != nil {
			return err
		}
		if status != "success" {
			return fmt.Errorf("reverted transaction reached business handler: %v", status)
		}
		n, err := continuousNumber(a)
		if err != nil {
			return err
		}
		id := fmt.Sprintf("%d:%s:%v", finiteChain(a), name, hash)
		_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": id, "balance": big.NewInt(int64(n))}, store.Conflict{})
		return err
	}
}
func factoryTransactionExpected(g factoryTransactionGolden, branches map[uint64]string, start uint64) map[string]string {
	expected := map[string]string{}
	for _, x := range g.ExpectedTransactions {
		if x.Branch != branches[x.ChainID] || x.Block < start {
			continue
		}
		for _, event := range x.Events {
			expected[fmt.Sprintf("%d:%s:%s", x.ChainID, event.Name, event.TransactionHash)] = fmt.Sprint(x.Block)
		}
	}
	return expected
}
func factoryTransactionRows(t *testing.T, f finiteFixture, want map[string]string) {
	t.Helper()
	managedInspect(t, f, func(db store.Backend) {
		rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
		got := map[string]string{}
		for _, r := range rows {
			got[fmt.Sprint(r["id"])] = fmt.Sprint(r["balance"])
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("business rows differ from actual pinned factory transaction oracle: got=%v want=%v", got, want)
		}
	})
}

// Every handler projection is checked against actual pinned-source output,
// including its complete checkpoint and original, possibly sparse tx index.
func factoryTransactionOracleHandler(g factoryTransactionGolden) func(string) indexing.Handler {
	return func(name string) indexing.Handler {
		write := factoryTransactionHandler(name)
		return func(ctx context.Context, a *indexing.HandlerArgs) error {
			hash, err := factoryTransactionProperty(a, "transaction", "hash")
			if err != nil {
				return err
			}
			for _, x := range g.ExpectedTransactions {
				if x.ChainID != finiteChain(a) {
					continue
				}
				for _, event := range x.Events {
					if event.Name != name || event.TransactionHash != hash {
						continue
					}
					if a.Event.Get("id") != event.Checkpoint {
						return fmt.Errorf("factory checkpoint differs: %v want %s", a.Event.Get("id"), event.Checkpoint)
					}
					for key, want := range map[string]any{"transactionIndex": event.TransactionIndex, "from": event.From, "to": event.To} {
						got, err := factoryTransactionProperty(a, "transaction", key)
						if err != nil {
							return err
						}
						if fmt.Sprint(got) != fmt.Sprint(want) {
							return fmt.Errorf("transaction %s=%v want %v", key, got, want)
						}
					}
					return write(ctx, a)
				}
			}
			return fmt.Errorf("transaction absent from pinned oracle: %d %s %v", finiteChain(a), name, hash)
		}
	}
}

func TestRuntimeFactoryTransactionLifecycle(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			for _, start := range []uint64{1, 3} {
				t.Run(fmt.Sprintf("%s/cache_%t/start_%d", backend, cache, start), func(t *testing.T) {
					continuousRoot(t)
					g := factoryTransactionGoldens(t)
					s, _ := factoryTransactionRPC(t, 3, g)
					b := factoryTransactionBuild(t, s.p, start, 1, cache, factoryTransactionOracleHandler(g))
					f := newManagedFiniteFixture(t, backend, b)
					epochs := []uint64{}
					o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "factory-transaction-lifecycle", OpenID: "transaction-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
					o.Live.OnProgress = func(_ context.Context, p RuntimeLiveProgress) error {
						if p.Type == "block" || p.Type == "ready" {
							s.setLatest(4)
						}
						return nil
					}
					r := runtimeOpen(t, backend, o)
					result, err := r.Run(context.Background())
					if err != nil || !result.Completed || !result.Ready {
						t.Fatalf("factory transaction run=%+v err=%v", result, err)
					}
					if err = r.Close(context.Background()); err != nil {
						t.Fatal(err)
					}
					want := factoryTransactionExpected(g, map[uint64]string{1: "old", 2: "old"}, start)
					factoryTransactionRows(t, f, want)
					o.OpenID = "transaction-reopen"
					o.Live.OnProgress = nil
					r = runtimeOpen(t, backend, o)
					result, err = r.Run(context.Background())
					if err != nil || !result.Completed || !result.Ready || !result.Recovered {
						t.Fatalf("factory transaction reopen=%+v err=%v", result, err)
					}
					if err = r.Close(context.Background()); err != nil {
						t.Fatal(err)
					}
					factoryTransactionRows(t, f, want)
				})
			}
		}
	}
}

func TestRuntimeFactoryTransactionReorg(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/cache_%t", backend, cache), func(t *testing.T) {
				continuousRoot(t)
				g := factoryTransactionGoldens(t)
				s, fork := factoryTransactionRPC(t, 3, g)
				b := factoryTransactionBuild(t, s.p, 1, 600, cache, factoryTransactionOracleHandler(g))
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				var r *RuntimeRunner
				forked, reorg := false, 0
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "transaction-reorg", OpenID: "old-fork", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
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
					t.Fatalf("transaction reorg=%+v forked=%v reorg=%d err=%v", out, forked, reorg, err)
				}
				c, err := r.Control(context.Background())
				if err != nil || len(c.ReplayTargets) != 0 || !c.Serving {
					t.Fatalf("replay not drained: %+v %v", c, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				want := factoryTransactionExpected(g, map[uint64]string{1: "fresh", 2: "old"}, 1)
				factoryTransactionRows(t, f, want)
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
				factoryTransactionRows(t, f, want)
			})
		}
	}
}

func TestRuntimeFactoryTransactionFailedCommit(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/cache_%t", backend, cache), func(t *testing.T) {
				continuousRoot(t)
				g := factoryTransactionGoldens(t)
				s, _ := factoryTransactionRPC(t, 4, g)
				failed := false
				retry := false
				oracle := factoryTransactionOracleHandler(g)
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
							return fmt.Errorf("controlled transaction handler failure after SQL write")
						}
						return nil
					}
				}
				b := factoryTransactionBuild(t, s.p, 1, 600, cache, h)
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "transaction-failure", OpenID: "failed-write", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				r := runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if err == nil || out.Completed || out.Ready || !failed {
					t.Fatalf("transaction failure accepted=%+v fired=%v err=%v", out, failed, err)
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
						t.Fatalf("failed transaction leaked SQL rows: %v", rows)
					}
				})
				retry = true
				o.OpenID = "retry-write"
				r = runtimeOpen(t, backend, o)
				out, err = r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("failed transaction reopen=%+v err=%v", out, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				factoryTransactionRows(t, f, factoryTransactionExpected(g, map[uint64]string{1: "old", 2: "old"}, 1))
			})
		}
	}
}

type factoryTransactionCrashSpec struct {
	continuousCrashSpec
	Cache bool
}

func factoryTransactionCrashChild(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec factoryTransactionCrashSpec
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	g := factoryTransactionGoldens(t)
	p := &finiteRPC{server: &httptest.Server{URL: spec.RPC}}
	b := factoryTransactionBuild(t, p, 1, 600, spec.Cache, factoryTransactionOracleHandler(g))
	epochs := []uint64{}
	var r *RuntimeRunner
	forked := false
	o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: spec.Storage, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}, BuildID: "transaction-crash", OpenID: "app-child", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
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
			return client.Call(ctx, "ponder_test_factory_transaction_fork", []any{}, &yes)
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
func TestRuntimeFactoryTransactionCrash(t *testing.T) {
	if path := os.Getenv("PONDER_M45_FACTORY_TRANSACTION_CRASH_SPEC"); path != "" {
		factoryTransactionCrashChild(t, path)
		return
	}
	for _, cache := range []bool{true, false} {
		for _, phase := range []string{"prepare", "business", "ack"} {
			t.Run(fmt.Sprintf("postgres/cache_%t/%s", cache, phase), func(t *testing.T) {
				continuousRoot(t)
				g := factoryTransactionGoldens(t)
				s, _ := factoryTransactionRPC(t, 3, g)
				b := factoryTransactionBuild(t, s.p, 1, 600, cache, factoryTransactionOracleHandler(g))
				f := newManagedFiniteFixture(t, "postgres", b)
				dir, err := os.MkdirTemp(os.Getenv("PONDER_M45_TEST_ROOT"), "factory-transaction-app-crash-")
				if err != nil {
					t.Fatal(err)
				}
				resolved, err := filepath.EvalSymlinks(dir)
				if err != nil || !strings.HasPrefix(resolved, "/root/repo/") {
					t.Fatalf("crash evidence path %s %v", resolved, err)
				}
				marker := filepath.Join(dir, "exit-marker.json")
				spec := factoryTransactionCrashSpec{continuousCrashSpec: continuousCrashSpec{RPC: s.p.server.URL, Phase: phase, Marker: marker, Storage: f.options.Owned.Storage}, Cache: cache}
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
				cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRuntimeFactoryTransactionCrash$", "-test.v")
				cmd.Env = append(os.Environ(), "PONDER_M45_FACTORY_TRANSACTION_CRASH_SPEC="+path)
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
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "transaction-crash", OpenID: "app-parent", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
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
				factoryTransactionRows(t, f, factoryTransactionExpected(g, map[uint64]string{1: "fresh", 2: "old"}, 1))
			})
		}
	}
}

// Rejection must precede requester creation and storage DDL.
func TestRuntimeFactoryTransactionCapability(t *testing.T) {
	for _, kind := range []string{"opt_out", "trace", "missing_source_checkpoints", "missing_live"} {
		t.Run(kind, func(t *testing.T) {
			g := factoryTransactionGoldens(t)
			s, _ := factoryTransactionRPC(t, 3, g)
			b := factoryTransactionBuild(t, s.p, 1, 1, true, factoryTransactionOracleHandler(g))
			if kind == "trace" {
				for _, group := range b.Executable().EventCallbacks {
					for _, cb := range group {
						cb["filter"].(map[string]any)["type"] = kind
					}
				}
			}
			requested := false
			o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: Options{Build: b}, BuildID: "capability", OpenID: "capability", Live: &RuntimeLiveOptions{}, Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
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
