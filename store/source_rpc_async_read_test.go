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

// Inspect the genuine queue while the Store lock excludes all execution; an
// admission acknowledgment is the actual prepared operation, not worker startup.
func TestSourceRPCAsyncReadAdmission(t *testing.T) {
	s := &Store{}
	cache := &ProtocolCache{s: s, namespace: "sync"}
	adapter := cache.AsyncSourceRPCRequestCache(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := `[1,"eth_call",[{"to":"0xABC","data":"0x1234"},"0x2"]]`
	first := adapter.BeginCacheGet(context.Background(), key)
	second := adapter.BeginCacheGet(context.Background(), key)
	adapter.reads.mu.Lock()
	if len(adapter.reads.pending) != 2 {
		t.Fatal("read admission missing")
	}
	hash, err := SourceRPCRequestHash(map[string]any{"method": "eth_call", "params": []any{map[string]any{"to": "0xABC", "data": "0x1234"}, "0x2"}})
	if err != nil {
		t.Fatal(err)
	}
	for job := range adapter.reads.pending {
		if !strings.Contains(job.query, "WHERE chain_id=$1 AND request_hash IN ($2)") || len(job.args) != 2 || job.args[0] != uint64(1) || job.args[1] != hash {
			t.Fatal("unprepared read", job.query, job.args)
		}
	}
	adapter.reads.mu.Unlock()
	for _, future := range []<-chan rpc.CacheLookupResult{first, second} {
		select {
		case result := <-future:
			t.Fatal("read completed behind locked Store", result)
		default:
		}
	}
	copied := *adapter
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = copied.Close(ctx); err != nil {
		t.Fatal("Close failed to cancel Store-lock waits", err)
	}
	for _, future := range []<-chan rpc.CacheLookupResult{first, second} {
		result := <-future
		if !errors.Is(result.Err, context.Canceled) || result.Hit || result.Value != nil {
			t.Fatal("canceled read", result)
		}
		if _, open := <-future; open {
			t.Fatal("future not closed")
		}
	}
	if result := <-adapter.BeginCacheGet(ctx, key); result.Err == nil {
		t.Fatal("copied Close did not seal original")
	}
}
func TestSourceRPCAsyncReadValidation(t *testing.T) {
	adapter := (&ProtocolCache{s: &Store{}, namespace: "sync"}).AsyncSourceRPCRequestCache(1)
	for _, key := range []string{`{}`, `[2,"eth_chainId",[]]`, `[1,"eth_getBalance",["0x1","0x20000000000000"]]`, `[1,1,[]]`} {
		select {
		case result := <-adapter.BeginCacheGet(context.Background(), key):
			if result.Err == nil {
				t.Fatal("invalid read accepted", key)
			}
		default:
			t.Fatal("validation not synchronous", key)
		}
	}
	adapter.reads.mu.Lock()
	pending, running := len(adapter.reads.pending), adapter.reads.running
	adapter.reads.mu.Unlock()
	if pending != 0 || running {
		t.Fatal("invalid read entered real queue")
	}
	if err := adapter.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestSourceRPCAsyncReadCallerCancellation(t *testing.T) {
	s := &Store{}
	cache := &ProtocolCache{s: s, namespace: "sync"}
	adapter := cache.AsyncSourceRPCRequestCache(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	future := adapter.BeginCacheGet(ctx, `[1,"eth_chainId",[]]`)
	cancel()
	select {
	case result := <-future:
		if !errors.Is(result.Err, context.Canceled) {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for Store lock")
	}
	if err := adapter.WaitCacheReads(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if result := <-adapter.BeginCacheGet(context.Background(), `[1,"eth_chainId",[]]`); result.Err == nil {
		t.Fatal("closed protocol accepted read")
	}
	if err := adapter.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type asyncRPCReadGateBackend struct {
	Backend
	mu   sync.Mutex
	gate *asyncRPCGate
}

func (b *asyncRPCReadGateBackend) setGate(g *asyncRPCGate) { b.mu.Lock(); b.gate = g; b.mu.Unlock() }
func (b *asyncRPCReadGateBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &asyncRPCReadGateTx{Transaction: tx, backend: b}, nil
}

type asyncRPCReadGateTx struct {
	Transaction
	backend *asyncRPCReadGateBackend
}

func (t *asyncRPCReadGateTx) Query(ctx context.Context, query string, args ...any) (Result, error) {
	t.backend.mu.Lock()
	gate := t.backend.gate
	t.backend.mu.Unlock()
	if gate != nil && strings.HasPrefix(query, "SELECT request_hash,result FROM ") && strings.Contains(query, `"rpc_request_results"`) {
		gate.once.Do(func() { close(gate.entered) })
		<-gate.release
	}
	return t.Transaction.Query(ctx, query, args...)
}
func TestSourceRPCAsyncReadBackends(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			backend := &asyncRPCReadGateBackend{Backend: sourceBackend(t, ctx, dialect)}
			db, err := New(ctx, backend, sourceActionsModel(), fmt.Sprintf("ponder_go_async_read_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			cache, err := db.EnableProtocolCache(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema.Quote(cache.Namespace())+" CASCADE") })
			key := `[1,"eth_call",[{"to":"0xABC","data":"0x1234"},"0x2"]]`
			t.Run("prepared_read_hit_and_miss", func(t *testing.T) {
				adapter := cache.AsyncSourceRPCRequestCache(1)
				defer adapter.Close(ctx)
				if err := adapter.CachePut(ctx, key, json.RawMessage(`"0xAB"`)); err != nil {
					t.Fatal(err)
				}
				db.mu.Lock()
				hit := adapter.BeginCacheGet(ctx, key)
				miss := adapter.BeginCacheGet(ctx, `[1,"eth_chainId",[]]`)
				adapter.reads.mu.Lock()
				pending := len(adapter.reads.pending)
				adapter.reads.mu.Unlock()
				db.mu.Unlock()
				if pending != 2 {
					t.Fatal("missing real queued reads", pending)
				}
				first, second := <-hit, <-miss
				if first.Err != nil || !first.Hit || string(first.Value) != `"0xAB"` || second.Err != nil || second.Hit || second.Value != nil {
					t.Fatal("actual read results", first, second)
				}
				if err := adapter.WaitCacheReads(ctx); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("close_timeout_retains_read_ownership", func(t *testing.T) {
				adapter := cache.AsyncSourceRPCRequestCache(1)
				gate := &asyncRPCGate{entered: make(chan struct{}), release: make(chan struct{})}
				backend.setGate(gate)
				defer gate.unblock()
				future := adapter.BeginCacheGet(ctx, key)
				awaitAsyncGate(t, gate.entered)
				deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
				if err := adapter.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("Close before inflight read released", err)
				}
				adapter.reads.mu.Lock()
				pending := len(adapter.reads.pending)
				adapter.reads.mu.Unlock()
				if pending != 1 {
					t.Fatal("Close lost read ownership", pending)
				}
				gate.unblock()
				result := <-future
				if !errors.Is(result.Err, context.Canceled) || result.Hit || result.Value != nil {
					t.Fatal("canceled inflight read", result)
				}
				if err := adapter.Close(ctx); err != nil {
					t.Fatal(err)
				}
				backend.setGate(nil)
			})
			t.Run("bound_adapters_no_async_reader", func(t *testing.T) {
				source, err := cache.SourceSyncStore(16000)
				if err != nil {
					t.Fatal(err)
				}
				if err = source.WithTransaction(ctx, func(bound *SourceSyncStore) error {
					if _, ok := any(bound.RPCRequestCache(1)).(rpc.AsyncCacheReader); ok {
						t.Fatal("bound source unexpectedly async")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err = db.Apply(ctx, event("async-reader-bound", 1, 1), func(w *Writer) error {
					if _, ok := any(w.SourceRPCRequestCache()).(rpc.AsyncCacheReader); ok {
						t.Fatal("writer unexpectedly async")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("late_sql_read_error", func(t *testing.T) {
				adapter := cache.AsyncSourceRPCRequestCache(1)
				defer adapter.Close(ctx)
				db.mu.Lock()
				future := adapter.BeginCacheGet(ctx, key)
				if err := backend.Exec(ctx, "DROP TABLE "+cache.table("rpc_request_results")); err != nil {
					db.mu.Unlock()
					t.Fatal(err)
				}
				db.mu.Unlock()
				result := <-future
				if result.Err == nil || !strings.Contains(result.Err.Error(), "does not exist") || result.Hit || result.Value != nil {
					t.Fatal("late query failure", result)
				}
				if err := adapter.WaitCacheReads(ctx); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
