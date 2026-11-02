package historical

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
	"io"
	"net/http"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strconv"
	"strings"
	"testing"
	"time"
)

const factoryParent = "0x1111111111111111111111111111111111111111"
const factoryChild = "0x2222222222222222222222222222222222222222"
const factoryOther = "0x3333333333333333333333333333333333333333"

var factoryCreated = crypto.Keccak256Hash([]byte("Created(address)")).Hex()
var factoryPing = crypto.Keccak256Hash([]byte("Ping(uint256)")).Hex()

// A real HTTP provider supplies competing hash-linked branches, transactions
// and exact log query semantics. Production source filtering/decoding is used.
func factoryRPC(t *testing.T, latest, birth uint64, offset bool) (*continuousRPC, func(uint64, uint64)) {
	t.Helper()
	s := newContinuousRPC(t, latest)
	enrich := func(b map[string]any) {
		hash := fmt.Sprint(b["hash"])
		tx := crypto.Keccak256Hash([]byte(hash + "tx")).Hex()
		b["logsBloom"] = "0x" + strings.Repeat("f", 512)
		b["transactions"] = []any{map[string]any{"hash": tx, "blockHash": hash, "blockNumber": b["number"], "transactionIndex": "0x0", "from": factoryParent, "to": factoryChild, "gas": "0x5208", "gasPrice": "0x1", "value": "0x0", "nonce": "0x0", "type": "0x0", "input": "0x", "r": "0x1", "s": "0x1", "v": "0x1b"}}
	}
	for _, bs := range s.canonical {
		for _, b := range bs {
			enrich(b)
		}
	}
	original := s.p.server.Config.Handler
	s.p.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var q struct {
			ID     json.RawMessage
			Method string
			Params []any
		}
		if e := json.Unmarshal(raw, &q); e != nil {
			t.Error(e)
			return
		}
		if q.Method != "eth_getLogs" {
			original.ServeHTTP(w, r)
			return
		}
		id, _ := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/"), 10, 64)
		s.mu.Lock()
		gate := s.gate
		s.mu.Unlock()
		if gate != nil {
			if e := gate(r.Context(), id, q.Method, q.Params); e != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32000, "message": e.Error()}})
				return
			}
		}
		f := q.Params[0].(map[string]any)
		s.mu.Lock()
		defer s.mu.Unlock()
		from, to := uint64(0), uint64(0)
		blocks := []map[string]any{}
		if h, ok := f["blockHash"]; ok {
			if b := s.byHash[fmt.Sprint(h)]; b != nil {
				blocks = append(blocks, b)
			}
		} else {
			from, _ = strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(f["fromBlock"]), "0x"), 16, 64)
			to, _ = strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(f["toBlock"]), "0x"), 16, 64)
			for n := from; n <= to; n++ {
				if b := s.canonical[id][n]; b != nil {
					blocks = append(blocks, b)
				}
			}
		}
		match := func(v any, want string) bool {
			if v == nil {
				return true
			}
			switch x := v.(type) {
			case string:
				return x == want
			case []any:
				for _, a := range x {
					if a == want {
						return true
					}
				}
			}
			return false
		}
		out := []any{}
		for _, b := range blocks {
			n, _ := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(b["number"]), "0x"), 16, 64)
			child := factoryChild
			born := birth
			if s.forked[fmt.Sprint(b["hash"])] {
				child = factoryOther
				born = 3
			}
			tx := b["transactions"].([]any)[0].(map[string]any)["hash"]
			mk := func(addr string, index int, topics []any, data string) map[string]any {
				return map[string]any{"address": addr, "blockHash": b["hash"], "blockNumber": b["number"], "transactionHash": tx, "transactionIndex": "0x0", "logIndex": fmt.Sprintf("0x%x", index), "removed": false, "topics": topics, "data": data}
			}
			logs := []map[string]any{mk(factoryChild, 0, []any{factoryPing}, fmt.Sprintf("0x%064x", n)), mk(factoryOther, 1, []any{factoryPing}, fmt.Sprintf("0x%064x", n))}
			if n == born {
				topics := []any{factoryCreated, "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(child, "0x")}
				data := "0x"
				if offset {
					topics = []any{factoryCreated}
					data = "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(child, "0x")
				}
				logs = append(logs, mk(factoryParent, 2, topics, data))
			}
			for _, l := range logs {
				topics, ok := f["topics"].([]any)
				if !match(f["address"], fmt.Sprint(l["address"])) || (ok && len(topics) > 0 && !match(topics[0], fmt.Sprint(l["topics"].([]any)[0]))) {
					continue
				}
				out = append(out, l)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": out})
	})
	fork := func(id, from uint64) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.forkLocked(id, from)
		for n := from; n <= 32; n++ {
			enrich(s.canonical[id][n])
		}
	}
	return s, fork
}

