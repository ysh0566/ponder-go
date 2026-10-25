package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type admissionRequester func(context.Context, string, any, any) error

func (f admissionRequester) Call(ctx context.Context, method string, params, out any) error {
	return f(ctx, method, params, out)
}

// This wrapper observes completion of the real adapter's queue, not a fabricated
// scheduling handshake. The action receives a separate copy of the same future.
type observedAdmissionCache struct {
	*store.AsyncSourceRPCRequestCache
	completed chan error
}

func (c *observedAdmissionCache) BeginCachePut(ctx context.Context, key string, value json.RawMessage) <-chan error {
	actual := c.AsyncSourceRPCRequestCache.BeginCachePut(ctx, key, value)
	result := make(chan error, 1)
	go func() { err := <-actual; c.completed <- err; result <- err; close(result) }()
	return result
}

func TestClientRealCacheAdmissionLifecycle(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"success", "late-error", "caller-cancel", "client-close", "adapter-close", "business-rollback"} {
				t.Run(scenario, func(t *testing.T) {
					s := adapterStore(t, backend)
					ctx := context.Background()
					protocol, err := s.EnableProtocolCache(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						_ = s.Backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(protocol.Namespace())+" CASCADE")
					})
					adapter := protocol.AsyncSourceRPCRequestCache(1)
					observed := &observedAdmissionCache{adapter, make(chan error, 1)}
					entered, release, ownerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
					released := false
					defer func() {
						if !released {
							close(release)
						}
					}()
					rollback := errors.New("original business rollback")
					requester := admissionRequester(func(ctx context.Context, method string, params, out any) error {
						if method != "eth_getBalance" {
							return fmt.Errorf("unexpected method %s", method)
						}
						go func() {
							ownerDone <- s.Apply(context.Background(), adapterEvent("held", 1), func(w *store.Writer) error {
								// Strict support is deliberately unavailable for the bound Writer: it
								// must never detach a write from this transaction's lifetime.
								if _, err := NewStrictClientSet([]Object{{"id": 1, "cacheRpcRequests": true}}, []rpc.Requester{&balanceRequester{}}, w.SourceRPCRequestCache()); err == nil {
									return errors.New("bound writer accepted strict asynchronous mode")
								}
								close(entered)
								<-release
								if scenario == "business-rollback" {
									return rollback
								}
								return nil
							})
						}()
						select {
						case <-entered:
						case <-time.After(5 * time.Second):
							return errors.New("transaction owner did not enter")
						}
						return json.Unmarshal([]byte(`"0x2a"`), out)
					})
					chain := Object{"id": 1, "cacheRpcRequests": true}
					clients, err := NewStrictClientSet([]Object{chain}, []rpc.Requester{requester}, observed)
					if err != nil {
						t.Fatal(err)
					}
					clients.SetEvent(Object{"type": "setup", "block": 0})
					client, _ := clients.GetClient(chain)
					callCtx, cancel := context.WithCancel(ctx)
					defer cancel()
					result, err := client.GetBalance(callCtx, "0xabc")
					if err != nil || result.Int64() != 42 {
						t.Fatal(result, err)
					}
					// The actual adapter must ALREADY own a pending prepared write when the
					// action returns, while the actual Store owner prevents any SQL execution.
					blocked, stop := context.WithTimeout(ctx, 15*time.Millisecond)
					err = adapter.WaitCacheWrites(blocked)
					stop()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("action returned without real pending queue admission", err)
					}
					if scenario == "late-error" {
						_ = protocol.Close()
					}
					if scenario == "caller-cancel" {
						cancel()
					}
					if scenario == "client-close" || scenario == "adapter-close" {
						closeCtx, stop := context.WithTimeout(ctx, 2*time.Second)
						if scenario == "client-close" {
							err = clients.Close(closeCtx)
						} else {
							err = adapter.Close(closeCtx)
						}
						stop()
						if err != nil {
							t.Fatal("Close deadlocked behind transaction owner", err)
						}
					}
					close(release)
					released = true
					ownerErr := <-ownerDone
					if scenario == "business-rollback" {
						if ownerErr != rollback {
							t.Fatal("rollback error changed", ownerErr)
						}
					} else if ownerErr != nil {
						t.Fatal(ownerErr)
					}
					if err = clients.WaitCacheWrites(ctx); err != nil {
						t.Fatal(err)
					}
					if err = adapter.WaitCacheWrites(ctx); err != nil {
						t.Fatal(err)
					}
					completion := <-observed.completed
					success := scenario == "success" || scenario == "business-rollback"
					if success && completion != nil {
						t.Fatal(completion)
					}
					if !success && completion == nil {
						t.Fatal("expected owned write failure")
					}
					// A late cache failure never retroactively changes the completed action.
					if result.Int64() != 42 {
						t.Fatal("successful result mutated")
					}
					if success {
						raw, hit, err := adapter.CacheGet(ctx, `[1,"eth_getBalance",["0xabc","0x0"]]`)
						if err != nil || !hit || string(raw) != `"0x2a"` {
							t.Fatal("independent cache commit missing", string(raw), hit, err)
						}
					}
					if err = clients.Close(ctx); err != nil {
						t.Fatal(err)
					}
					if err = adapter.Close(ctx); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}
