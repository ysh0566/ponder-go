package historical

// These M43 integration tests are newly reconstructed after the original local
// WIP volume became unavailable. They are not recovered original test bytes and
// add no upstream-original credit. All events pass through the real source build,
// RPC acquisition, SQL extraction, decoder, owned service and durable group path.
import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type finiteRPCRequest struct {
	Chain         uint64
	Method        string
	Params        []any
	DuringHandler bool
}
type finiteRPC struct {
	server        *httptest.Server
	end           uint64
	mu            sync.Mutex
	calls         []finiteRPCRequest
	hook          func(context.Context, finiteRPCRequest) error
	handlerActive atomic.Bool
	inflight      atomic.Int64
	finished      chan struct{}
}

func newFiniteRPC(t *testing.T, end uint64) *finiteRPC {
	t.Helper()
	p := &finiteRPC{end: end, finished: make(chan struct{}, 1)}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.inflight.Add(1)
		defer func() {
			p.inflight.Add(-1)
			select {
			case p.finished <- struct{}{}:
			default:
			}
		}()
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  []any           `json:"params"`
		}
		if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		chain, e := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/"), 10, 64)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		call := finiteRPCRequest{chain, request.Method, request.Params, p.handlerActive.Load()}
		p.mu.Lock()
		p.calls = append(p.calls, call)
		hook := p.hook
		p.mu.Unlock()
		var result any
		if hook != nil {
			e = hook(r.Context(), call)
		}
		if e == nil {
			switch request.Method {
			case "eth_getBlockByNumber":
				number := end
				if request.Params[0] != "latest" {
					number, e = strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(request.Params[0]), "0x"), 16, 64)
				}
				if e == nil {
					result = finiteBlock(chain, number)
				}
			case "eth_getBalance":
				result = "0x2a"
			case "eth_call":
				result = "0x" + strings.Repeat("0", 62) + "2a"
			case "eth_getLogs":
				result = []any{}
			default:
				e = fmt.Errorf("unexpected RPC %s", request.Method)
			}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		if e != nil {
			response["error"] = map[string]any{"code": -32000, "message": e.Error()}
		} else {
			response["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(p.server.Close)
	return p
}
func finiteBlock(chain, n uint64) map[string]any {
	hash := func(v uint64) string { return fmt.Sprintf("0x%064x", chain*1000000+v) }
	parent := uint64(0)
	if n > 0 {
		parent = n - 1
	}
	return map[string]any{"number": fmt.Sprintf("0x%x", n), "timestamp": fmt.Sprintf("0x%x", 1000+n), "hash": hash(n), "parentHash": hash(parent), "logsBloom": "0x" + strings.Repeat("0", 512), "transactions": []any{}, "miner": "0x" + strings.Repeat("1", 40), "gasUsed": "0x2a", "gasLimit": "0x100000", "baseFeePerGas": "0x1", "nonce": "0x0000000000000000", "mixHash": hash(0), "stateRoot": hash(0), "transactionsRoot": hash(0), "receiptsRoot": hash(0), "sha3Uncles": hash(0), "size": "0x100", "difficulty": "0x0", "totalDifficulty": "0x0", "extraData": "0x"}
}
func (p *finiteRPC) snapshot() []finiteRPCRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]finiteRPCRequest(nil), p.calls...)
}
func (p *finiteRPC) setHook(h func(context.Context, finiteRPCRequest) error) {
	p.mu.Lock()
	p.hook = h
	p.mu.Unlock()
}
func finiteBuild(t *testing.T, p *finiteRPC, end uint64, cache bool, handler, setup indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":0,"cacheRpcRequests":%t},"two":{"id":2,"rpc":%q,"reorgWindow":0,"cacheRpcRequests":%t}},"contracts":{"Setup":{"abi":[],"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":%d}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":%d,"interval":1}}}`, p.server.URL+"/1", cache, p.server.URL+"/2", cache, end, end)
	cfg, e := config.ParseSourceConfiguration([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	base, e := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 16})
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range base.RPCClients() {
		r.RequestsPerSecond = 0
		r.Retries = 0
	}
	funcs := []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: handler}}
	if setup != nil {
		funcs = append(funcs, config.SourceIndexingFunction{Name: "Setup:setup", Fn: setup})
	}
	build, e := config.BuildSourceIndexing(context.Background(), cfg, base, funcs)
	if e != nil {
		t.Fatal(e)
	}
	return build
}

type finiteFixture struct {
	backend string
	options Options
	open    func(context.Context, Options) (*Runner, error)
}

func newFiniteFixture(t *testing.T, backend string, build *config.SourceIndexingBuild) finiteFixture {
	t.Helper()
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	options := Options{Build: build, QuerySize: 2, Clock: func() time.Time { return time.Unix(2000, 0) }, Owned: indexing.OwnedOptions{Storage: store.IndependentStorageOptions{BusinessNamespace: "ponder_go_m43_b_" + id, SyncNamespace: "ponder_go_m43_s_" + id, Schema: schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}}, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}
	f := finiteFixture{backend: backend, options: options, open: OpenPGlite}
	if backend == "postgres" {
		f.open = OpenPostgres
		f.options.Owned.Storage.PostgresURL = os.Getenv("PONDER_TEST_POSTGRES")
		if f.options.Owned.Storage.PostgresURL == "" {
			t.Fatal("explicit fresh task PostgreSQL required; no skip")
		}
		var names []string
		for _, n := range []string{options.Owned.Storage.BusinessNamespace, options.Owned.Storage.SyncNamespace} {
			for _, s := range []string{"_rpc_1", "_legacy", "_sync", ""} {
				names = append(names, n+s)
			}
		}
		b, e := store.OpenPostgres(context.Background(), f.options.Owned.Storage.PostgresURL)
		if e != nil {
			t.Fatal(e)
		}
		for _, n := range names {
			rows, e := b.Query(context.Background(), "SELECT nspname FROM pg_namespace WHERE nspname=$1", n)
			if e != nil {
				_ = b.Close()
				t.Fatal(e)
			}
			if len(rows.Rows) != 0 {
				_ = b.Close()
				t.Fatalf("existing namespace %s; no cleanup registered", n)
			}
		}
		if e = b.Close(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			b, e := store.OpenPostgres(context.Background(), f.options.Owned.Storage.PostgresURL)
			if e != nil {
				t.Error(e)
				return
			}
			defer b.Close()
			for _, n := range names {
				if e = b.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(n)+" CASCADE"); e != nil {
					t.Error(e)
				}
			}
		})
	} else {
		root := t.TempDir()
		f.options.Owned.Storage.BusinessDirectory = filepath.Join(root, "business")
		f.options.Owned.Storage.SyncDirectory = filepath.Join(root, "sync")
	}
	return f
}

// Default fixture cleanup is strict, including after ordinary Run cancellation.
// Only a caller retaining the per-runner scope can record an explicit fault.
func (f finiteFixture) start(t *testing.T) *Runner {
	t.Helper()
	r, _ := f.startWithCloseScope(t)
	return r
}
func (f finiteFixture) startWithCloseScope(t *testing.T) (*Runner, *finiteCloseScope) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, e := f.open(ctx, f.options)
	cancel()
	if e != nil {
		t.Fatal(e)
	}
	closeScope := &finiteCloseScope{runner: r, postgres: f.backend == "postgres"}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := r.Close(ctx); !closeScope.accepts(r, e) {
			t.Error(e)
		}
	})
	return r, closeScope
}
func finiteChain(args *indexing.HandlerArgs) uint64 {
	n, e := uintQuantity(args.Context.Chain["id"])
	if e != nil {
		panic(e)
	}
	return n
}
func finiteNumber(args *indexing.HandlerArgs) (uint64, error) {
	p, ok := args.Event.Property("block")
	if !ok {
		return 0, fmt.Errorf("missing actual block view")
	}
	v, e := p.Get("number")
	if e != nil {
		return 0, e
	}
	return uintQuantity(v)
}
func finiteIncrement(ctx context.Context, a *indexing.HandlerArgs) error {
	key := store.Row{"id": fmt.Sprintf("chain%d", finiteChain(a))}
	row, e := a.Context.DB.Find(ctx, "account", key)
	if e != nil {
		return e
	}
	if row == nil {
		_, e = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": key["id"], "balance": big.NewInt(1)}, store.Conflict{})
		return e
	}
	n, ok := new(big.Int).SetString(fmt.Sprint(row["balance"]), 10)
	if !ok {
		return fmt.Errorf("invalid actual balance")
	}
	n.Add(n, big.NewInt(1))
	_, e = a.Context.DB.Update(ctx, "account", key, store.Row{"balance": n})
	return e
}
func finiteBalances(t *testing.T, r *Runner) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := r.Owner().Database().WithReadOnly(context.Background(), 1, func(ctx context.Context) error {
		rows, e := r.Owner().Database().Select(ctx, "account", nil)
		if e != nil {
			return e
		}
		for _, row := range rows {
			out[fmt.Sprint(row["id"])] = fmt.Sprint(row["balance"])
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func finiteAssertEnds(t *testing.T, r *Runner, end uint64) {
	t.Helper()
	s, e := r.Owner().HistoricalSnapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !s.Initialized || len(s.Chains) != 2 {
		t.Fatal(s)
	}
	for _, c := range s.Chains {
		cp, e := checkpoint.Decode(c.Cursor)
		if e != nil || cp.BlockNumber != end || c.Cursor != c.LatestCheckpoint || c.Cursor != c.SafeCheckpoint || c.Cursor != c.FinalizedCheckpoint {
			t.Fatal(c, e)
		}
	}
}

var finiteStop = errors.New("controlled outside-transaction stop")

func TestFiniteHistoricalRunner(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			p := newFiniteRPC(t, 8)
			setups := []uint64{}
			seen := map[uint64][]uint64{}
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				n, e := finiteNumber(a)
				if e != nil {
					return e
				}
				id := finiteChain(a)
				seen[id] = append(seen[id], n)
				return finiteIncrement(ctx, a)
			})
			setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				setups = append(setups, finiteChain(a))
				return nil
			})
			build := finiteBuild(t, p, 8, true, h, setup)
			f := newFiniteFixture(t, backend, build)
			f.options.OnCommitted = func(context.Context, GroupResult) error { return finiteStop }
			r := f.start(t)
			first, e := r.Run(context.Background())
			if !errors.Is(e, finiteStop) || first.Completed || first.Resumed || len(first.Groups) != 1 {
				t.Fatal(first, e)
			}
			prefix := first.Groups[0]
			if prefix.Events == 0 || !prefix.Applied || prefix.Attempts != 1 {
				t.Fatal(prefix)
			}
			if !reflect.DeepEqual(setups, []uint64{1, 2}) {
				t.Fatal("actual setup callback order", setups)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			f.options.OnCommitted = nil
			r = f.start(t)
			result, e := r.Run(context.Background())
			if e != nil || !result.Completed || !result.Resumed {
				t.Fatal(result, e)
			}
			totals := map[uint64]int{prefix.ChainID: prefix.Events}
			for _, g := range result.Groups {
				totals[g.ChainID] += g.Events
				if !g.Applied || g.Attempts != 1 {
					t.Fatal(g)
				}
			}
			if !reflect.DeepEqual(totals, map[uint64]int{1: 8, 2: 8}) {
				t.Fatal("event totals", totals)
			}
			for _, id := range []uint64{1, 2} {
				want := []uint64{1, 2, 3, 4, 5, 6, 7, 8}
				if !reflect.DeepEqual(seen[id], want) {
					t.Fatalf("chain%d events %v", id, seen[id])
				}
			}
			if !reflect.DeepEqual(setups, []uint64{1, 2}) {
				t.Fatal("setup repeated on durable resume", setups)
			}
			if got := finiteBalances(t, r); !reflect.DeepEqual(got, map[string]string{"chain1": "8", "chain2": "8"}) {
				t.Fatal(got)
			}
			finiteAssertEnds(t, r, 8)
			again, e := r.Run(context.Background())
			if e != nil || !again.Completed || !again.Resumed || len(again.Groups) != 0 {
				t.Fatal("completed replay", again, e)
			}
			if p.inflight.Load() != 0 {
				t.Fatal("RPC work survived finite run")
			}
		})
	}
}

type finiteDelegate struct {
	historicalTransactions
	beforeGroup func(checkpoint.Checkpoint, []indexing.Object) error
	afterGroup  func(bool, error) (bool, error)
	init        func(context.Context, []indexing.InitialChainCheckpoint) error
	snapshot    func(context.Context) (store.HistoricalSnapshot, error)
}

func (d *finiteDelegate) HistoricalSnapshot(ctx context.Context) (store.HistoricalSnapshot, error) {
	if d.snapshot != nil {
		return d.snapshot(ctx)
	}
	return d.historicalTransactions.HistoricalSnapshot(ctx)
}
func (d *finiteDelegate) InitializeMultichain(ctx context.Context, s []indexing.InitialChainCheckpoint) error {
	if d.init != nil {
		return d.init(ctx, s)
	}
	return d.historicalTransactions.InitializeMultichain(ctx, s)
}
func (d *finiteDelegate) HistoricalGroupWithRefetch(ctx context.Context, c checkpoint.Checkpoint, e []indexing.Object, u func(indexing.Object, indexing.Object), f func(context.Context, []indexing.Object) error) (bool, error) {
	if d.beforeGroup != nil {
		if err := d.beforeGroup(c, e); err != nil {
			return false, err
		}
	}
	v, err := d.historicalTransactions.HistoricalGroupWithRefetch(ctx, c, e, u, f)
	if d.afterGroup != nil {
		return d.afterGroup(v, err)
	}
	return v, err
}
func finiteNarrow(build *config.SourceIndexingBuild) {
	for _, g := range build.Executable().EventCallbacks {
		for _, cb := range g {
			cb["filter"].(map[string]any)["include"] = []any{"block.number"}
		}
	}
}
func TestFiniteHistoricalRunnerRetry(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			for _, mode := range []string{"sticky_false", "sticky_true", "nonretryable"} {
				t.Run(mode, func(t *testing.T) {
					p := newFiniteRPC(t, 4)
					missing := map[string]int{}
					attempted := map[string]int{}
					successful := map[string]int{}
					var waits []time.Duration
					var current *Runner
					h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
						n, e := finiteNumber(a)
						if e != nil {
							return e
						}
						key := fmt.Sprintf("%d/%d", finiteChain(a), n)
						attempted[key]++
						if _, e = a.Context.Client.GetBalance(ctx, "0x0000000000000000000000000000000000000001"); e != nil {
							return e
						}
						if e = current.owner.Clients().WaitCacheWrites(ctx); e != nil {
							return e
						}
						if e = finiteIncrement(ctx, a); e != nil {
							return e
						}
						if mode == "nonretryable" {
							return &store.NonRetryableUserError{Message: "controlled terminal user failure"}
						}
						block, _ := a.Event.Property("block")
						_, e = block.Get("gasUsed")
						if e != nil {
							missing[key]++
							if mode == "sticky_true" {
								return nil
							}
							return e
						}
						successful[key]++
						return nil
					})
					build := finiteBuild(t, p, 4, true, h, nil)
					finiteNarrow(build)
					f := newFiniteFixture(t, backend, build)
					f.options.RetryWait = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return ctx.Err() }
					r := f.start(t)
					current = r
					// Observe actual extraction batches before any transaction. A group with a
					// missing gasUsed must fail at its first event exactly once, then refetch.
					expectedMissing := map[string]int{}
					seenGroups := map[string]bool{}
					r.transactions = &finiteDelegate{historicalTransactions: r.owner, beforeGroup: func(cp checkpoint.Checkpoint, es []indexing.Object) error {
						key := checkpoint.MustEncode(cp)
						if seenGroups[key] {
							return nil
						}
						seenGroups[key] = true
						if len(es) > 0 {
							body := es[0]["event"].(map[string]any)
							block := body["block"].(map[string]any)
							if _, ok := block["gasUsed"]; !ok {
								n, _ := uintQuantity(block["number"])
								expectedMissing[fmt.Sprintf("%d/%d", cp.ChainID, n)]++
							}
						}
						return nil
					}}
					result, e := r.Run(context.Background())
					if mode == "nonretryable" {
						var terminal *store.NonRetryableUserError
						if !errors.As(e, &terminal) || result.Completed || len(waits) != 0 || len(attempted) != 1 {
							t.Fatal(result, e, waits, attempted)
						}
						if len(finiteBalances(t, r)) != 0 {
							t.Fatal("failed business group committed")
						}
					} else {
						if backend == "pglite" {
							var invalid *indexing.InvalidEventAccessError
							if !errors.As(e, &invalid) || result.Completed || len(waits) != 0 {
								t.Fatal(result, e, waits)
							}
							if !reflect.DeepEqual(missing, expectedMissing) || len(missing) != 1 {
								t.Fatal("first actual narrow group", missing, expectedMissing)
							}
							if len(finiteBalances(t, r)) != 0 {
								t.Fatal("native PGlite failed group did not rollback")
							}
							if e = r.Close(context.Background()); e != nil {
								t.Fatal(e)
							}
							r = f.start(t)
							current = r
							result, e = r.Run(context.Background())
						}
						if e != nil || !result.Completed {
							var invalid *indexing.InvalidEventAccessError
							if errors.As(e, &invalid) {
								t.Fatalf("missing-field attempts: %s key=%s retries=%d missing=%v expected=%v attempted=%v result=%+v error=%v", invalid.ErrorName(), invalid.Key, len(waits), missing, expectedMissing, attempted, result, e)
							}
							t.Fatal(result, e)
						}
						if backend == "postgres" {
							if !reflect.DeepEqual(missing, expectedMissing) || len(missing) == 0 || len(waits) != len(missing) {
								t.Fatal("exact observed narrow groups", missing, expectedMissing, waits)
							}
							for _, d := range waits {
								if d != 125*time.Millisecond {
									t.Fatal(d)
								}
							}
						}
						wantSuccess := map[string]int{}
						for _, id := range []uint64{1, 2} {
							for n := uint64(1); n <= 4; n++ {
								wantSuccess[fmt.Sprintf("%d/%d", id, n)] = 1
							}
						}
						if !reflect.DeepEqual(successful, wantSuccess) {
							t.Fatal("lost or duplicate success", successful)
						}
						for key, n := range attempted {
							if n != successful[key]+missing[key] {
								t.Fatal("unexplained attempt", key, n, successful[key], missing[key])
							}
						}
						if got := finiteBalances(t, r); !reflect.DeepEqual(got, map[string]string{"chain1": "4", "chain2": "4"}) {
							t.Fatal(got)
						}
						finiteAssertEnds(t, r, 4)
					}
					if e = r.owner.Clients().WaitCacheWrites(context.Background()); e != nil {
						t.Fatal(e)
					}
					// Real independent cache persists each exact key once despite business retry.
					cacheCalls := map[string]int{}
					for _, c := range p.snapshot() {
						if c.Method == "eth_getBalance" {
							raw, _ := json.Marshal(c.Params)
							cacheCalls[fmt.Sprintf("%d/%s", c.Chain, raw)]++
						}
					}
					for key, n := range cacheCalls {
						if n != 1 {
							t.Fatal("same cache key refetched during retry", key, n)
						}
					}
					if len(cacheCalls) != len(attempted) {
						t.Fatal("RPC keys do not match actual attempted events", cacheCalls, attempted)
					}
					if e = r.Close(context.Background()); e != nil {
						t.Fatal(e)
					}
					r = f.start(t)
					for _, c := range p.snapshot() {
						if c.Method != "eth_getBalance" {
							continue
						}
						client, e := r.owner.Clients().GetClient(build.Executable().Chains[c.Chain-1])
						if e != nil {
							t.Fatal(e)
						}
						number, _ := uintQuantity(c.Params[1])
						if _, e = client.GetBalance(context.Background(), fmt.Sprint(c.Params[0]), indexing.BalanceOptions{BlockNumber: new(big.Int).SetUint64(number)}); e != nil {
							t.Fatal(e)
						}
					}
					after := 0
					for _, c := range p.snapshot() {
						if c.Method == "eth_getBalance" {
							after++
						}
					}
					if after != len(cacheCalls) {
						t.Fatal("independent committed cache lost", after, cacheCalls)
					}
				})
			}
		})
	}
}

func TestFiniteHistoricalRunnerUnknownCommit(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			for _, mode := range []string{"setup", "before", "after", "unreadable"} {
				t.Run(mode, func(t *testing.T) {
					p := newFiniteRPC(t, 3)
					setupCount := 0
					handlerCount := 0
					waits := 0
					h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
						handlerCount++
						return finiteIncrement(ctx, a)
					})
					build := finiteBuild(t, p, 3, true, h, indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { setupCount++; return nil }))
					f := newFiniteFixture(t, backend, build)
					f.options.RetryWait = func(context.Context, time.Duration) error { waits++; return nil }
					r := f.start(t)
					injected := false
					unreadable := false
					groupCalls := 0
					unknown := &store.CommitOutcomeUnknownError{Err: errors.New("controlled acknowledgement loss")}
					delegate := &finiteDelegate{historicalTransactions: r.owner}
					delegate.init = func(ctx context.Context, initial []indexing.InitialChainCheckpoint) error {
						e := r.owner.InitializeMultichain(ctx, initial)
						if e == nil && mode == "setup" {
							injected = true
							return unknown
						}
						return e
					}
					delegate.beforeGroup = func(checkpoint.Checkpoint, []indexing.Object) error {
						groupCalls++
						if mode == "before" && !injected {
							injected = true
							return unknown
						}
						return nil
					}
					delegate.afterGroup = func(applied bool, e error) (bool, error) {
						if e == nil && !injected && (mode == "after" || mode == "unreadable") {
							injected = true
							unreadable = mode == "unreadable"
							return applied, unknown
						}
						return applied, e
					}
					delegate.snapshot = func(ctx context.Context) (store.HistoricalSnapshot, error) {
						if unreadable {
							return store.HistoricalSnapshot{}, errors.New("controlled unavailable durable read")
						}
						return r.owner.HistoricalSnapshot(ctx)
					}
					r.transactions = delegate
					result, e := r.Run(context.Background())
					if !injected || waits != 0 {
						t.Fatal("unknown was blindly retried", injected, waits)
					}
					switch mode {
					case "setup", "after":
						if e != nil || !result.Completed {
							t.Fatal(result, e)
						}
						if mode == "after" && !result.Groups[0].Reconciled {
							t.Fatal("not reconciled", result)
						}
						finiteAssertEnds(t, r, 3)
					case "before":
						if !errors.Is(e, unknown) || groupCalls != 1 || handlerCount != 0 || len(finiteBalances(t, r)) != 0 {
							t.Fatal(result, e, groupCalls, handlerCount)
						}
					case "unreadable":
						if !errors.Is(e, unknown) || groupCalls != 1 || result.Completed || handlerCount == 0 {
							t.Fatal(result, e, groupCalls, handlerCount)
						}
					}
					if setupCount != 2 {
						t.Fatal("setup duplicated", setupCount)
					}
					if e = r.Close(context.Background()); e != nil {
						t.Fatal(e)
					}
					r = f.start(t)
					result, e = r.Run(context.Background())
					if e != nil || !result.Completed || !result.Resumed || setupCount != 2 {
						t.Fatal(result, e, setupCount)
					}
					if got := finiteBalances(t, r); !reflect.DeepEqual(got, map[string]string{"chain1": "3", "chain2": "3"}) {
						t.Fatal(got)
					}
					finiteAssertEnds(t, r, 3)
				})
			}
		})
	}
}

// Scheduling wrapper: actual Client.BeginCall is invoked synchronously before
// returning its completion relay. Holding completion is an explicit test gate,
// never a goroutine around Call advertised as async admission.
type finiteObservedRPC struct {
	base     *rpc.Client
	admit    func(context.Context, string, any)
	hold     func(string, any) bool
	entered  chan struct{}
	release  chan struct{}
	gateOnce sync.Once
}

func (r *finiteObservedRPC) Actions() *rpc.Actions { return r.base.Actions() }
func (r *finiteObservedRPC) Call(ctx context.Context, m string, p, out any) error {
	return r.base.Call(ctx, m, p, out)
}
func (r *finiteObservedRPC) BeginCall(ctx context.Context, m string, p, out any) <-chan error {
	if r.admit != nil {
		r.admit(ctx, m, p)
	}
	future := r.base.BeginCall(ctx, m, p, out)
	if r.hold == nil || !r.hold(m, p) {
		return future
	}
	held := false
	r.gateOnce.Do(func() { held = true; close(r.entered) })
	if !held {
		return future
	}
	done := make(chan error, 1)
	go func() { e := <-future; <-r.release; done <- e; close(done) }()
	return done
}
func finiteWait(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
func TestFiniteHistoricalRunnerCancellation(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			for _, mode := range []string{"acquisition_close_timeout", "handler"} {
				t.Run(mode, func(t *testing.T) {
					p := newFiniteRPC(t, 4)
					entered := make(chan struct{})
					release := make(chan struct{})
					var releaseOnce sync.Once
					releaseGate := func() { releaseOnce.Do(func() { close(release) }) }
					var once sync.Once
					h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
						if mode == "handler" {
							once.Do(func() { close(entered) })
							<-release
							return ctx.Err()
						}
						return finiteIncrement(ctx, a)
					})
					build := finiteBuild(t, p, 4, true, h, nil)
					f := newFiniteFixture(t, backend, build)
					if mode == "acquisition_close_timeout" {
						f.options.Owned.Requesters = []rpc.Requester{}
						for i, base := range build.Executable().RPCs {
							wrapper := &finiteObservedRPC{base: base}
							if i == 0 {
								wrapper.entered = entered
								wrapper.release = release
								wrapper.hold = func(m string, _ any) bool { return m == "eth_getBlockByNumber" }
							}
							f.options.Owned.Requesters = append(f.options.Owned.Requesters, wrapper)
						}
					}
					r, closeScope := f.startWithCloseScope(t)
					t.Cleanup(releaseGate) // LIFO: unblock admitted work before owner Close.
					done := make(chan error, 1)
					go func() { _, e := r.Run(context.Background()); done <- e }()
					finiteWait(t, entered, mode)
					timeout, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
					e := r.Close(timeout)
					cancel()
					if !errors.Is(e, context.DeadlineExceeded) {
						t.Fatal("Close released unfinished work", e)
					}
					closeScope.markTriggered(r) // The actual deliberate Close already timed out.
					if _, releaseBuild, e := build.Executable().AcquireSession(); e == nil {
						releaseBuild()
						t.Fatal("build lease released before actual Close")
					}
					releaseGate()
					select {
					case e := <-done:
						if e == nil {
							t.Fatal("canceled run succeeded")
						}
					case <-time.After(15 * time.Second):
						t.Fatal("Run did not join")
					}
					if e = r.Close(context.Background()); !closeScope.accepts(r, e) {
						t.Fatal(e)
					}
					if p.inflight.Load() != 0 {
						t.Fatal("RPC survived close")
					}
					// The same graph and persistent storage can be acquired only after join.
					f.options.Owned.Requesters = nil
					r = f.start(t)
					if e = r.Close(context.Background()); e != nil {
						t.Fatal(e)
					}
				})
			}
		})
	}
}

func TestFiniteHistoricalRunnerFiniteBoundary(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			p := newFiniteRPC(t, 5)
			build := finiteBuild(t, p, 5, true, indexing.Handler(finiteIncrement), nil)
			f := newFiniteFixture(t, backend, build)
			var clock atomic.Int64
			clock.Store(2000)
			f.options.Clock = func() time.Time { return time.Unix(clock.Load(), 0) }
			p.setHook(func(_ context.Context, c finiteRPCRequest) error {
				if c.Method == "eth_getBlockByNumber" && c.Params[0] == "latest" {
					return errors.New("finite runner must not refresh latest")
				}
				return nil
			})
			ended := map[uint64]bool{}
			f.options.OnCommitted = func(_ context.Context, g GroupResult) error {
				cp, e := checkpoint.Decode(g.Checkpoint)
				if e != nil {
					return e
				}
				if cp.BlockNumber == 5 {
					ended[g.ChainID] = true
				}
				if len(ended) == 2 {
					clock.Add(60)
				}
				return nil
			}
			r := f.start(t)
			result, e := r.Run(context.Background())
			if e != nil || !result.Completed {
				t.Fatal(result, e)
			}
			finiteAssertEnds(t, r, 5)
			latest := 0
			for _, c := range p.snapshot() {
				if c.Method == "eth_getBlockByNumber" && c.Params[0] == "latest" {
					latest++
				}
			}
			if latest != 2 || p.inflight.Load() != 0 {
				t.Fatal("extra latest read or worker leak", latest, p.inflight.Load())
			}
		})
	}
}
func TestFiniteHistoricalRunnerCacheDisabled(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			p := newFiniteRPC(t, 4)
			build := finiteBuild(t, p, 4, false, indexing.Handler(finiteIncrement), nil)
			r := newFiniteFixture(t, backend, build).start(t)
			result, e := r.Run(context.Background())
			if e != nil || !result.Completed {
				t.Fatal(result, e)
			}
			finiteAssertEnds(t, r, 4)
			e = r.owner.WithHistoricalSync(context.Background(), 0, func(ctx context.Context, s *indexing.HistoricalSync) error {
				for _, g := range build.Executable().EventCallbacks {
					filters := []filter.Object{g[0]["filter"].(map[string]any)}
					intervals, e := s.GetIntervals(ctx, filters)
					if e != nil {
						return e
					}
					id, _ := uintQuantity(filters[0]["chainId"])
					data, e := s.GetEventData(ctx, store.SourceEventDataArgs{ChainID: id, Filters: filters, FromBlock: 1, ToBlock: 4, Limit: 10})
					if e != nil {
						return e
					}
					if len(data.Blocks) != 0 {
						return fmt.Errorf("cache-disabled blocks persisted: %d", len(data.Blocks))
					}
					for _, entry := range intervals.Entries() {
						for _, v := range entry.Values {
							if len(v.Intervals) != 0 {
								return fmt.Errorf("cache-disabled intervals persisted: %v", v.Intervals)
							}
						}
					}
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}

// A close allowance belongs to one runner, never to the fixture or a reopened
// owner. Triggering does nothing for PGlite or a different runner instance.
type finiteCloseScope struct {
	runner    *Runner
	postgres  bool
	triggered atomic.Bool
}

func (s *finiteCloseScope) markTriggered(r *Runner) {
	if s != nil && r != nil && s.runner == r && s.postgres {
		s.triggered.Store(true)
	}
}
func (s *finiteCloseScope) accepts(r *Runner, err error) bool {
	return err == nil || (s != nil && r != nil && s.runner == r && s.postgres && s.triggered.Load() && finiteClosedOnly(err))
}
func finiteClosedOnly(err error) bool {
	if err == nil {
		return true
	}
	return finiteClosedLeaves(err, 0)
}
func finiteClosedLeaves(err error, depth int) bool {
	// Conservative rejection also bounds malformed cyclic/overdeep wrappers.
	if err == nil || depth >= 64 {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		leaf := false
		for _, child := range many.Unwrap() {
			if child == nil {
				continue
			}
			leaf = true
			if !finiteClosedLeaves(child, depth+1) {
				return false
			}
		}
		return leaf
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return finiteClosedLeaves(one.Unwrap(), depth+1)
	}
	// Do not use errors.Is: one allowed branch or a custom Is method must not
	// conceal cancellation, deadline, or arbitrary leaves elsewhere in a tree.
	return err == sql.ErrConnDone || err == driver.ErrBadConn
}
func TestFiniteHistoricalRunnerFencedPostgres(t *testing.T) {
	p := newFiniteRPC(t, 3)
	calls := 0
	waits := 0
	killed := false
	var f finiteFixture
	var r *Runner
	var closeScope *finiteCloseScope
	h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
		calls++
		if !killed {
			result, e := a.Context.DB.SQL(ctx, "SELECT pg_backend_pid()::text AS pid")
			if e != nil {
				return e
			}
			if len(result.Rows) != 1 {
				return errors.New("missing backend pid")
			}
			admin, e := store.OpenPostgres(context.Background(), f.options.Owned.Storage.PostgresURL)
			if e != nil {
				return e
			}
			defer admin.Close()
			result, e = admin.Query(context.Background(), "SELECT pg_terminate_backend($1::integer) AS terminated", result.Rows[0]["pid"])
			if e != nil {
				return e
			}
			if len(result.Rows) != 1 || result.Rows[0]["terminated"] != true {
				return fmt.Errorf("owner not terminated: %v", result)
			}
			killed = true
			closeScope.markTriggered(r) // pg_terminate_backend returned true.
		}
		return finiteIncrement(ctx, a)
	})
	build := finiteBuild(t, p, 3, true, h, nil)
	f = newFiniteFixture(t, "postgres", build)
	f.options.RetryWait = func(context.Context, time.Duration) error { waits++; return nil }
	r, closeScope = f.startWithCloseScope(t)
	result, e := r.Run(context.Background())
	if e == nil || result.Completed || calls != 1 || waits != 0 || !killed {
		t.Fatal("owner loss retried or completed", result, e, calls, waits)
	}
	if _, e = r.owner.HistoricalSnapshot(context.Background()); e == nil {
		t.Fatal("fenced owner silently reconnected")
	}
	if e = r.Close(context.Background()); !closeScope.accepts(r, e) {
		t.Fatal(e)
	}
	r = f.start(t)
	result, e = r.Run(context.Background())
	if e != nil || !result.Completed {
		t.Fatal(result, e)
	}
	finiteAssertEnds(t, r, 3)
	if got := finiteBalances(t, r); !reflect.DeepEqual(got, map[string]string{"chain1": "3", "chain2": "3"}) {
		t.Fatal(got)
	}
}
func TestFiniteHistoricalRunnerValidation(t *testing.T) {
	cases := []string{"mode", "unbounded", "unfinalized", "invalid_handler", "nil_handler", "nil_function", "partial_setup", "nil_requester", "negative_query"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			p := newFiniteRPC(t, 3)
			build := finiteBuild(t, p, 3, true, indexing.Handler(finiteIncrement), nil)
			b := build.Executable()
			options := Options{Build: build}
			switch name {
			case "mode":
				b.Ordering = "isolated"
			case "unbounded":
				delete(b.EventCallbacks[0][0]["filter"].(map[string]any), "toBlock")
			case "unfinalized":
				b.EventCallbacks[0][0]["filter"].(map[string]any)["toBlock"] = 4
			case "invalid_handler":
				b.EventCallbacks[0][0]["fn"] = func() {}
			case "nil_handler":
				var fn indexing.Handler
				b.EventCallbacks[0][0]["fn"] = fn
			case "nil_function":
				var fn func(context.Context, *indexing.HandlerArgs) error
				b.EventCallbacks[0][0]["fn"] = fn
			case "partial_setup":
				b.SetupCallbacks = b.SetupCallbacks[:1]
			case "nil_requester":
				options.Owned.Requesters = []rpc.Requester{(*rpc.Client)(nil), b.RPCs[1]}
			case "negative_query":
				options.QuerySize = -1
			}
			opened := 0
			_, e := open(context.Background(), options, func(context.Context, indexing.OwnedOptions) (*indexing.Owned, error) {
				opened++
				return nil, errors.New("storage opened")
			})
			if e == nil || opened != 0 {
				t.Fatal("invalid build reached storage", e, opened)
			}
			_, release, e := b.AcquireSession()
			if e != nil {
				t.Fatal("failed constructor leaked lease", e)
			}
			release()
		})
	}
}
func TestFiniteHistoricalRunnerDefaultFieldOrder(t *testing.T) {
	item := abi.Event{Type: "event", Name: "Order", Inputs: []abi.Parameter{{Name: "a", HasName: true, Type: "uint256"}, {Name: "z", HasName: true, Type: "uint256", Indexed: true}}}
	callback := events.Object{"type": "contract", "name": "Token:Order", "abiItem": item, "filter": events.Object{"type": "log"}}
	raw := events.RawEvent{EventCallbackIndex: 0, Checkpoint: "actual-decode", Block: events.Object{"number": big.NewInt(1)}, Log: events.Object{"topics": []string{abi.EventSelector(item), fmt.Sprintf("0x%064x", 9)}, "data": fmt.Sprintf("0x%064x", 9), "logIndex": 0, "address": "0x" + strings.Repeat("1", 40)}}
	decoded, e := (&events.Decoder{}).DecodeEvents(events.Object{"id": 1}, []events.Object{callback}, []events.RawEvent{raw})
	if e != nil || len(decoded) != 1 {
		t.Fatal(decoded, e)
	}
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprint("override=", override), func(t *testing.T) {
			p := newFiniteRPC(t, 3)
			build := finiteBuild(t, p, 3, true, indexing.Handler(finiteIncrement), nil)
			options := Options{Build: build}
			want := []string{"z", "a"}
			if override {
				want = []string{"a", "z"}
				options.Owned.Service.FieldOrder = func(indexing.Object) map[string][]string { return map[string][]string{"args": {"a", "z"}} }
			}
			marker := errors.New("stop before storage")
			seen := false
			_, e := open(context.Background(), options, func(_ context.Context, o indexing.OwnedOptions) (*indexing.Owned, error) {
				seen = true
				if o.Service.FieldOrder == nil || !reflect.DeepEqual(o.Service.FieldOrder(decoded[0])["args"], want) {
					t.Fatal("constructor metadata", want)
				}
				return nil, marker
			})
			if !seen || !errors.Is(e, marker) {
				t.Fatal(seen, e)
			}
		})
	}
}

// Observe only quiescent session state: r.prefetch has joined both admission
// jobs and the real business delegate has not yet entered its transaction.
// No unsafe conversion, mutation, SQL or production visibility hook is used.
func finitePrefetchedKeys(r *Runner) []string {
	db := reflect.ValueOf(r.owner.Database()).Elem()
	session := db.FieldByName("session").Elem()
	if !session.FieldByName("active").IsNil() || session.FieldByName("reading").Bool() {
		panic("prefetch observation overlaps a binding")
	}
	tables := session.FieldByName("tables")
	table := tables.MapIndex(reflect.ValueOf("account"))
	if !table.IsValid() || table.IsNil() {
		return nil
	}
	v := table.Elem()
	if v.FieldByName("complete").Bool() {
		return nil
	}
	out := []string{}
	m := v.FieldByName("prefetched")
	for _, key := range m.MapKeys() {
		if m.MapIndex(key).Bool() {
			out = append(out, key.String())
		}
	}
	sort.Strings(out)
	return out
}
func TestFiniteHistoricalRunnerProfiles(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			const end = 121
			p := newFiniteRPC(t, end)
			contractABI, e := abi.Parse(`[{"type":"function","name":"value","inputs":[{"name":"n","type":"uint256"}],"outputs":[{"type":"uint256"}]}]`)
			if e != nil {
				t.Fatal(e)
			}
			var current *Runner
			missing := 0
			expectedMissing := 0
			observedGroups := map[string]bool{}
			profilesObserved := false
			successfulCalls := uint64(0)
			closedCounts, closedCompleted := uint64(0), uint64(0)
			admittedOutside := atomic.Int64{}
			sqlKeys := map[string]bool{}
			var includeBeforeReset any // Retain the actual first learned slice, not just its address.
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				p.handlerActive.Store(true)
				defer p.handlerActive.Store(false)
				n, e := finiteNumber(a)
				if e != nil {
					return e
				}
				row, e := a.Context.DB.Find(ctx, "account", store.Row{"id": strconv.FormatUint(n, 10)})
				if e != nil {
					return e
				}
				if row != nil {
					return fmt.Errorf("unexpected predicted negative row %d", n)
				}
				v, e := a.Context.Client.ReadContract(ctx, indexing.ContractCall{ABI: contractABI, Address: "0x0000000000000000000000000000000000000001", FunctionName: "value", Args: []any{new(big.Int).SetUint64(n)}})
				if e != nil {
					return e
				}
				if fmt.Sprint(v) != "42" {
					return fmt.Errorf("RPC decode %v", v)
				}
				if n == end {
					pattern := current.owner.Service().ColumnAccessPattern()["Blocks:block"]
					if pattern.Resolved && includeBeforeReset == nil {
						includeBeforeReset = current.build.EventCallbacks[0][0]["filter"].(map[string]any)["include"]
					}
					block, _ := a.Event.Property("block")
					if _, e = block.Get("gasUsed"); e != nil {
						missing++
						return e
					}
				}
				if e := finiteIncrement(ctx, a); e != nil {
					return e
				}
				successfulCalls++
				return nil
			})
			build := finiteBuild(t, p, end, true, h, nil)
			f := newFiniteFixture(t, backend, build)
			f.options.QuerySize = 3
			f.options.Owned.Cache = store.CacheOptions{Complete: true, MaxBytes: 1}
			f.options.RetryWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
			for _, base := range build.Executable().RPCs {
				f.options.Owned.Requesters = append(f.options.Owned.Requesters, &finiteObservedRPC{base: base, admit: func(_ context.Context, m string, _ any) {
					if m == "eth_call" && !p.handlerActive.Load() {
						admittedOutside.Add(1)
					}
				}})
			}
			r := f.start(t)
			current = r
			r.transactions = &finiteDelegate{historicalTransactions: r.owner, beforeGroup: func(cp checkpoint.Checkpoint, es []indexing.Object) error {
				groupKey := checkpoint.MustEncode(cp)
				if !observedGroups[groupKey] {
					observedGroups[groupKey] = true
					for _, event := range es {
						block := event["event"].(map[string]any)["block"].(map[string]any)
						n, _ := uintQuantity(block["number"])
						if n == end {
							if _, ok := block["gasUsed"]; !ok {
								expectedMissing++
							}
						}
					}
				}
				keys := finitePrefetchedKeys(r)
				for _, key := range keys {
					var values []string
					if e := json.Unmarshal([]byte(key), &values); e != nil {
						return e
					}
					if len(values) == 1 {
						if n, e := strconv.ParseUint(values[0], 10, 64); e == nil && n > 0 && n <= end {
							sqlKeys[key] = true
						}
					}
				}
				return nil
			}}
			result, e := r.Run(context.Background())
			profilesObserved = len(r.owner.Clients().Profiles("Blocks:block")) > 0
			if backend == "pglite" && e != nil {
				var invalid *indexing.InvalidEventAccessError
				if !errors.As(e, &invalid) {
					t.Fatal(e)
				}
				closedCounts = r.owner.Service().EventCount()["Blocks:block"]
				closedCompleted = r.owner.Service().Metrics().Completed["Blocks:block"]
				prefix, snapshotErr := r.owner.HistoricalSnapshot(context.Background())
				if snapshotErr != nil {
					t.Fatal(snapshotErr)
				}
				t.Logf("profiles pre-reopen: snapshot=%+v rows=%v successful_calls=%d missing=%d expected_missing=%d", prefix, finiteBalances(t, r), successfulCalls, missing, expectedMissing)
				if e = r.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				r = f.start(t)
				current = r
				result, e = r.Run(context.Background())
			}
			if e != nil || !result.Completed {
				t.Fatal(result, e)
			}
			finiteAssertEnds(t, r, end)
			if count := closedCounts + r.owner.Service().EventCount()["Blocks:block"]; count != successfulCalls {
				t.Fatal("profile attempt count mismatch", count, successfulCalls)
			}
			if completed := closedCompleted + r.owner.Service().Metrics().Completed["Blocks:block"]; completed != 242 {
				t.Fatal("completed metrics did not restore failed transaction prefix", completed)
			}
			if got := finiteBalances(t, r); !reflect.DeepEqual(got, map[string]string{"chain1": "121", "chain2": "121"}) {
				t.Fatal(got)
			}
			if len(sqlKeys) == 0 || admittedOutside.Load() == 0 {
				t.Fatal("no observed SQL-backed negative key or actual RPC admission prefetch", sqlKeys, admittedOutside.Load())
			}
			if missing == 0 || missing != expectedMissing || includeBeforeReset == nil {
				t.Fatal("late resolved-field access differs from exact observed narrow groups", missing, expectedMissing, includeBeforeReset)
			}
			after := r.build.EventCallbacks[0][0]["filter"].(map[string]any)["include"]
			if reflect.ValueOf(after).Pointer() == reflect.ValueOf(includeBeforeReset).Pointer() {
				t.Fatal("reset did not replace learned include array")
			}
			if !profilesObserved {
				t.Fatal("RPC profiles discarded before rollback/reopen boundary")
			}
			if p.inflight.Load() != 0 {
				t.Fatal("profile work not joined")
			}
		})
	}
}

func TestFiniteHistoricalRunnerPrefetchCancellation(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			p := newFiniteRPC(t, 24)
			a, e := abi.Parse(`[{"type":"function","name":"value","inputs":[{"name":"n","type":"uint256"}],"outputs":[{"type":"uint256"}]}]`)
			if e != nil {
				t.Fatal(e)
			}
			var runner *Runner
			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseGate := func() { releaseOnce.Do(func() { close(release) }) }
			var gateOnce sync.Once
			var held atomic.Bool
			h := indexing.Handler(func(ctx context.Context, args *indexing.HandlerArgs) error {
				p.handlerActive.Store(true)
				defer p.handlerActive.Store(false)
				n, e := finiteNumber(args)
				if e != nil {
					return e
				}
				_, e = args.Context.Client.ReadContract(ctx, indexing.ContractCall{ABI: a, Address: "0x0000000000000000000000000000000000000001", FunctionName: "value", Args: []any{new(big.Int).SetUint64(n)}})
				if e != nil {
					return e
				}
				return finiteIncrement(ctx, args)
			})
			build := finiteBuild(t, p, 24, true, h, nil)
			f := newFiniteFixture(t, backend, build)
			// Hold one actual async eth_call completion only when admitted outside the
			// handler. Profile discovery is real; no manually seeded predictions.
			for _, base := range build.Executable().RPCs {
				wrapper := &finiteObservedRPC{base: base, entered: make(chan struct{}), release: release}
				wrapper.hold = func(m string, _ any) bool {
					if m != "eth_call" || p.handlerActive.Load() {
						return false
					}
					chosen := false
					gateOnce.Do(func() { chosen = true; held.Store(true); close(entered) })
					return chosen
				}
				f.options.Owned.Requesters = append(f.options.Owned.Requesters, wrapper)
			}
			runner, closeScope := f.startWithCloseScope(t)
			t.Cleanup(releaseGate) // LIFO: release even if a preceding assertion fails.
			done := make(chan error, 1)
			go func() { _, e := runner.Run(context.Background()); done <- e }()
			finiteWait(t, entered, "actual RPC prefetch admission")
			timeout, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			e = runner.Close(timeout)
			cancel()
			if !errors.Is(e, context.DeadlineExceeded) {
				t.Fatal("Close did not retain pending prefetch", e)
			}
			closeScope.markTriggered(runner) // The actual deliberate Close already timed out.
			if _, releaseBuild, e := build.Executable().AcquireSession(); e == nil {
				releaseBuild()
				t.Fatal("prefetch escaped build ownership")
			}
			releaseGate()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("prefetch cancellation completed run")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("prefetch cancellation did not join")
			}
			if e = runner.Close(context.Background()); !closeScope.accepts(runner, e) {
				t.Fatal(e)
			}
			if !held.Load() || p.inflight.Load() != 0 {
				t.Fatal("prefetch gate/work not joined")
			}
			f.options.Owned.Requesters = nil
			runner = f.start(t)
			result, e := runner.Run(context.Background())
			if e != nil || !result.Completed {
				t.Fatal("durable reopen after cancellation", result, e)
			}
			finiteAssertEnds(t, runner, 24)
		})
	}
}

func TestFiniteHistoricalRunnerEmptyAndDuplicate(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			p := newFiniteRPC(t, 4)
			raw := fmt.Sprintf(`{"chains":{"one":{"id":1,"rpc":%q,"reorgWindow":0},"two":{"id":2,"rpc":%q,"reorgWindow":0}},"contracts":{"Token":{"abi":[{"type":"event","name":"Ping","inputs":[]}],"address":"0x0000000000000000000000000000000000000001","chain":{"one":{},"two":{}},"startBlock":1,"endBlock":4}}}`, p.server.URL+"/1", p.server.URL+"/2")
			cfg, e := config.ParseSourceConfiguration([]byte(raw))
			if e != nil {
				t.Fatal(e)
			}
			base, e := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 16})
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range base.RPCClients() {
				c.RequestsPerSecond = 0
				c.Retries = 0
			}
			calls := 0
			build, e := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "Token:Ping", Fn: indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { calls++; return nil })}})
			if e != nil {
				t.Fatal(e)
			}
			r := newFiniteFixture(t, backend, build).start(t)
			result, e := r.Run(context.Background())
			if e != nil || !result.Completed || calls != 0 || len(result.Groups) == 0 {
				t.Fatal(result, e, calls)
			}
			for _, g := range result.Groups {
				if g.Events != 0 || !g.Applied || g.Attempts != 1 {
					t.Fatal("empty acquisition group", g)
				}
			}
			finiteAssertEnds(t, r, 4)
			state, e := r.owner.HistoricalSnapshot(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range state.Chains {
				cp, e := checkpoint.Decode(c.Cursor)
				if e != nil {
					t.Fatal(e)
				}
				applied, e := r.owner.HistoricalGroup(context.Background(), cp, nil, nil)
				if e != nil || applied {
					t.Fatal("durable duplicate group replay", applied, e)
				}
			}
			again, e := r.Run(context.Background())
			if e != nil || !again.Completed || len(again.Groups) != 0 || calls != 0 {
				t.Fatal(again, e, calls)
			}
		})
	}
}

// This vector exercises retry scheduling against the dialect of a real opened
// Owned instance. The callback deliberately performs no SQL/commit: these are
// scheduling-error controls, not evidence of database rollback or lost COMMIT.
func TestFiniteHistoricalRunnerRetryPolicy(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			p := newFiniteRPC(t, 2)
			build := finiteBuild(t, p, 2, true, indexing.Handler(finiteIncrement), nil)
			f := newFiniteFixture(t, backend, build)
			var delays []time.Duration
			f.options.RetryWait = func(ctx context.Context, d time.Duration) error { delays = append(delays, d); return ctx.Err() }
			r := f.start(t)
			calls := 0
			var first error
			err := r.retry(context.Background(), func() error {
				calls++
				e := fmt.Errorf("controlled retry scheduling error %d", calls)
				if first == nil {
					first = e
				}
				return e
			})
			wantCalls := 1
			wantDelays := []time.Duration(nil)
			if backend == "postgres" {
				wantCalls = 10
				for i := 0; i < 9; i++ {
					wantDelays = append(wantDelays, (125*time.Millisecond)<<i)
				}
			}
			if calls != wantCalls || !reflect.DeepEqual(delays, wantDelays) || err != first {
				t.Fatal("retry policy or first-error identity", calls, delays, err, first)
			}
			calls = 0
			delays = nil
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err = r.retry(ctx, func() error { calls++; return first })
			if !errors.Is(err, context.Canceled) || calls != 0 || len(delays) != 0 {
				t.Fatal("pre-canceled retry admitted work", err, calls, delays)
			}
			if backend == "postgres" {
				t.Run("wait_cancellation", func(t *testing.T) {
					calls = 0
					delays = nil
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					r.options.RetryWait = func(ctx context.Context, d time.Duration) error {
						delays = append(delays, d)
						cancel()
						return ctx.Err()
					}
					err = r.retry(ctx, func() error { calls++; return first })
					if !errors.Is(err, context.Canceled) || calls != 1 || !reflect.DeepEqual(delays, []time.Duration{125 * time.Millisecond}) {
						t.Fatal("canceled wait replayed callback", err, calls, delays)
					}
				})
			}
		})
	}
}

func TestFiniteHistoricalRunnerCompletionOrder(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			const end = 4
			p := newFiniteRPC(t, end)
			// Keep persistent synchronization enabled. prepareBlockRangeData acquires
			// eth_getLogs outside Store.WithTransaction; block-header acquisition occurs
			// inside SyncBlockData's transaction and would serialize the other chain.
			raw := fmt.Sprintf(`{"chains":{"one":{"id":1,"rpc":%q,"reorgWindow":0,"cacheRpcRequests":true},"two":{"id":2,"rpc":%q,"reorgWindow":0,"cacheRpcRequests":true}},"contracts":{"Token":{"abi":[{"type":"event","name":"Ping","inputs":[]}],"address":"0x0000000000000000000000000000000000000001","chain":{"one":{},"two":{}},"startBlock":1,"endBlock":4}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":4,"interval":1}}}`, p.server.URL+"/1", p.server.URL+"/2")
			cfg, e := config.ParseSourceConfiguration([]byte(raw))
			if e != nil {
				t.Fatal(e)
			}
			base, e := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 16})
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range base.RPCClients() {
				c.RequestsPerSecond = 0
				c.Retries = 0
			}
			blockCalls := map[uint64]int{}
			logCalls := 0
			build, e := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{
				{Name: "Token:Ping", Fn: indexing.Handler(func(context.Context, *indexing.HandlerArgs) error {
					logCalls++
					return errors.New("provider emitted no log events")
				})},
				{Name: "Blocks:block", Fn: indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					blockCalls[finiteChain(a)]++
					return finiteIncrement(ctx, a)
				})},
			})
			if e != nil {
				t.Fatal(e)
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			continueCallback := make(chan struct{})
			var enteredOnce, releaseOnce, callbackOnce sync.Once
			releaseGate := func() { releaseOnce.Do(func() { close(release) }) }
			releaseCallback := func() { callbackOnce.Do(func() { close(continueCallback) }) }
			// Logs use Requester.Call, not BeginCall. Hold the actual chain1 HTTP
			// response in pre-SQL preparation, and let chain2 complete only after that
			// gate is entered. Neither barrier fabricates or replaces an RPC result.
			p.setHook(func(ctx context.Context, c finiteRPCRequest) error {
				if c.Method != "eth_getLogs" {
					return nil
				}
				if c.Chain == 1 {
					enteredOnce.Do(func() { close(entered) })
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				} else if c.Chain == 2 {
					select {
					case <-entered:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			})
			f := newFiniteFixture(t, backend, build)
			f.options.QuerySize = 64
			first := make(chan GroupResult, 1)
			f.options.OnCommitted = func(ctx context.Context, g GroupResult) error {
				select {
				case first <- g:
				case <-ctx.Done():
					return ctx.Err()
				}
				select {
				case <-continueCallback:
					return finiteStop
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			r := f.start(t)
			t.Cleanup(func() { releaseGate(); releaseCallback() }) // Before owner Close, even on failed assertions.
			type outcome struct {
				result Result
				err    error
			}
			done := make(chan outcome, 1)
			go func() { result, e := r.Run(context.Background()); done <- outcome{result, e} }()
			finiteWait(t, entered, "chain1 real pre-SQL log completion gate")
			var group GroupResult
			select {
			case group = <-first:
			case <-time.After(15 * time.Second):
				t.Fatal("chain2 could not commit while chain1 acquisition was held")
			}
			select {
			case <-release:
				t.Fatal("chain1 was released before first commit observation")
			default:
			}
			cp, e := checkpoint.Decode(group.Checkpoint)
			if e != nil || group.ChainID != 2 || group.Events != end || cp.BlockNumber != end {
				t.Fatal("not actual chain2 finite group first", group, e)
			}
			earlier := checkpoint.MustEncode(checkpoint.Block(1000+end, 1, end, true))
			if earlier >= group.Checkpoint {
				t.Fatal("control does not distinguish a global checkpoint sort", earlier, group.Checkpoint)
			}
			if blockCalls[1] != 0 || blockCalls[2] != end || logCalls != 0 {
				t.Fatal("unexpected handler prefix while held", blockCalls, logCalls)
			}
			releaseGate()
			releaseCallback()
			select {
			case out := <-done:
				if !errors.Is(out.err, finiteStop) || out.result.Completed || len(out.result.Groups) != 1 || out.result.Groups[0].ChainID != 2 {
					t.Fatal(out)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("controlled stop did not cancel/join extraction")
			}
			if got := finiteBalances(t, r); !reflect.DeepEqual(got, map[string]string{"chain2": "4"}) {
				t.Fatal("business prefix after completion-ordered stop", got)
			}
			// Run joined the owned client workers above. A canceled HTTP client can
			// return before httptest's remote handler defer, so observe that separate
			// provider-side lifetime explicitly rather than sleep or weaken zero.
			drain := time.NewTimer(15 * time.Second)
			defer drain.Stop()
			for p.inflight.Load() != 0 {
				select {
				case <-p.finished:
				case <-drain.C:
					t.Fatal("completion-order HTTP handlers did not drain")
				}
			}
			if p.inflight.Load() != 0 {
				t.Fatal("completion-order RPC worker survived stop")
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}

// This workload creates its zero-valued accounts in real atomic setup callbacks,
// not in fixture SQL. Therefore either chain's first resumed group must see an
// existing row, independently of extraction completion order.
func TestFiniteHistoricalRunnerResumeCompleteCache(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			const end = 8
			p := newFiniteRPC(t, end)
			setups := []uint64{}
			seen := map[uint64][]uint64{}
			setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				setups = append(setups, id)
				_, e := a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("chain%d", id), "balance": big.NewInt(0)}, store.Conflict{})
				return e
			})
			handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				n, e := finiteNumber(a)
				if e != nil {
					return e
				}
				id := finiteChain(a)
				seen[id] = append(seen[id], n)
				if e := finiteIncrement(ctx, a); e != nil {
					return e
				}
				// This workload explicitly flushes through the public database API
				// inside the same business transaction. A real backend constraint error
				// therefore crosses Database.capture, not an invented test classifier.
				return a.Context.DB.Flush(ctx, "account")
			})
			build := finiteBuild(t, p, end, true, handler, setup)
			f := newFiniteFixture(t, backend, build)
			f.options.Owned.Cache = store.CacheOptions{Complete: true}
			waits := []time.Duration{}
			f.options.RetryWait = func(ctx context.Context, d time.Duration) error {
				waits = append(waits, d)
				return ctx.Err()
			}
			f.options.OnCommitted = func(context.Context, GroupResult) error { return finiteStop }
			r := f.start(t)
			first, e := r.Run(context.Background())
			if !errors.Is(e, finiteStop) || first.Completed || first.Resumed || len(first.Groups) != 1 {
				t.Fatal("fresh complete-cache prefix", first, e)
			}
			group := first.Groups[0]
			if !group.Applied || group.Events == 0 || group.Attempts != 1 || !reflect.DeepEqual(setups, []uint64{1, 2}) || len(waits) != 0 {
				t.Fatal("fresh atomic setup/group", group, setups, waits)
			}
			prefix, e := r.owner.HistoricalSnapshot(context.Background())
			if e != nil || !prefix.Initialized || len(prefix.Chains) != 2 {
				t.Fatal(prefix, e)
			}
			for _, chain := range prefix.Chains {
				want := checkpoint.MustEncode(checkpoint.Block(1001, chain.ChainID, 1, false))
				if chain.ChainID == group.ChainID {
					want = group.Checkpoint
				}
				if chain.Cursor != want || chain.LatestCheckpoint != want || chain.SafeCheckpoint != want || chain.FinalizedCheckpoint != want {
					t.Fatal("durable first-group checkpoint", chain, want, group)
				}
			}
			prefixRows := finiteBalances(t, r)
			wantPrefixRows := map[string]string{"chain1": "0", "chain2": "0"}
			wantPrefixRows[fmt.Sprintf("chain%d", group.ChainID)] = strconv.Itoa(group.Events)
			if !reflect.DeepEqual(prefixRows, wantPrefixRows) {
				t.Fatal("real setup and committed-prefix rows", prefixRows, wantPrefixRows)
			}
			prefixSetups := append([]uint64(nil), setups...)
			prefixCompleted := r.owner.Service().Metrics().Completed["Blocks:block"]
			t.Logf("resume complete-cache prefix: group=%+v snapshot=%+v rows=%v setups=%v completed=%d", group, prefix, prefixRows, setups, prefixCompleted)
			if e = r.Close(context.Background()); e != nil {
				t.Fatal("prefix close", e)
			}
			f.options.OnCommitted = nil
			r = f.start(t)
			reopened, e := r.owner.HistoricalSnapshot(context.Background())
			if e != nil || !reflect.DeepEqual(reopened, prefix) || !reflect.DeepEqual(setups, prefixSetups) {
				t.Fatal("reopened durable prefix/setup", reopened, prefix, setups, e)
			}
			t.Logf("resume complete-cache reopened: snapshot=%+v setups=%v", reopened, setups)
			result, runErr := r.Run(context.Background())
			if runErr != nil {
				after, snapshotErr := r.owner.HistoricalSnapshot(context.Background())
				if snapshotErr != nil {
					t.Fatal("failed resume snapshot unreadable", runErr, snapshotErr)
				}
				afterRows := finiteBalances(t, r)
				prefixUnchanged := reflect.DeepEqual(after, prefix) && reflect.DeepEqual(afterRows, prefixRows)
				setupUnchanged := reflect.DeepEqual(setups, prefixSetups)
				var classified *indexing.DatabaseError
				var pg *pgconn.PgError
				code, constraint, observation := "", "", "unrecognized"
				if backend == "postgres" && errors.As(runErr, &pg) {
					code, constraint, observation = pg.Code, pg.ConstraintName, "pgconn_structured"
				} else if backend == "pglite" && errors.As(runErr, &classified) {
					// The current PGlite bridge retains only Message and Code, rendered
					// into Cause.Error(). These are parsed actual text, not typed metadata.
					parts := regexp.MustCompile(`^duplicate key value violates unique constraint "([^"]+)" \(([0-9A-Z]{5})\)$`).FindStringSubmatch(classified.Cause.Error())
					if len(parts) == 3 {
						code, constraint, observation = parts[2], parts[1], "pglite_cause_text"
					}
				}
				if errors.As(runErr, &classified) && classified.Name == "UniqueConstraintError" && code == "23505" && constraint == "account_pkey" && prefixUnchanged && setupUnchanged && result.Resumed && !result.Completed && len(result.Groups) == 0 && len(waits) == 0 {
					t.Fatalf("resume complete-cache conflict: sqlstate=%s constraint=%s prefix_unchanged=%t resumed=%t committed_groups=%d setup_unchanged=%t backend=%s observation=%s waits=%v snapshot=%+v rows=%v setups=%v error=%v", code, constraint, prefixUnchanged, result.Resumed, len(result.Groups), setupUnchanged, backend, observation, waits, after, afterRows, setups, runErr)
				}
				t.Fatal("resume failed without exact untouched-prefix SQL conflict", result, runErr, code, constraint, observation, after, afterRows, setups, waits)
			}
			if !result.Completed || !result.Resumed || !reflect.DeepEqual(setups, prefixSetups) || len(waits) != 0 {
				t.Fatal("complete-cache resume", result, setups, waits)
			}
			totals := map[uint64]int{group.ChainID: group.Events}
			for _, g := range result.Groups {
				totals[g.ChainID] += g.Events
				if !g.Applied || g.Attempts != 1 {
					t.Fatal("resume replayed group", g)
				}
			}
			if !reflect.DeepEqual(totals, map[uint64]int{1: end, 2: end}) {
				t.Fatal("complete-cache event totals", totals)
			}
			for _, id := range []uint64{1, 2} {
				if !reflect.DeepEqual(seen[id], []uint64{1, 2, 3, 4, 5, 6, 7, 8}) {
					t.Fatal("complete-cache missing or duplicate dispatch", id, seen[id])
				}
			}
			rows := finiteBalances(t, r)
			if !reflect.DeepEqual(rows, map[string]string{"chain1": "8", "chain2": "8"}) || prefixCompleted+r.owner.Service().Metrics().Completed["Blocks:block"] != 2*end {
				t.Fatal("complete-cache final rows/completions", rows, prefixCompleted, r.owner.Service().Metrics().Completed)
			}
			finiteAssertEnds(t, r, end)
			final, e := r.owner.HistoricalSnapshot(context.Background())
			if e != nil || p.inflight.Load() != 0 {
				t.Fatal("resume final snapshot/workers", e, p.inflight.Load())
			}
			t.Logf("resume complete-cache final: snapshot=%+v rows=%v setups=%v event_totals=%v", final, rows, setups, totals)
		})
	}
}