func factoryBuild(t *testing.T, p *finiteRPC, start, end, window uint64, cache, offset bool, h indexing.Handler, overlap ...bool) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":%d,"cacheRpcRequests":%t},"two":{"id":2,"rpc":%q,"reorgWindow":%d,"cacheRpcRequests":%t}},"contracts":{"C":{"abi":[{"type":"event","name":"Ping","inputs":[{"name":"value","type":"uint256","indexed":false}]}],"address":{"address":%q,"event":{"type":"event","name":"Created","inputs":[{"name":"child","type":"address","indexed":%t}]},"parameter":"child","startBlock":1,"endBlock":3},"chain":{"one":{},"two":{}},"startBlock":%d,"endBlock":%d}}}`, p.server.URL+"/1", window, cache, p.server.URL+"/2", window, cache, factoryParent, !offset, start, end)
	funcs := []config.SourceIndexingFunction{{Name: "C:Ping", Fn: h}}
	if len(overlap) > 0 && overlap[0] {
		var parsed map[string]any
		if e := json.Unmarshal([]byte(raw), &parsed); e != nil {
			t.Fatal(e)
		}
		contracts := parsed["contracts"].(map[string]any)
		copyBytes, _ := json.Marshal(contracts["C"])
		var other map[string]any
		_ = json.Unmarshal(copyBytes, &other)
		other["address"].(map[string]any)["address"] = []any{factoryParent, "0x4444444444444444444444444444444444444444"}
		contracts["Other"] = other
		enc, _ := json.Marshal(parsed)
		raw = string(enc)
		funcs = append(funcs, config.SourceIndexingFunction{Name: "Other:Ping", Fn: h})
	}
	cfg, e := config.ParseSourceConfiguration([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	base, e := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range base.RPCClients() {
		r.RequestsPerSecond = 0
		r.Retries = 0
	}
	b, e := config.BuildSourceIndexing(context.Background(), cfg, base, funcs)
	if e != nil {
		t.Fatal(e)
	}
	for _, cbs := range b.Executable().EventCallbacks {
		for _, cb := range cbs {
			f := cb["filter"].(map[string]any)
			fs := filter.GetFilterFactories(f)
			if len(fs) != 1 || filter.FilterFromBlock(f) != 1 || fmt.Sprint(fs[0]["fromBlock"]) != "1" || fmt.Sprint(fs[0]["toBlock"]) != "3" {
				t.Fatalf("compiled source factory bounds differ: %v", f)
			}
		}
	}
	return b
}

func TestRuntimeFactoryLifecycle(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, tc := range []struct {
			name          string
			start, birth  uint64
			cache, offset bool
		}{
			{"early_birth_cached", 3, 1, true, false}, {"early_birth_uncached", 3, 1, false, true}, {"same_block_cached", 1, 2, true, true}, {"same_block_uncached", 1, 2, false, false},
		} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				continuousRoot(t)
				s, _ := factoryRPC(t, 3, tc.birth, tc.offset)
				calls := map[uint64]int{}
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					calls[finiteChain(a)]++
					return finiteIncrement(ctx, a)
				})
				b := factoryBuild(t, s.p, tc.start, 4, 1, tc.cache, tc.offset, h)
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "factory-" + tc.name, OpenID: "factory-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				// Move through historical -> live without finalizing all future work.
				o.Live.OnProgress = func(ctx context.Context, e RuntimeLiveProgress) error {
					if e.Type == "block" || e.Type == "ready" {
						s.setLatest(4)
					}
					return nil
				}
				r := runtimeOpen(t, backend, o)
				out, e := r.Run(context.Background())
				if e != nil || !out.Completed || !out.Ready {
					t.Fatalf("factory runtime=%+v err=%v", out, e)
				}
				if c, e := r.Control(context.Background()); e != nil || c.Version != "managed-live-factory-v10" {
					t.Fatalf("factory identity %+v %v", c, e)
				}
				want := int(5 - max(tc.start, tc.birth))
				for _, id := range []uint64{1, 2} {
					if calls[id] != want {
						t.Fatalf("child birth/same-block chain%d calls%d want%d", id, calls[id], want)
					}
				}
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				o.OpenID = "factory-reopen"
				o.Live.OnProgress = nil
				r = runtimeOpen(t, backend, o)
				out, e = r.Run(context.Background())
				if e != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("factory reopen=%+v err=%v", out, e)
				}
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
					if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != fmt.Sprint(want) || fmt.Sprint(rows[1]["balance"]) != fmt.Sprint(want) {
						t.Fatalf("factory durable replay duplicated/lost effects: %v", rows)
					}
				})
			})
		}
	}
}

