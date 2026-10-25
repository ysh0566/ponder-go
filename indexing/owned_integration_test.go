package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type ownedFixture struct {
	options OwnedOptions
	open    func(context.Context, OwnedOptions) (*Owned, error)
}

func newOwnedFixture(t *testing.T, dialect string, service Options, requester rpc.Requester) ownedFixture {
	t.Helper()
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	options := OwnedOptions{Storage: store.IndependentStorageOptions{
		BusinessNamespace: "ponder_go_m41_b_" + id, SyncNamespace: "ponder_go_m41_s_" + id,
		Schema: schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}},
	}, Service: service, Requesters: []rpc.Requester{requester}}
	f := ownedFixture{options: options, open: OpenOwnedPGlite}
	if dialect == "postgres" {
		f.open = OpenOwnedPostgres
		f.options.Storage.PostgresURL = os.Getenv("PONDER_TEST_POSTGRES")
		if f.options.Storage.PostgresURL == "" {
			t.Fatal("explicit task PostgreSQL required")
		}
		// Timestamp uniqueness alone is not ownership proof. Check every exact
		// family member before registering any destructive fixture cleanup.
		var namespaces []string
		for _, n := range []string{f.options.Storage.BusinessNamespace, f.options.Storage.SyncNamespace} {
			for _, suffix := range []string{"_rpc_1", "_legacy", "_sync", ""} {
				namespaces = append(namespaces, n+suffix)
			}
		}
		preflightErr := func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			b, err := store.OpenPostgres(ctx, f.options.Storage.PostgresURL)
			if err != nil {
				return err
			}
			defer b.Close()
			for _, namespace := range namespaces {
				rows, err := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", namespace)
				if err != nil {
					return err
				}
				if len(rows.Rows) != 0 {
					return fmt.Errorf("refusing existing fixture namespace %q; no cleanup registered", namespace)
				}
			}
			return nil
		}()
		if preflightErr != nil {
			t.Fatal(preflightErr)
		}
		// The serial test database lease excludes concurrent fixture creators.
		// Only these preflight-absent, exact fixture namespaces are removed.
		t.Cleanup(func() {
			b, e := store.OpenPostgres(context.Background(), f.options.Storage.PostgresURL)
			if e != nil {
				t.Error(e)
				return
			}
			defer b.Close()
			for _, namespace := range namespaces {
				if e := b.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(namespace)+" CASCADE"); e != nil {
					t.Error(e)
				}
			}
		})
	} else {
		root := t.TempDir()
		f.options.Storage.BusinessDirectory = filepath.Join(root, "business")
		f.options.Storage.SyncDirectory = filepath.Join(root, "sync")
	}
	return f
}
func (f ownedFixture) start(t *testing.T) *Owned {
	t.Helper()
	init, cancel := context.WithCancel(context.Background())
	o, e := f.open(init, f.options)
	cancel()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if e := o.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return o
}
func ownedCP(block uint64) checkpoint.Checkpoint {
	return checkpoint.Checkpoint{ChainID: 1, BlockTimestamp: block, BlockNumber: block, EventType: 5}
}
func ownedBaseline(t *testing.T, o *Owned) {
	t.Helper()
	encoded, e := checkpoint.Encode(ownedCP(0))
	if e != nil {
		t.Fatal(e)
	}
	e = o.storage.BusinessActions().InitializeRuntime(context.Background(), store.CheckpointState{ChainID: 1, ChainName: "one", SafeCheckpoint: encoded, FinalizedCheckpoint: encoded, LatestCheckpoint: encoded}, func(*store.Writer) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
}
func ownedChain() Object { return Object{"id": 1, "name": "one", "cacheRpcRequests": true} }
func ownedBasicOptions() Options {
	chain := ownedChain()
	return Options{Build: Build{Chains: []Object{chain}, Contracts: []Object{{}}, IndexingFunctions: []Object{}}, Errors: &ErrorState{}}
}

func TestOwnedPersistentHandlerTransactions(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			var callsMu sync.Mutex
			calls := map[string]int{}
			requester := admissionRequester(func(ctx context.Context, method string, params, out any) error {
				callsMu.Lock()
				calls[method]++
				callsMu.Unlock()
				switch method {
				case "eth_getBalance":
					return json.Unmarshal([]byte(`"0x2a"`), out)
				case "eth_call":
					return json.Unmarshal([]byte(`"0x`+strings.Repeat("0", 62)+`2a"`), out)
				default:
					return fmt.Errorf("unexpected RPC %s", method)
				}
			})
			a := actionTestABI(t, `[{"type":"function","name":"value","inputs":[],"outputs":[{"type":"uint256"}]}]`)
			contract := ContractCall{ABI: a, Address: "0x0000000000000000000000000000000000000001", FunctionName: "value"}
			var owner *Owned
			var savedArgs *HandlerArgs
			var savedContext *Context
			var savedClient *Client
			var oldCtx context.Context
			phase := 1
			handler := Handler(func(run context.Context, args *HandlerArgs) error {
				if savedArgs == nil {
					savedArgs = args
					savedContext = args.Context
					savedClient = args.Context.Client
				} else if args != savedArgs || args.Context != savedContext || args.Context.Client != savedClient || args.Context.DB != owner.Database() {
					return errors.New("handler identities changed")
				}
				if oldCtx != nil {
					if _, e := args.Context.DB.Find(oldCtx, "account", store.Row{"id": "alice"}); e == nil {
						return errors.New("old callback context accepted during later binding")
					}
				}
				for i := 0; i < 2; i++ {
					value, e := args.Context.Client.GetBalance(run, "0xabc")
					if e != nil || value.Int64() != 42 {
						return fmt.Errorf("balance: %v %v", value, e)
					}
					read, e := args.Context.Client.ReadContract(run, contract)
					if e != nil || read.(*big.Int).Int64() != 42 {
						return fmt.Errorf("contract: %v %v", read, e)
					}
					if e = owner.Clients().WaitCacheWrites(run); e != nil {
						return e
					}
				}
				if phase == 1 {
					_, e := args.Context.DB.InsertOne(run, "account", store.Row{"id": "alice", "balance": big.NewInt(1)}, store.Conflict{})
					return e
				}
				_, e := args.Context.DB.Update(run, "account", store.Row{"id": "alice"}, store.Row{"balance": big.NewInt(2)})
				return e
			})
			chain := ownedChain()
			setup := Object{"name": "Token:setup", "chain": chain, "block": 1, "fn": handler}
			eventCB := Object{"name": "Token:block", "fn": handler, "filter": Object{"type": "block", "include": DefaultFilterInclude("block", false)}}
			service := Options{Build: Build{Chains: []Object{chain}, Contracts: []Object{{}}, SetupCallbacks: [][]Object{{setup}}, EventCallbacks: [][]Object{{eventCB}}, IndexingFunctions: []Object{{"name": "Token:setup"}, {"name": "Token:block"}}}, Errors: &ErrorState{}}
			fixture := newOwnedFixture(t, dialect, service, requester)
			owner = fixture.start(t)
			ownedBaseline(t, owner)
			var firstCtx context.Context
			if e := owner.Batch(ctx, 1, true, func(run context.Context, s *Service, w *store.Writer) error {
				firstCtx = run
				if e := s.ProcessSetupEvents(run); e != nil {
					return e
				}
				if e := w.CommitBlock(run, "account", ownedCP(1)); e != nil {
					return e
				}
				return w.AdvanceLatest(run, ownedCP(1))
			}); e != nil {
				t.Fatal(e)
			}
			oldCtx = firstCtx
			phase = 2
			rollback := errors.New("rollback requested")
			beforeJournal, e := owner.storage.BusinessActions().Journal(ctx, "account")
			if e != nil {
				t.Fatal(e)
			}
			beforeStates, e := owner.storage.BusinessActions().Checkpoints(ctx)
			if e != nil {
				t.Fatal(e)
			}
			event := Object{"type": "block", "chain": chain, "eventCallback": eventCB, "event": Object{"block": Object{"number": big.NewInt(2)}}}
			e = owner.Batch(ctx, 1, true, func(run context.Context, s *Service, w *store.Writer) error {
				if e := s.ProcessHistoricalEvents(run, []Object{event}, nil); e != nil {
					return e
				}
				if e := w.CommitBlock(run, "account", ownedCP(2)); e != nil {
					return e
				}
				if e := w.AdvanceLatest(run, ownedCP(2)); e != nil {
					return e
				}
				return rollback
			})
			if !errors.Is(e, rollback) {
				t.Fatal(e)
			}
			afterJournal, e := owner.storage.BusinessActions().Journal(ctx, "account")
			if e != nil || !reflect.DeepEqual(beforeJournal, afterJournal) {
				t.Fatal("journal escaped rollback", afterJournal, e)
			}
			afterStates, e := owner.storage.BusinessActions().Checkpoints(ctx)
			if e != nil || !reflect.DeepEqual(beforeStates, afterStates) {
				t.Fatal("checkpoint escaped rollback", afterStates, e)
			}
			if e = owner.Batch(ctx, 1, true, func(run context.Context, s *Service, w *store.Writer) error {
				row, e := owner.Database().Find(run, "account", store.Row{"id": "alice"})
				if e != nil || row["balance"].(*big.Int).Int64() != 1 {
					return fmt.Errorf("rollback cache leaked: %v %v", row, e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			callsMu.Lock()
			if calls["eth_getBalance"] != 2 || calls["eth_call"] != 2 {
				t.Fatal("miss/hit counts", calls)
			}
			callsMu.Unlock()
			if e = owner.Close(ctx); e != nil {
				t.Fatal(e)
			}
			owner = fixture.start(t)
			client, e := owner.Clients().GetClient(chain)
			if e != nil {
				t.Fatal(e)
			}
			for _, block := range []int64{1, 2} {
				v, e := client.GetBalance(ctx, "0xabc", BalanceOptions{BlockNumber: big.NewInt(block)})
				if e != nil || v.Int64() != 42 {
					t.Fatal(v, e)
				}
				v2, e := client.ReadContract(ctx, contract, ReadContractOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(block)}})
				if e != nil || v2.(*big.Int).Int64() != 42 {
					t.Fatal(v2, e)
				}
			}
			callsMu.Lock()
			defer callsMu.Unlock()
			if calls["eth_getBalance"] != 2 || calls["eth_call"] != 2 {
				t.Fatal("independent cache not durable across rollback/reopen", calls)
			}
		})
	}
}