// These supplemental logical controls need no database or network. Mixed-tree
// cases are counterexamples to the prior errors.Is-any-branch classifier.
type finiteCloseIsAlias struct{}

func (finiteCloseIsAlias) Error() string        { return "not a closed-connection leaf" }
func (finiteCloseIsAlias) Is(target error) bool { return target == sql.ErrConnDone }

type finiteCloseManyError struct{ children []error }

func (finiteCloseManyError) Error() string     { return "custom multiple unwrap" }
func (e finiteCloseManyError) Unwrap() []error { return e.children }

type finiteCloseLinkError struct{ next error }

func (*finiteCloseLinkError) Error() string   { return "custom single unwrap" }
func (e *finiteCloseLinkError) Unwrap() error { return e.next }

func TestFiniteHistoricalRunnerCloseErrorScope(t *testing.T) {
	arbitrary := errors.New("unrelated close failure")
	cycle := &finiteCloseLinkError{}
	cycle.next = cycle
	var tooDeep error = sql.ErrConnDone
	for i := 0; i < 65; i++ {
		tooDeep = fmt.Errorf("nested: %w", tooDeep)
	}
	cases := []struct {
		name            string
		err             error
		allClosedLeaves bool
	}{
		{"nil", nil, true},
		{"connection_done", sql.ErrConnDone, true},
		{"bad_connection", driver.ErrBadConn, true},
		{"wrapped_connection", fmt.Errorf("owner: %w", sql.ErrConnDone), true},
		{"both_allowed", errors.Join(sql.ErrConnDone, driver.ErrBadConn), true},
		{"wrapped_both_allowed", fmt.Errorf("owner: %w", errors.Join(fmt.Errorf("business: %w", sql.ErrConnDone), driver.ErrBadConn)), true},
		{"nil_and_allowed", finiteCloseManyError{[]error{nil, sql.ErrConnDone, nil}}, true},
		{"wrapped_mixed_canceled", fmt.Errorf("owner: %w", errors.Join(sql.ErrConnDone, context.Canceled)), false},
		{"wrapped_mixed_deadline", fmt.Errorf("owner: %w", errors.Join(driver.ErrBadConn, context.DeadlineExceeded)), false},
		{"wrapped_mixed_arbitrary", fmt.Errorf("owner: %w", errors.Join(sql.ErrConnDone, arbitrary)), false},
		{"deep_mixed_tree", errors.Join(sql.ErrConnDone, fmt.Errorf("nested: %w", errors.Join(driver.ErrBadConn, context.Canceled))), false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"arbitrary", arbitrary, false},
		{"empty_many", finiteCloseManyError{}, false},
		{"nil_only_many", finiteCloseManyError{[]error{nil, nil}}, false},
		{"nil_single", &finiteCloseLinkError{}, false},
		{"spoofed_is", finiteCloseIsAlias{}, false},
		{"cycle", cycle, false},
		{"overdeep", tooDeep, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := finiteClosedOnly(tc.err); got != tc.allClosedLeaves {
				t.Fatalf("closed-leaf classification got=%t want=%t error=%v", got, tc.allClosedLeaves, tc.err)
			}
			original, reopened := &Runner{}, &Runner{}
			scenarios := []struct {
				name           string
				scope          *finiteCloseScope
				marker, owner  *Runner
				mayAllowClosed bool
			}{
				{"ordinary_strict", nil, nil, original, false},
				{"postgres_before_trigger", &finiteCloseScope{runner: original, postgres: true}, nil, original, false},
				{"postgres_after_real_trigger_marker", &finiteCloseScope{runner: original, postgres: true}, original, original, true},
				{"pglite_even_after_marker", &finiteCloseScope{runner: original}, original, original, false},
				{"different_owner_marker", &finiteCloseScope{runner: original, postgres: true}, reopened, original, false},
				{"marked_old_scope_cannot_cover_reopen", &finiteCloseScope{runner: original, postgres: true}, original, reopened, false},
				{"new_reopened_scope_is_strict", &finiteCloseScope{runner: reopened, postgres: true}, nil, reopened, false},
			}
			for _, scenario := range scenarios {
				t.Run(scenario.name, func(t *testing.T) {
					// Pure marker logic only: these cases do not claim to terminate a
					// backend or perform cancellation; integration sites own that proof.
					scenario.scope.markTriggered(scenario.marker)
					want := tc.err == nil || (scenario.mayAllowClosed && tc.allClosedLeaves)
					if got := scenario.scope.accepts(scenario.owner, tc.err); got != want {
						t.Fatalf("per-runner close allowance got=%t want=%t error=%v", got, want, tc.err)
					}
				})
			}
		})
	}
}