func TestRuntimeFactoryReorg(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cache := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/cache_%t", backend, cache), func(t *testing.T) {
				continuousRoot(t)
				s, fork := factoryRPC(t, 3, 2, false)
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					row, e := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
					if e != nil {
						return e
					}
					v := 0
					if row != nil {
						v, _ = strconv.Atoi(fmt.Sprint(row["balance"]))
					}
					if row == nil {
						_, e = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "balance": fmt.Sprint(v + 1)}, store.Conflict{})
					} else {
						_, e = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"balance": fmt.Sprint(v + 1)})
					}
					return e
				})
				b := factoryBuild(t, s.p, 1, 4, 100, cache, false, h)
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				var r *RuntimeRunner
				step, reorg := 0, 0
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: fmt.Sprintf("factory-reorg-%t", cache), OpenID: "factory-old-fork", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				o.Live.OnProgress = func(ctx context.Context, e RuntimeLiveProgress) error {
					if e.Type == "block" && step == 0 {
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						both := true
						for _, h := range c.Cursors {
							cp, _ := checkpoint.Decode(h.Checkpoint)
							both = both && cp.BlockNumber >= 3
						}
						if both {
							step = 1
							fork(1, 2)
						}
					}
					if e.Type == "reorg" {
						reorg++
						if ready, err := r.Ready(ctx); err != nil || ready {
							return fmt.Errorf("factory reorg ready prematurely")
						}
					}
					if e.Type == "ready" && step == 1 && reorg == 1 {
						step = 2
						s.setLatest(4)
					}
					return nil
				}
				r = runtimeOpen(t, backend, o)
				out, e := r.Run(context.Background())
				if e != nil || !out.Completed || !out.Ready || step != 2 || reorg != 1 {
					t.Fatalf("factory reorg %+v step%d reorg%d %v", out, step, reorg, e)
				}
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
					if len(rows) != 1 || fmt.Sprint(rows[0]["balance"]) != "5" {
						t.Fatalf("orphan factory/global suffix survived: %v", rows)
					}
				})
			})
		}
	}
}

func TestRuntimeFactoryAcquisitionRetry(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, method := range []string{"eth_getBlockByHash", "eth_getLogs"} {
			t.Run(backend+"/"+method, func(t *testing.T) {
				continuousRoot(t)
				s, _ := factoryRPC(t, 3, 1, false)
				b := factoryBuild(t, s.p, 3, 4, 1, true, false, indexing.Handler(finiteIncrement))
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				armed, fired, retries := false, false, 0
				var r *RuntimeRunner
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "factory-retry-" + method, OpenID: "factory-retry", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, RetryInterval: time.Millisecond}}
				s.gate = func(_ context.Context, _ uint64, m string, _ []any) error {
					s.mu.Lock()
					defer s.mu.Unlock()
					if armed && !fired && m == method {
						fired = true
						return fmt.Errorf("controlled factory prefix acquisition failure")
					}
					return nil
				}
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
							return fmt.Errorf("factory retry exposed ready")
						}
					}
					return nil
				}
				r = runtimeOpen(t, backend, o)
				out, e := r.Run(context.Background())
				if e != nil || !out.Completed || !out.Ready || !fired || retries < 1 {
					t.Fatalf("factory retry=%+v fired%v retries%d err=%v", out, fired, retries, e)
				}
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
					if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "2" || fmt.Sprint(rows[1]["balance"]) != "2" {
						t.Fatalf("retry factory writes=%v", rows)
					}
				})
			})
		}
	}
}

func TestRuntimeFactoryFailedCommitReopen(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s, _ := factoryRPC(t, 4, 1, false)
			fail := true
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				if e := finiteIncrement(ctx, a); e != nil {
					return e
				}
				if fail && finiteChain(a) == 2 {
					n, e := continuousNumber(a)
					if e != nil {
						return e
					}
					if n == 3 {
						return fmt.Errorf("controlled factory handler failure after SQL write")
					}
				}
				return nil
			})
			b := factoryBuild(t, s.p, 3, 4, 1, true, false, h)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "factory-business-failure", OpenID: "factory-failed", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
			r := runtimeOpen(t, backend, o)
			out, e := r.Run(context.Background())
			if e == nil || out.Completed || out.Ready {
				t.Fatalf("factory failed handler accepted=%+v %v", out, e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			fail = false
			o.OpenID = "factory-business-retry"
			r = runtimeOpen(t, backend, o)
			out, e = r.Run(context.Background())
			if e != nil || !out.Completed || !out.Ready || !out.Recovered {
				t.Fatalf("factory failed business reopen=%+v %v", out, e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "2" || fmt.Sprint(rows[1]["balance"]) != "2" {
					t.Fatalf("partial SQL write or replay duplicated=%v", rows)
				}
			})
		})
	}
}