func TestOwnedPendingCacheAcrossBindingExit(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, scenario := range []string{"commit", "rollback", "caller-cancel", "late-error", "owner-close"} {
				t.Run(scenario, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
					defer cancel()
					var owner *Owned
					entered, release, heldDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
					var releaseOnce sync.Once
					unblock := func() { releaseOnce.Do(func() { close(release) }) }
					defer unblock()
					requester := admissionRequester(func(ctx context.Context, method string, params, out any) error {
						if method != "eth_getBalance" {
							return fmt.Errorf("unexpected method %s", method)
						}
						syncStore, e := owner.storage.SyncCache().SourceSyncStore(100)
						if e != nil {
							return e
						}
						go func() {
							heldDone <- syncStore.WithTransaction(context.Background(), func(*store.SourceSyncStore) error { close(entered); <-release; return nil })
						}()
						select {
						case <-entered:
						case <-ctx.Done():
							return ctx.Err()
						}
						return json.Unmarshal([]byte(`"0x2a"`), out)
					})
					fixture := newOwnedFixture(t, dialect, ownedBasicOptions(), requester)
					owner = fixture.start(t)
					callCtx, stopCall := context.WithCancel(ctx)
					defer stopCall()
					rollback := errors.New("business failure")
					owner.Clients().SetEvent(Object{"type": "setup", "block": 7})
					client, e := owner.Clients().GetClient(ownedChain())
					if e != nil {
						t.Fatal(e)
					}
					var callbackCtx context.Context
					e = owner.Batch(callCtx, 1, true, func(run context.Context, _ *Service, w *store.Writer) error {
						callbackCtx = run
						value, e := client.GetBalance(run, "0xabc")
						if e != nil || value.Int64() != 42 {
							return fmt.Errorf("get balance %v %v", value, e)
						}
						if _, e = owner.Database().InsertOne(run, "account", store.Row{"id": "pending", "balance": big.NewInt(1)}, store.Conflict{}); e != nil {
							return e
						}
						if scenario == "rollback" {
							return rollback
						}
						return nil
					})
					if scenario == "rollback" {
						if !errors.Is(e, rollback) {
							t.Fatal(e)
						}
					} else if e != nil {
						t.Fatal(e)
					}
					if callbackCtx.Err() != nil {
						t.Fatal("normal binding exit cancelled independent work", callbackCtx.Err())
					}
					short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
					e = owner.caches[0].base.WaitCacheWrites(short)
					stop()
					if !errors.Is(e, context.DeadlineExceeded) {
						t.Fatal("actual cache queue not pending at binding exit", e)
					}
					switch scenario {
					case "caller-cancel":
						stopCall()
					case "late-error":
						if e = owner.storage.SyncCache().Close(); e != nil {
							t.Fatal(e)
						}
					case "owner-close":
						short, stop = context.WithTimeout(ctx, 20*time.Millisecond)
						e = owner.Close(short)
						stop()
						// Cancellation can settle the queued job before it acquires
						// SQL. The held transaction belongs only to this fixture,
						// not the owner's admitted client work. PG may close now;
						// PGlite must wait for its backend transaction mutex.
						if e != nil && !errors.Is(e, context.DeadlineExceeded) {
							t.Fatal("owner close", e)
						}
						if e = owner.Batch(ctx, 1, true, func(context.Context, *Service, *store.Writer) error { return nil }); e == nil {
							t.Fatal("closing owner admitted batch")
						}
					}
					unblock()
					if e = <-heldDone; e != nil {
						t.Fatal(e)
					}
					if e = owner.Clients().WaitCacheWrites(ctx); e != nil {
						t.Fatal(e)
					}
					if e = owner.Close(ctx); e != nil {
						t.Fatal(e)
					}
					owner = fixture.start(t)
					request := map[string]any{"method": "eth_getBalance", "params": []any{"0xabc", "0x7"}}
					results, e := owner.storage.SyncCache().GetSourceRPCRawRequestResults(ctx, 1, []any{request})
					if e != nil || len(results) != 1 {
						t.Fatal(results, e)
					}
					durable := scenario == "commit" || scenario == "rollback"
					if durable != (results[0] != nil) {
						t.Fatalf("cache durability=%v result=%v; settled is not persisted", durable, results)
					}
					rows, e := owner.storage.BusinessActions().Select(ctx, "account")
					if e != nil {
						t.Fatal(e)
					}
					expected := 1
					if scenario == "rollback" {
						expected = 0
					}
					if len(rows) != expected {
						t.Fatal("business rollback independent from cache", rows)
					}
				})
			}
		})
	}
}

func TestOwnedCloseWaitsActiveBinding(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			fixture := newOwnedFixture(t, dialect, ownedBasicOptions(), &balanceRequester{})
			owner := fixture.start(t)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			go func() {
				done <- owner.Batch(ctx, 1, true, func(run context.Context, _ *Service, _ *store.Writer) error {
					close(entered)
					<-release
					_, e := owner.Database().InsertOne(run, "account", store.Row{"id": "last", "balance": big.NewInt(1)}, store.Conflict{})
					return e
				})
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
			e := owner.Close(short)
			stop()
			if !errors.Is(e, context.DeadlineExceeded) {
				t.Fatal("close did not retain active binding", e)
			}
			if e = owner.Database().WithReadOnly(ctx, 1, func(context.Context) error { return nil }); e == nil {
				t.Fatal("closing owner admitted read-only binding")
			}
			unblock()
			if e = <-done; e != nil {
				t.Fatal(e)
			}
			if e = owner.Close(ctx); e != nil {
				t.Fatal(e)
			}
			owner = fixture.start(t)
			rows, e := owner.storage.BusinessActions().Select(ctx, "account")
			if e != nil || len(rows) != 1 {
				t.Fatal("close cancelled admitted business transaction", rows, e)
			}
		})
	}
}
