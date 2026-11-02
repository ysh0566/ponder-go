package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type continuousRPC struct {
	mu          sync.Mutex
	latest      map[uint64]uint64
	canonical   map[uint64]map[uint64]map[string]any
	byHash      map[string]map[string]any
	forked      map[string]bool
	version     uint64
	failByHash  int
	failLatest  map[uint64]int
	latestCalls map[uint64]int
	gate        func(context.Context, uint64, string, []any) error
	p           *finiteRPC
}

func newContinuousRPC(t *testing.T, latest uint64) *continuousRPC {
	s := &continuousRPC{latest: map[uint64]uint64{1: latest, 2: latest}, canonical: map[uint64]map[uint64]map[string]any{}, byHash: map[string]map[string]any{}, forked: map[string]bool{}, failLatest: map[uint64]int{}, latestCalls: map[uint64]int{}}
	for _, id := range []uint64{1, 2} {
		s.canonical[id] = map[uint64]map[string]any{}
		for n := uint64(0); n <= 32; n++ {
			b := finiteBlock(id, n)
			s.canonical[id][n] = b
			s.byHash[fmt.Sprint(b["hash"])] = b
		}
	}
	p := &finiteRPC{}
	s.p = p
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     json.RawMessage
			Method string
			Params []any
		}
		if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		id, _ := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/"), 10, 64)
		s.mu.Lock()
		gate := s.gate
		s.mu.Unlock()
		var e error
		if gate != nil {
			e = gate(r.Context(), id, q.Method, q.Params)
		}
		s.mu.Lock()
		var out any
		if e == nil {
			switch q.Method {
			case "eth_getBlockByNumber":
				n := s.latest[id]
				if q.Params[0] == "latest" {
					s.latestCalls[id]++
					if s.failLatest[id] > 0 {
						s.failLatest[id]--
						e = errors.New("controlled latest acquisition failure")
					}
				} else {
					n, e = strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(q.Params[0]), "0x"), 16, 64)
				}
				out = s.canonical[id][n]
			case "eth_getBlockByHash":
				if s.failByHash > 0 {
					s.failByHash--
					e = errors.New("controlled retained-header acquisition failure")
				}
				out = s.byHash[fmt.Sprint(q.Params[0])]
				if out == nil {
					e = fmt.Errorf("unknown retained fixture hash %v", q.Params[0])
				}
			case "eth_getBalance":
				n, _ := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(q.Params[1]), "0x"), 16, 64)
				out = "0x2a"
				if b := s.canonical[id][n]; b != nil && s.forked[fmt.Sprint(b["hash"])] {
					out = "0x63"
				}
			case "eth_getLogs":
				out = []any{}
			case "ponder_test_fork":
				s.forkLocked(1, 3)
				s.latest[1], s.latest[2] = 4, 4
				out = true
			default:
				e = fmt.Errorf("unexpected continuous RPC %s", q.Method)
			}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": q.ID}
		if e != nil {
			response["error"] = map[string]any{"code": -32000, "message": e.Error()}
		} else {
			response["result"] = out
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		s.mu.Unlock()
	}))
	t.Cleanup(p.server.Close)
	return s
}
func (s *continuousRPC) forkLocked(id, from uint64) {
	s.version++
	for n := from; n <= 32; n++ {
		b := finiteBlock(id, n)
		b["hash"] = fmt.Sprintf("0x%064x", id*100000000+s.version*10000+n)
		b["parentHash"] = s.canonical[id][n-1]["hash"]
		if n == from {
			b["timestamp"] = s.canonical[id][n-1]["timestamp"]
		}
		s.canonical[id][n] = b
		s.byHash[fmt.Sprint(b["hash"])] = b
		s.forked[fmt.Sprint(b["hash"])] = true
	}
}
func (s *continuousRPC) fork(id, from uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forkLocked(id, from)
}
func (s *continuousRPC) setLatest(n uint64) {
	s.mu.Lock()
	s.latest[1], s.latest[2] = n, n
	s.mu.Unlock()
}
func continuousNumber(a *indexing.HandlerArgs) (uint64, error) {
	if b, ok := a.Event.Property("block"); ok {
		v, e := b.Get("number")
		if e != nil {
			return 0, e
		}
		return uintQuantity(v)
	}
	return uintQuantity(a.Event.Get("block").(map[string]any)["number"])
}
func continuousRoot(t *testing.T) {
	t.Helper()
	root := os.Getenv("PONDER_M45_TEST_ROOT")
	if root == "" {
		t.Fatal("retained root required")
	}
	t.Setenv("PONDER_M44_TEST_ROOT", root)
}

type continuousSegment struct {
	id, n uint64
	code  uint64
}

func continuousFold(trace []continuousSegment) *big.Int {
	v := big.NewInt(0)
	for _, s := range trace {
		if s.code != 0 {
			v.Mul(v, big.NewInt(1000))
			v.Add(v, new(big.Int).SetUint64(s.code))
		}
	}
	return v
}

