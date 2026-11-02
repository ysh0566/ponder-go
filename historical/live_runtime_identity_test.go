package historical

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type runtimeRPCState struct {
	mu           sync.Mutex
	active       bool
	fork         bool
	counts       map[string]int
	mutate       func(uint64, uint64, int, bool, bool, map[string]any)
	latest       map[uint64]uint64
	balanceCalls int
}

func controlledRuntimeRPC(t *testing.T, s *runtimeRPCState) *finiteRPC {
	t.Helper()
	s.counts = map[string]int{}
	p := &finiteRPC{}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     json.RawMessage
			Method string
			Params []any
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		id, _ := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/"), 10, 64)
		var out any
		var err error
		s.mu.Lock()
		defer s.mu.Unlock()
		switch q.Method {
		case "eth_getBlockByNumber":
			n := s.latest[id]
			if q.Params[0] != "latest" {
				n, err = strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(q.Params[0]), "0x"), 16, 64)
			}
			b := finiteBlock(id, n)
			key := fmt.Sprintf("%d:%d", id, n)
			if s.active && len(q.Params) > 1 && q.Params[1] == true {
				s.counts[key]++
			}
			if s.mutate != nil {
				s.mutate(id, n, s.counts[key], s.active, s.fork, b)
			}
			out = b
		case "eth_getBalance":
			out = "0x2a"
			if s.fork && id == 1 && len(q.Params) > 1 && q.Params[1] == "0x3" {
				out = "0x63"
				s.balanceCalls++
			}
		case "eth_getLogs":
			out = []any{}
		default:
			err = fmt.Errorf("unexpected controlled RPC %s", q.Method)
		}
		response := map[string]any{"jsonrpc": "2.0", "id": q.ID}
		if err != nil {
			response["error"] = map[string]any{"code": -32000, "message": err.Error()}
		} else {
			response["result"] = out
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(p.server.Close)
	return p
}
func TestRuntimeAcquiredIdentity(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"two_fetches_change_fork", "finalized_identity_changes"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				root := os.Getenv("PONDER_M45_TEST_ROOT")
				if root == "" {
					t.Fatal("retained root required")
				}
				t.Setenv("PONDER_M44_TEST_ROOT", root)
				s := &runtimeRPCState{latest: map[uint64]uint64{1: 3, 2: 3}}
				s.mutate = func(id, n uint64, count int, active, fork bool, b map[string]any) {
					if !active || id != 1 {
						return
					}
					if (kind == "two_fetches_change_fork" && n == 1 && count >= 2) || (kind == "finalized_identity_changes" && n == 2) {
						b["hash"] = fmt.Sprintf("0x%064x", 999000+n)
					}
				}
				p := controlledRuntimeRPC(t, s)
				calls := 0
				b := runtimeBuild(t, p, 3, 1, indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					if finiteChain(a) == 1 {
						calls++
					}
					return finiteIncrement(ctx, a)
				}))
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				r := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "runtime-identity-v1", OpenID: "identity-1", Requesters: runtimeFactory(p, &epochs)})
				s.mu.Lock()
				s.active = true
				s.mu.Unlock()
				out, err := r.Run(context.Background())
				if err == nil || out.Ready || out.Completed {
					t.Fatalf("accepted fork mismatch result=%+v err=%v", out, err)
				}
				if kind == "two_fetches_change_fork" {
					if !strings.Contains(err.Error(), "acquired header differs") || calls != 0 {
						t.Fatalf("mixed identity reached handler calls=%d err=%v", calls, err)
					}
				} else {
					if !strings.Contains(err.Error(), "finalized identity changed") || calls != 1 {
						t.Fatalf("different finalized branch reached handler calls=%d err=%v", calls, err)
					}
				}
				if ready, e := r.Ready(context.Background()); e != nil || ready {
					t.Fatalf("failed run ready=%v err=%v", ready, e)
				}
			})
		}
	}
}
func TestRuntimeHistoryBeforeLive(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			s := &runtimeRPCState{latest: map[uint64]uint64{1: 5, 2: 6}}
			s.mutate = func(id, n uint64, _ int, _, _ bool, b map[string]any) {
				if id == 2 {
					b["timestamp"] = fmt.Sprintf("0x%x", 2000+n)
				}
			}
			p := controlledRuntimeRPC(t, s)
			activated := false
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				if _, history := a.Event.Property("block"); !history {
					if !activated {
						return fmt.Errorf("live handler ran before activation")
					}
					row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "chain2"})
					if err != nil {
						return err
					}
					if row == nil || fmt.Sprint(row["balance"]) != "5" {
						return fmt.Errorf("live observed incomplete other-chain history: %v", row)
					}
				}
				return finiteIncrement(ctx, a)
			})
			b := runtimeBuild(t, p, 5, 1, h)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			r := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "runtime-phase-v1", OpenID: "phase-1", Requesters: runtimeFactory(p, &epochs), OnActivated: func(context.Context) error { activated = true; return nil }})
			out, err := r.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !out.Ready || !out.Completed || out.HistoricalBlocks != 9 || out.LiveBlocks != 1 {
				t.Fatal(out)
			}
		})
	}
}
func TestRuntimeForkRecoveryRefetch(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			s := &runtimeRPCState{latest: map[uint64]uint64{1: 3, 2: 3}}
			s.mutate = func(id, n uint64, _ int, _, fork bool, b map[string]any) {
				if fork && id == 1 && n == 3 {
					b["hash"] = fmt.Sprintf("0x%064x", 999003)
					b["timestamp"] = "0x3ec"
				}
			}
			p := controlledRuntimeRPC(t, s)
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				balance, err := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
				if err != nil {
					return err
				}
				key := store.Row{"id": fmt.Sprintf("chain%d", finiteChain(a))}
				row, err := a.Context.DB.Find(ctx, "account", key)
				if err != nil {
					return err
				}
				if row == nil {
					_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": key["id"], "balance": new(big.Int).Set(balance)}, store.Conflict{})
				} else {
					_, err = a.Context.DB.Update(ctx, "account", key, store.Row{"balance": new(big.Int).Set(balance)})
				}
				return err
			})
			b := runtimeBuild(t, p, 3, 1, h)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "runtime-fork-v1", OpenID: "fork-1", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Completed {
				t.Fatalf("first run %+v %v", out, err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			s.fork = true
			s.mu.Unlock()
			o.OpenID = "fork-2"
			r = runtimeOpen(t, backend, o)
			out, err = r.Run(context.Background())
			if err != nil || !out.Completed || !out.Recovered || out.LiveBlocks != 2 {
				t.Fatalf("fork replay %+v %v", out, err)
			}
			c, err := r.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if c.Cursors[1].Hash != fmt.Sprintf("0x%064x", 999003) || len(c.ReplayTargets) != 0 {
				t.Fatalf("fork replay did not replace public head %+v", c)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			fresh := s.balanceCalls
			s.mu.Unlock()
			if fresh != 1 {
				t.Fatalf("numeric orphan RPC answer reused; fresh fork calls=%d", fresh)
			}
			managedInspect(t, f, func(b store.Backend) {
				rows := managedRows(t, b, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || rows[0]["balance"] != "99" || rows[1]["balance"] != "42" {
					t.Fatalf("fork values not refetched: %v", rows)
				}
			})
		})
	}
}

func TestRuntimeFinalizedAncestorBeforeBusiness(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			s := &runtimeRPCState{latest: map[uint64]uint64{1: 6, 2: 6}}
			s.mutate = func(id, n uint64, _ int, active, _ bool, b map[string]any) {
				if active && id == 1 && (n == 1 || n == 2) {
					b["hash"] = fmt.Sprintf("0x%064x", 999000+n)
					if n == 2 {
						b["parentHash"] = fmt.Sprintf("0x%064x", 999001)
					}
				}
			}
			p := controlledRuntimeRPC(t, s)
			calls := 0
			b := runtimeBuild(t, p, 2, 1, indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error { calls++; return finiteIncrement(ctx, a) }))
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			r := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "ancestor-v1", OpenID: "ancestor-1", Requesters: runtimeFactory(p, &epochs)})
			s.mu.Lock()
			s.active = true
			s.mu.Unlock()
			out, err := r.Run(context.Background())
			if err == nil || out.Ready || calls != 0 {
				t.Fatalf("competing finalized ancestors reached business: %+v err=%v calls=%d", out, err, calls)
			}
		})
	}
}