func TestRuntimeFactoryOverlappingParents(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s, _ := factoryRPC(t, 3, 1, false)
			b := factoryBuild(t, s.p, 3, 4, 1, true, false, indexing.Handler(finiteIncrement), true)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "factory-shared-fragment", OpenID: "shared-fragment-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
			o.Live.OnProgress = func(_ context.Context, e RuntimeLiveProgress) error {
				if e.Type == "ready" {
					s.setLatest(4)
				}
				return nil
			}
			r := runtimeOpen(t, backend, o)
			out, e := r.Run(context.Background())
			if e != nil || !out.Completed || !out.Ready {
				t.Fatalf("overlap runtime %+v %v", out, e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			check := func() {
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
					if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "4" || fmt.Sprint(rows[1]["balance"]) != "4" {
						t.Fatalf("shared factory fragment lost source callbacks=%v", rows)
					}
				})
			}
			check()
			o.OpenID = "shared-fragment-reopen"
			o.Live.OnProgress = nil
			r = runtimeOpen(t, backend, o)
			out, e = r.Run(context.Background())
			if e != nil || !out.Completed || !out.Ready || !out.Recovered {
				t.Fatalf("overlap reopen %+v %v", out, e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			check()
		})
	}
}

func TestRuntimeFactoryForgedRecoveryRoot(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"root", "down_parent"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				continuousRoot(t)
				s, _ := factoryRPC(t, 3, 1, false)
				calls := 0
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error { calls++; return finiteIncrement(ctx, a) })
				b := factoryBuild(t, s.p, 3, 4, 1, true, false, h)
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				var r *RuntimeRunner
				stopped := false
				o := RuntimeOptions{FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "factory-forged-" + kind, OpenID: "factory-bootstrap-down", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
				s.gate = func(ctx context.Context, _ uint64, m string, p []any) error {
					if r == nil || stopped || m != "eth_getBlockByNumber" || fmt.Sprint(p[0]) != "0x1" {
						return nil
					}
					c, e := r.Control(ctx)
					if e != nil {
						return e
					}
					if c.Batches == 1 {
						stopped = true
						return fmt.Errorf("controlled interruption after Down bootstrap")
					}
					return nil
				}
				r = runtimeOpen(t, backend, o)
				out, e := r.Run(context.Background())
				if e == nil || !stopped || calls != 0 || out.Completed {
					t.Fatalf("bootstrap boundary %+v stopped%v calls%d %v", out, stopped, calls, e)
				}
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				s.gate = nil
				o.OpenID = "factory-forged-reopen"
				r = runtimeOpen(t, backend, o)
				before, e := r.Control(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				cp, e := checkpoint.Decode(before.Cursors[1].Checkpoint)
				if e != nil || cp.BlockNumber != 1 || store.ManagedLiveReachedBlock(before, 1, 1) {
					t.Fatalf("missing restored Down root %+v %v", before, e)
				}
				published, e := r.PublicCheckpoints(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				n := uint64(1)
				if kind == "down_parent" {
					n = 0
				}
				s.mu.Lock()
				expected := fmt.Sprint(s.canonical[1][n]["hash"])
				forged := map[string]any{}
				for k, v := range s.canonical[1][n] {
					forged[k] = v
				}
				forged["hash"] = fmt.Sprintf("0x%064x", 987654)
				s.byHash[expected] = forged
				s.mu.Unlock()
				out, e = r.Run(context.Background())
				if e == nil || calls != 0 || out.Ready || out.Completed {
					t.Fatalf("forged factory root accepted %+v calls%d %v", out, calls, e)
				}
				after, e := r.Control(context.Background())
				if e != nil || after.Tail != before.Tail || after.LastOperation != before.LastOperation {
					t.Fatalf("forged prefix persisted business %+v %v", after, e)
				}
				actual, e := r.PublicCheckpoints(context.Background())
				if e != nil || fmt.Sprint(actual) != fmt.Sprint(published) {
					t.Fatalf("forged prefix moved public checkpoint %v %v", actual, e)
				}
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
					if len(rows) != 0 {
						t.Fatalf("forged prefix executed handler %v", rows)
					}
				})
			})
		}
	}
}
