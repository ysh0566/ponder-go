package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
)

// This probe deliberately keeps SQL admission unavailable. It inspects the real
// adapter queue before any worker can touch a backend, rather than waiting for a
// goroutine-start handshake and calling that admission.
func TestSourceRPCAsyncAdmission(t *testing.T) {
	s := &Store{}
	cache := &ProtocolCache{s: s, namespace: "sync"}
	adapter := cache.AsyncSourceRPCRequestCache(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	value := json.RawMessage(`"0xABC"`)
	future := adapter.BeginCachePut(context.Background(), `[1,"eth_call",[{"to":"0xABC","data":"0x1234"},"0x2"]]`, value)
	copy(value, `"0xBAD"`)
	adapter.state.mu.Lock()
	if len(adapter.state.pending) != 1 {
		t.Fatal("Begin returned before real owned admission")
	}
	for job := range adapter.state.pending {
		hash, err := SourceRPCRequestHash(map[string]any{"method": "eth_call", "params": []any{map[string]any{"to": "0xABC", "data": "0x1234"}, "0x2"}})
		if err != nil || !strings.Contains(job.query, "ON CONFLICT(chain_id,request_hash) DO NOTHING") || len(job.args) != 4 || job.args[0] != uint64(1) || job.args[1] != hash || job.args[2] != uint64(2) || job.args[3] != `"0xABC"` {
			t.Fatalf("prepared immutable SQL payload = %q %#v, %v", job.query, job.args, err)
		}
	}
	adapter.state.mu.Unlock()
	select {
	case err := <-future:
		t.Fatalf("write completed without available Store: %v", err)
	default:
	}
	// Copying the public handle must not split queue/cancellation ownership.
	copied := *adapter
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := copied.Close(ctx); err != nil {
		t.Fatal("Close waited on callback-owned Store lock:", err)
	}
	if err := <-future; !errors.Is(err, context.Canceled) {
		t.Fatalf("future = %v", err)
	}
	if _, open := <-future; open {
		t.Fatal("future not closed")
	}
	if err := <-adapter.BeginCachePut(ctx, `[1,"eth_chainId",[]]`, json.RawMessage(`"0x1"`)); err == nil {
		t.Fatal("copy close did not seal original handle")
	}
}

func TestSourceRPCAsyncValidation(t *testing.T) {
	cache := &ProtocolCache{s: &Store{}, namespace: "sync"}
	adapter := cache.AsyncSourceRPCRequestCache(1)
	for _, tc := range []struct{ key, value string }{
		{`{}`, `"0x1"`},
		{`[2,"eth_chainId",[]]`, `"0x1"`},
		{`[1,"eth_chainId",[]]`, `invalid`},
		{`[1,"eth_getBalance",["0x1","0x20000000000000"]]`, `"0x1"`},
	} {
		future := adapter.BeginCachePut(context.Background(), tc.key, json.RawMessage(tc.value))
		select {
		case err := <-future:
			if err == nil {
				t.Fatal("invalid input accepted", tc)
			}
		default:
			t.Fatal("validation did not finish synchronously", tc)
		}
	}
	adapter.state.mu.Lock()
	defer adapter.state.mu.Unlock()
	if len(adapter.state.pending) != 0 || adapter.state.running {
		t.Fatal("invalid input entered write queue")
	}
}

func TestSourceRPCAsyncCallerCancellation(t *testing.T) {
	s := &Store{}
	cache := &ProtocolCache{s: s, namespace: "sync"}
	adapter := cache.AsyncSourceRPCRequestCache(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	future := adapter.BeginCachePut(ctx, `[1,"eth_chainId",[]]`, json.RawMessage(`"0x1"`))
	cancel()
	select {
	case err := <-future:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller cancellation waited for Store lock")
	}
	if err := adapter.WaitCacheWrites(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-adapter.BeginCachePut(context.Background(), `[1,"eth_chainId",[]]`, json.RawMessage(`"0x1"`)); err == nil {
		t.Fatal("closed protocol cache accepted write")
	}
	if err := adapter.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type asyncRPCGate struct {
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
}

func (g *asyncRPCGate) unblock() { g.releaseOnce.Do(func() { close(g.release) }) }

type asyncRPCGateBackend struct {
	Backend
	mu   sync.Mutex
	gate *asyncRPCGate
}

func (b *asyncRPCGateBackend) setGate(g *asyncRPCGate) {
	b.mu.Lock()
	b.gate = g
	b.mu.Unlock()
}
func (b *asyncRPCGateBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &asyncRPCGateTx{Transaction: tx, backend: b}, nil
}

type asyncRPCGateTx struct {
	Transaction
	backend *asyncRPCGateBackend
}

func (t *asyncRPCGateTx) Exec(ctx context.Context, query string, args ...any) error {
	t.backend.mu.Lock()
	gate := t.backend.gate
	t.backend.mu.Unlock()
	if gate != nil && strings.HasPrefix(query, "INSERT INTO ") && strings.Contains(query, `"rpc_request_results"`) {
		gate.once.Do(func() { close(gate.entered) })
		// Model the documented already-inflight backend limit: cancellation
		// cannot forcibly release this operation; ownership must be retained.
		<-gate.release
	}
	return t.Transaction.Exec(ctx, query, args...)
}
func awaitAsyncGate(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("real SQL execution did not reach gate")
	}
}

func TestSourceRPCAsyncBackends(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			backend := &asyncRPCGateBackend{Backend: sourceBackend(t, ctx, dialect)}
			db, err := New(ctx, backend, sourceActionsModel(), fmt.Sprintf("ponder_go_async_rpc_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			cache, err := db.EnableProtocolCache(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema.Quote(cache.Namespace())+" CASCADE") })
			key := func(n int) string { return fmt.Sprintf(`[1,"eth_getBalance",["0xabc","0x%x"]]`, n) }

			t.Run("prepared_snapshot_and_real_rows", func(t *testing.T) {
				adapter := cache.AsyncSourceRPCRequestCache(1)
				defer adapter.Close(ctx)
				db.mu.Lock()
				value := json.RawMessage(`"0xAB"`)
				first := adapter.BeginCachePut(ctx, key(1), value)
				second := adapter.BeginCachePut(ctx, key(2), value)
				copy(value, `"0xFF"`)
				adapter.state.mu.Lock()
				pending := len(adapter.state.pending)
				adapter.state.mu.Unlock()
				db.mu.Unlock()
				if pending != 2 {
					t.Fatal("admitted jobs", pending)
				}
				for _, future := range []<-chan error{first, second} {
					if err := <-future; err != nil {
						t.Fatal(err)
					}
				}
				if err := adapter.WaitCacheWrites(ctx); err != nil {
					t.Fatal(err)
				}
				for n := 1; n <= 2; n++ {
					raw, found, err := adapter.CacheGet(ctx, key(n))
					if err != nil || !found || string(raw) != `"0xAB"` {
						t.Fatalf("row %d: %s %v %v", n, raw, found, err)
					}
				}
			})

			t.Run("delayed_write_native_read_visibility", func(t *testing.T) {
				adapter := cache.AsyncSourceRPCRequestCache(1)
				defer adapter.Close(ctx)
				gate := &asyncRPCGate{entered: make(chan struct{}), release: make(chan struct{})}
				backend.setGate(gate)
				defer gate.unblock()
				future := adapter.BeginCachePut(ctx, key(3), json.RawMessage(`"0x3"`))
				awaitAsyncGate(t, gate.entered)
				if db.mu.TryLock() {
					db.mu.Unlock()
					t.Fatal("SQL did not hold native Store transaction lock")
				}
				select {
				case err := <-future:
					t.Fatalf("delayed write completed: %v", err)
				default:
				}
				read := make(chan error, 1)
				go func() {
					raw, found, err := adapter.CacheGet(ctx, key(3))
					if err == nil && (!found || string(raw) != `"0x3"`) {
						err = fmt.Errorf("read after serialized write = %s %v", raw, found)
					}
					read <- err
				}()
				select {
				case err := <-read:
					t.Fatalf("read bypassed executing native transaction: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
				gate.unblock()
				if err := <-future; err != nil {
					t.Fatal(err)
				}
				if err := <-read; err != nil {
					t.Fatal(err)
				}
				backend.setGate(nil)
			})

			t.Run("close_timeout_retains_ownership_and_rolls_back", func(t *testing.T) {
				adapter := cache.AsyncSourceRPCRequestCache(1)
				gate := &asyncRPCGate{entered: make(chan struct{}), release: make(chan struct{})}
				backend.setGate(gate)
				defer gate.unblock()
				future := adapter.BeginCachePut(ctx, key(4), json.RawMessage(`"0x4"`))
				awaitAsyncGate(t, gate.entered)
				deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
				if err := adapter.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("Close before backend release", err)
				}
				adapter.state.mu.Lock()
				pending := len(adapter.state.pending)
				adapter.state.mu.Unlock()
				if pending != 1 {
					t.Fatal("Close lost owned future", pending)
				}
				gate.unblock()
				if err := <-future; !errors.Is(err, context.Canceled) {
					t.Fatal("canceled SQL", err)
				}
				if err := adapter.Close(ctx); err != nil {
					t.Fatal(err)
				}
				backend.setGate(nil)
				_, found, err := cache.SourceRPCRequestCache(1).CacheGet(ctx, key(4))
				if err != nil || found {
					t.Fatal("canceled write persisted", found, err)
				}
			})

			t.Run("bound_adapters_remain_synchronous_and_rollback", func(t *testing.T) {
				ss, err := cache.SourceSyncStore(16000)
				if err != nil {
					t.Fatal(err)
				}
				marker := errors.New("rollback bound synchronous cache")
				err = ss.WithTransaction(ctx, func(bound *SourceSyncStore) error {
					adapter := bound.RPCRequestCache(1)
					if _, ok := any(adapter).(rpc.AsyncCache); ok {
						t.Fatal("bound source adapter unexpectedly implements async admission")
					}
					if err := adapter.CachePut(ctx, key(5), json.RawMessage(`"0x5"`)); err != nil {
						return err
					}
					return marker
				})
				if !errors.Is(err, marker) {
					t.Fatal(err)
				}
				err = db.Apply(ctx, event("async-bound-rollback", 1, 1), func(w *Writer) error {
					adapter := w.SourceRPCRequestCache()
					if _, ok := any(adapter).(rpc.AsyncCache); ok {
						t.Fatal("Writer adapter unexpectedly implements async admission")
					}
					if err := adapter.CachePut(ctx, key(6), json.RawMessage(`"0x6"`)); err != nil {
						return err
					}
					return marker
				})
				if !errors.Is(err, marker) {
					t.Fatal(err)
				}
				for _, n := range []int{5, 6} {
					_, found, err := cache.SourceRPCRequestCache(1).CacheGet(ctx, key(n))
					if err != nil || found {
						t.Fatal("bound cache escaped rollback", n, found, err)
					}
				}
			})

			t.Run("late_real_sql_error", func(t *testing.T) {
				adapter := cache.AsyncSourceRPCRequestCache(1)
				defer adapter.Close(ctx)
				db.mu.Lock()
				future := adapter.BeginCachePut(ctx, key(7), json.RawMessage(`"0x7"`))
				if err := backend.Exec(ctx, "DROP TABLE "+cache.table("rpc_request_results")); err != nil {
					db.mu.Unlock()
					t.Fatal(err)
				}
				db.mu.Unlock()
				if err := <-future; err == nil || !strings.Contains(err.Error(), "does not exist") {
					t.Fatal("late SQL failure", err)
				}
				if err := adapter.WaitCacheWrites(ctx); err != nil {
					t.Fatal("late failure lost queue completion", err)
				}
			})
		})
	}
}