func TestRuntimeContinuousReorg(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"shared_rows", "empty_segments"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				continuousRoot(t)
				s := newContinuousRPC(t, 3)
				trace := []continuousSegment{}
				var pending uint64
				var r *RuntimeRunner
				step := 0
				pauses, reorgs, ready := 0, 0, 0
				var saved map[uint64]string
				handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					id := finiteChain(a)
					n, e := continuousNumber(a)
					if e != nil {
						return e
					}
					row, e := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
					if e != nil {
						return e
					}
					v := big.NewInt(0)
					if row != nil {
						var ok bool
						v, ok = new(big.Int).SetString(fmt.Sprint(row["balance"]), 10)
						if !ok {
							return fmt.Errorf("bad bigint")
						}
					}
					if v.Cmp(continuousFold(trace)) != 0 {
						return fmt.Errorf("global undo/replay differs before %d:%d: row=%v trace=%v", id, n, row, trace)
					}
					balance, e := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
					if e != nil {
						return e
					}
					s.mu.Lock()
					want := int64(42)
					if s.forked[fmt.Sprint(s.canonical[id][n]["hash"])] {
						want = 99
					}
					s.mu.Unlock()
					if balance.Int64() != want {
						return fmt.Errorf("orphaned numeric RPC cache survived reorg: chain%d:%d balance=%s want=%d", id, n, balance, want)
					}
					pending = id*100 + n*10 + 1
					if want == 99 {
						pending++
					}
					v.Mul(v, big.NewInt(1000))
					v.Add(v, new(big.Int).SetUint64(pending))
					if row == nil {
						_, e = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "balance": v}, store.Conflict{})
					} else {
						_, e = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"balance": v})
					}
					return e
				})
				interval := uint64(1)
				if kind == "empty_segments" {
					interval = 2
				}
				b := runtimeBuild(t, s.p, 6, interval, handler)
				for _, c := range b.Executable().Chains {
					c["reorgWindow"] = uint64(600)
				}
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-global-" + kind, OpenID: "continuous-global-1", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
					switch e.Type {
					case "block":
						cp, _ := checkpoint.Decode(e.Head.Checkpoint)
						trace = append(trace, continuousSegment{id: e.ChainID, n: cp.BlockNumber, code: pending})
						pending = 0
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						both := true
						for _, h := range c.Cursors {
							n, _ := checkpoint.Decode(h.Checkpoint)
							both = both && n.BlockNumber >= 3
						}
						if both && step == 0 {
							saved, err = r.PublicCheckpoints(ctx)
							if err != nil {
								return err
							}
							step = 1
							s.fork(1, 3)
						}
					case "paused":
						pauses++
						if yes, err := r.Ready(ctx); err != nil || yes {
							return fmt.Errorf("reorg exposed ready: %v %v", yes, err)
						}
						// Other-chain commits before this pause are legitimate. Bind
						// the exact public snapshot immediately before coordination.
						var err error
						saved, err = r.PublicCheckpoints(ctx)
						if err != nil {
							return err
						}
					case "reorg":
						reorgs++
						a, _ := checkpoint.Decode(e.Head.Checkpoint)
						cut := len(trace)
						for i, x := range trace {
							if x.id == e.ChainID && x.n > a.BlockNumber {
								cut = i
								break
							}
						}
						trace = trace[:cut]
						public, err := r.PublicCheckpoints(ctx)
						if err != nil {
							return err
						}
						if !reflect.DeepEqual(public, saved) {
							return fmt.Errorf("public heads moved before replay commit: %v saved=%v", public, saved)
						}
						c, err := r.Control(ctx)
						if err != nil || len(c.ReplayTargets) == 0 || c.Serving {
							return fmt.Errorf("missing private replay guard %+v %v", c, err)
						}
					case "ready":
						ready++
						c, err := r.Control(ctx)
						if err != nil {
							return err
						}
						if c.Reorgs == 1 && step == 1 {
							saved, err = r.PublicCheckpoints(ctx)
							if err != nil {
								return err
							}
							step = 2
							s.fork(1, 2)
							s.setLatest(5)
						} else if c.Reorgs == 2 && step == 2 {
							step = 3
							s.setLatest(6)
						}
					}
					return nil
				}}}
				r = runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || reorgs != 2 || pauses != 2 || ready != 3 || step != 3 {
					t.Fatalf("actual reorg result=%+v reorg=%d paused=%d ready=%d step=%d trace=%v err=%v", out, reorgs, pauses, ready, step, trace, err)
				}
				c, err := r.Control(context.Background())
				if err != nil || c.Reorgs != 2 || len(c.ReplayTargets) != 0 || !c.Serving {
					t.Fatalf("final reorg control=%+v err=%v", c, err)
				}
				for _, h := range c.Cursors {
					cp, _ := checkpoint.Decode(h.Checkpoint)
					if cp.BlockNumber != 6 {
						t.Fatal(c)
					}
				}
				if len(trace) != 12 {
					t.Fatalf("empty/executed segments lost after global replay: %v", trace)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
					if len(rows) != 1 || fmt.Sprint(rows[0]["balance"]) != continuousFold(trace).String() {
						t.Fatalf("actual shared row=%v trace=%v", rows, trace)
					}
					segments := managedRows(t, db, "SELECT ordinal FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".\"_ponder_go_live_segments\"")
					if len(segments) != 12 {
						t.Fatalf("empty segments missing: %d", len(segments))
					}
				})
			})
		}
	}
}
