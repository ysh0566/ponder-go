package indexing

// These M43 supplemental tests were newly rebuilt after the workspace loss.
// They are not recovered test bytes or mappings of additional upstream tests.

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type historicalAdmissionRequester struct {
	begin func(context.Context, string, any, any) <-chan error
	calls atomic.Int64
}

func (r *historicalAdmissionRequester) Call(context.Context, string, any, any) error {
	r.calls.Add(1)
	return fmt.Errorf("unexpected synchronous fallback")
}
func (r *historicalAdmissionRequester) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	return r.begin(ctx, method, params, out)
}

func historicalWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("causal operation did not settle")
	}
}

// The real Actions entry point must see AsyncRequester on the FINAL scope
// wrapper. A goroutine handshake around Call is not synchronous admission.
func TestHistoricalScopeAsyncAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted := make(chan struct{})
	providerResult := make(chan error, 1)
	marker := errors.New("actual provider result")
	var actualCtx context.Context
	r := &historicalAdmissionRequester{begin: func(run context.Context, method string, params, out any) <-chan error {
		if method != "eth_getBlockByNumber" || !reflect.DeepEqual(params, []any{"0x1", false}) {
			t.Fatalf("unexpected action admission: %s %v", method, params)
		}
		actualCtx = run
		close(admitted)
		return providerResult
	}}
	owner := &Owned{service: &Service{options: Options{Build: Build{Chains: []Object{{"id": 1}}}}}, requesters: []*ownedRequester{{base: r, gate: newOwnedGate()}}}
	scope := &HistoricalSync{owner: owner, ctx: ctx, cancel: cancel, gate: newOwnedGate()}
	requester, err := scope.Requester(1)
	if err != nil {
		t.Fatal(err)
	}
	again, err := scope.Requester(1)
	if err != nil || requester != again {
		t.Fatal("requester identity changed", err)
	}
	actions := requester.(interface{ Actions() *rpc.Actions }).Actions()
	continuation, async := actions.BeginSourceBlockByNumber(ctx, "0x1", false)
	if !async || continuation == nil {
		t.Fatal("scope Actions lost real async admission")
	}
	select {
	case <-admitted:
	default:
		t.Fatal("Begin returned before provider admission")
	}
	closing := make(chan struct{})
	go func() { scope.gate.close(); close(closing) }()
	historicalWait(t, actualCtx.Done())
	select {
	case <-closing:
		t.Fatal("gate dropped the unresolved provider future")
	default:
	}
	providerResult <- marker
	close(providerResult)
	if _, err := continuation(); !errors.Is(err, marker) {
		t.Fatal(err)
	}
	historicalWait(t, closing)
	if r.calls.Load() != 0 {
		t.Fatal("async action fell back to Call")
	}
	if err := requester.Call(context.Background(), "eth_chainId", nil, new(any)); err == nil {
		t.Fatal("escaped requester remained usable")
	}
	owner.requesters[0].gate.close()
}

func TestHistoricalScopeSynchronousCapability(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int64
	r := admissionRequester(func(context.Context, string, any, any) error { calls.Add(1); return nil })
	owner := &Owned{service: &Service{options: Options{Build: Build{Chains: []Object{{"id": 1}}}}}, requesters: []*ownedRequester{{base: r, gate: newOwnedGate()}}}
	scope := &HistoricalSync{owner: owner, ctx: ctx, cancel: cancel, gate: newOwnedGate()}
	requester, err := scope.Requester(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, advertised := requester.(rpc.AsyncRequester); advertised {
		t.Fatal("invented async admission for synchronous transport")
	}
	actions := requester.(interface{ Actions() *rpc.Actions }).Actions()
	if continuation, async := actions.BeginSourceBlockByNumber(ctx, "0x1", false); async || continuation != nil || calls.Load() != 0 {
		t.Fatal("synchronous action was admitted by async path")
	}
	if err := requester.Call(ctx, "eth_chainId", nil, new(any)); err != nil || calls.Load() != 1 {
		t.Fatal(err)
	}
	scope.gate.close()
	if err := requester.Call(ctx, "eth_chainId", nil, new(any)); err == nil || calls.Load() != 1 {
		t.Fatal("revoked requester escaped gate", err)
	}
	owner.requesters[0].gate.close()
}

func TestOwnedHistoricalScopeLifecycle(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			options, _, _ := multiBuild(func(context.Context, *HandlerArgs) error { return nil })
			f := multiFixture(t, dialect, options)
			admitted, providerCanceled := make(chan struct{}), make(chan struct{})
			providerResult := make(chan error, 1)
			var cancelOnce sync.Once
			defer func() { cancelOnce.Do(func() { providerResult <- context.Canceled; close(providerResult) }) }()
			r := &historicalAdmissionRequester{begin: func(run context.Context, _ string, _, _ any) <-chan error {
				close(admitted)
				go func() { <-run.Done(); close(providerCanceled) }()
				return providerResult
			}}
			f.options.Requesters = []rpc.Requester{r, r}
			owner := f.start(t)
			if owner.Dialect() != dialect {
				t.Fatal("dialect was not derived from actual backend", owner.Dialect())
			}
			var escaped *HistoricalSync
			var stale context.Context
			err := owner.WithHistoricalSync(ctx, 16000, func(run context.Context, scope *HistoricalSync) error {
				// Keep values but deliberately remove cancellation, so rejection
				// below proves private generation identity rather than ctx.Err().
				escaped, stale = scope, context.WithoutCancel(run)
				if _, err := owner.HistoricalSnapshot(ctx); err == nil {
					return errors.New("outside context entered active historical scope")
				}
				if err := owner.Database().WithReadOnly(ctx, 1, func(context.Context) error { return nil }); err == nil {
					return errors.New("outside readonly context entered active scope")
				}
				if err := owner.WithHistoricalSync(run, 16000, func(context.Context, *HistoricalSync) error { return nil }); err == nil {
					return errors.New("nested acquisition admitted")
				}
				if err := owner.InitializeMultichain(run, multiInitial()); err != nil {
					return err
				}
				if err := owner.Database().WithReadOnly(run, 1, func(read context.Context) error { return owner.Database().Prefetch(read, nil) }); err != nil {
					return err
				}
				if err := owner.Invalidate(run); err != nil {
					return err
				}
				var escapedTx *store.SourceSyncStore
				if err := scope.WithTransaction(run, func(tx *store.SourceSyncStore) error { escapedTx = tx; return nil }); err != nil {
					return err
				}
				if err := escapedTx.InsertIntervals(ctx, 1, nil, nil); err == nil {
					return errors.New("sync transaction capability escaped")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = escaped.GetIntervals(ctx, nil); err == nil {
				t.Fatal("escaped sync scope remained usable")
			}
			if _, err = owner.HistoricalSnapshot(stale); err == nil {
				t.Fatal("old scope context remained usable")
			}
			if _, err = owner.HistoricalSnapshot(ctx); err != nil {
				t.Fatal(err)
			}
			scopeResult := make(chan error, 1)
			go func() {
				scopeResult <- owner.WithHistoricalSync(ctx, 16000, func(run context.Context, scope *HistoricalSync) error {
					if _, err := owner.HistoricalSnapshot(stale); err == nil {
						return errors.New("old context authorized new scope")
					}
					requester, err := scope.Requester(1)
					if err != nil {
						return err
					}
					requester.(rpc.AsyncRequester).BeginCall(run, "eth_chainId", nil, new(any))
					<-run.Done()
					return run.Err()
				})
			}()
			historicalWait(t, admitted)
			wait, stop := context.WithTimeout(ctx, 20*time.Millisecond)
			err = owner.Close(wait)
			stop()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("Close failed to retain unfinished provider", err)
			}
			historicalWait(t, providerCanceled)
			select {
			case err := <-scopeResult:
				t.Fatal("scope released before provider completion", err)
			default:
			}
			if _, err := owner.storage.BusinessStore().Backend.Query(ctx, "SELECT 1 AS alive"); err != nil {
				t.Fatal("Close released backend under pending scope", err)
			}
			if replacement, err := f.open(ctx, f.options); err == nil {
				_ = replacement.Close(ctx)
				t.Fatal("Close timeout released storage lease")
			}
			cancelOnce.Do(func() { providerResult <- context.Canceled; close(providerResult) })
			if err = <-scopeResult; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err = owner.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = owner.HistoricalSnapshot(ctx); err == nil {
				t.Fatal("snapshot reopened closed owner")
			}
			owner = f.start(t)
			if snapshot, err := owner.HistoricalSnapshot(ctx); err != nil || !snapshot.Initialized || len(snapshot.Chains) != 2 {
				t.Fatal("durable reopen", snapshot, err)
			}
		})
	}
}

func TestOwnedHistoricalSnapshotValidation(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			options, _, _ := multiBuild(func(context.Context, *HandlerArgs) error { return nil })
			f := multiFixture(t, dialect, options)
			owner := f.start(t)
			internal := func(name string) string {
				return schema.Quote(owner.storage.BusinessStore().Namespace) + "." + schema.Quote(name)
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if err := owner.storage.BusinessStore().Backend.Exec(ctx, q, args...); err != nil {
					t.Fatal(err)
				}
			}
			reject := func(name string) {
				t.Helper()
				if snapshot, err := owner.HistoricalSnapshot(ctx); err == nil {
					t.Fatalf("%s accepted: %+v", name, snapshot)
				}
			}
			fresh, err := owner.HistoricalSnapshot(ctx)
			if err != nil || fresh.Initialized || len(fresh.Chains) != 0 {
				t.Fatal(fresh, err)
			}
			exec("INSERT INTO "+internal("_ponder_go_progress")+" (chain_id,checkpoint) VALUES(99,$1)", checkpoint.MustEncode(multiCP(99, 1)))
			reject("fresh orphan progress")
			exec("DELETE FROM " + internal("_ponder_go_progress"))
			journalSQL := "INSERT INTO " + internal("_ponder_go_journal") + " (table_name,row_key,row_data,operation,checkpoint,chain_id) VALUES('account','{}','{}',0,$1,$2)"
			exec(journalSQL, checkpoint.MustEncode(multiCP(1, 1)), 1)
			reject("fresh journal")
			exec("DELETE FROM " + internal("_ponder_go_journal"))
			if err := owner.InitializeMultichain(ctx, multiInitial()); err != nil {
				t.Fatal(err)
			}
			baseline, err := owner.HistoricalSnapshot(ctx)
			if err != nil || !baseline.Initialized || len(baseline.Chains) != 2 {
				t.Fatal(baseline, err)
			}
			configured := owner.service.options.Build.Chains
			owner.service.options.Build.Chains = append(append([]Object{}, configured...), Object{"id": 99, "name": "ninety-nine"})
			reject("incomplete configured chain coverage")
			owner.service.options.Build.Chains = configured
			ordering := owner.storage.BusinessStore().Ordering
			owner.storage.BusinessStore().Ordering = schema.Isolated
			reject("nondefault ordering")
			owner.storage.BusinessStore().Ordering = ordering
			for _, chain := range baseline.Chains {
				if chain.Cursor != chain.SafeCheckpoint || chain.Cursor != chain.FinalizedCheckpoint || chain.Cursor != chain.LatestCheckpoint {
					t.Fatal("inconsistent returned snapshot", chain)
				}
			}
			cursor := baseline.Chains[0].Cursor
			exec("DELETE FROM " + internal("_ponder_go_runtime_cursor") + " WHERE chain_id=1")
			reject("missing private cursor")
			exec("INSERT INTO "+internal("_ponder_go_runtime_cursor")+"(chain_id,checkpoint) VALUES(1,$1)", cursor)
			exec("UPDATE "+internal("_ponder_go_runtime_cursor")+" SET checkpoint=$1 WHERE chain_id=1", checkpoint.MustEncode(multiCP(1, 9)))
			reject("public/private mismatch")
			exec("UPDATE "+internal("_ponder_go_runtime_cursor")+" SET checkpoint=$1 WHERE chain_id=1", cursor)
			exec("INSERT INTO "+internal("_ponder_go_runtime_cursor")+"(chain_id,checkpoint) VALUES(99,$1)", checkpoint.MustEncode(multiCP(99, 9)))
			reject("orphan private cursor")
			exec("DELETE FROM " + internal("_ponder_go_runtime_cursor") + " WHERE chain_id=99")
			exec("UPDATE " + internal("_ponder_go_chains") + " SET chain_name='wrong' WHERE chain_id=1")
			reject("configured name mismatch")
			exec("UPDATE " + internal("_ponder_go_chains") + " SET chain_name='one' WHERE chain_id=1")
			exec("UPDATE "+internal("_ponder_go_progress")+" SET checkpoint=$1 WHERE chain_id=1", checkpoint.MaxString)
			reject("MAX public checkpoint")
			exec("UPDATE "+internal("_ponder_go_progress")+" SET checkpoint=$1 WHERE chain_id=1", cursor)
			exec(journalSQL, checkpoint.MaxString, 1)
			reject("unsealed MAX journal")
			exec("DELETE FROM " + internal("_ponder_go_journal"))
			exec(journalSQL, checkpoint.MustEncode(multiCP(99, 9)), 99)
			reject("orphan journal chain")
			exec("DELETE FROM " + internal("_ponder_go_journal"))
			// A configured other-chain journal row is not an orphan: default
			// multichain pruning retains a global suffix, not per-chain emptiness.
			exec(journalSQL, checkpoint.MustEncode(multiCP(2, 1)), 2)
			if _, err := owner.HistoricalSnapshot(ctx); err != nil {
				t.Fatal("legal other-chain journal rejected", err)
			}
			exec("DELETE FROM " + internal("_ponder_go_journal"))
			actual, err := owner.HistoricalSnapshot(ctx)
			if err != nil || !reflect.DeepEqual(actual, baseline) {
				t.Fatal("corruption repair changed baseline", actual, err)
			}
			if err := owner.Close(ctx); err != nil {
				t.Fatal(err)
			}
			owner = f.start(t)
			actual, err = owner.HistoricalSnapshot(ctx)
			if err != nil || !reflect.DeepEqual(actual, baseline) {
				t.Fatal("resume snapshot changed across reopen", actual, err)
			}
		})
	}
}

func TestOwnedHistoricalRefetchBoundary(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			calls, refetches := 0, 0
			options, chains, cb := multiBuild(func(run context.Context, args *HandlerArgs) error {
				calls++
				block, _ := args.Event.Property("block")
				number, err := block.Get("number")
				if err != nil {
					return err
				}
				id := fmt.Sprint(number)
				if _, err = args.Context.DB.InsertOne(run, "account", store.Row{"id": id, "balance": big.NewInt(1)}, store.Conflict{}); err != nil {
					return err
				}
				if _, err = args.Context.DB.Find(run, "account", store.Row{"id": id}); err != nil {
					return err
				}
				if id == "4" {
					_, err = block.Get("gasUsed")
				}
				return err
			})
			f := multiFixture(t, dialect, options)
			owner := f.start(t)
			if err := owner.InitializeMultichain(ctx, multiInitial()); err != nil {
				t.Fatal(err)
			}
			before, err := owner.HistoricalSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			events := []Object{multiEvent(chains[0], cb, 3), multiEvent(chains[0], cb, 4)}
			request := map[string]any{"method": "eth_getBalance", "params": []any{"0x0000000000000000000000000000000000000001", "0x4"}}
			var escaped context.Context
			err = owner.WithHistoricalSync(ctx, 16000, func(run context.Context, scope *HistoricalSync) error {
				_, err := owner.HistoricalGroupWithRefetch(run, multiCP(1, 4), events, nil, func(bound context.Context, actual []Object) error {
					refetches++
					escaped = bound
					if reflect.ValueOf(actual).Pointer() != reflect.ValueOf(events).Pointer() || reflect.ValueOf(actual[0]).Pointer() != reflect.ValueOf(events[0]).Pointer() {
						return errors.New("refetch did not receive actual group")
					}
					if owner.Service().Metrics().Completed["Token:block"] != 0 {
						return errors.New("completed metric not restored before refetch")
					}
					// Source index.ts:388-392 and 835-846 preserve learned fields
					// but restart column sampling on genuine missing-field access.
					// EventCount is separate and retains the first successful attempt.
					profile := owner.Service().ColumnAccessPattern()["Token:block"]
					if owner.Service().EventCount()["Token:block"] != 1 || profile.Count != 0 || profile.Resolved || !reflect.DeepEqual(profile.Block, []string{"number", "gasUsed"}) {
						return fmt.Errorf("missing-field learning/reset policy: events=%v profile=%+v", owner.Service().EventCount(), profile)
					}
					if include := cb["filter"].(Object)["include"]; !reflect.DeepEqual(include, DefaultFilterInclude("block", false)) {
						return fmt.Errorf("missing-field access did not restore default include: %v", include)
					}
					for _, id := range []string{"3", "4"} {
						row, err := owner.Database().Find(bound, "account", store.Row{"id": id})
						if err != nil || row != nil {
							return fmt.Errorf("actual bound row/buffer not cleared before refetch: %v %v", row, err)
						}
					}
					if err := scope.WithTransaction(bound, func(sync *store.SourceSyncStore) error {
						return sync.InsertRPCRawRequestResults(bound, 1, []store.SourceRPCRawResult{{Request: request, Result: `"0x2a"`}})
					}); err != nil {
						return err
					}
					actual[1]["event"].(Object)["block"].(Object)["gasUsed"] = big.NewInt(21)
					return nil
				})
				return err
			})
			var invalid *InvalidEventAccessError
			if !errors.As(err, &invalid) || invalid.Key != "block.gasUsed" || calls != 2 || refetches != 1 {
				t.Fatal("wrong refetch boundary", calls, refetches, err)
			}
			rows, err := owner.storage.BusinessActions().Select(ctx, "account")
			if err != nil || len(rows) != 0 {
				t.Fatal("native business rollback failed", rows, err)
			}
			after, err := owner.HistoricalSnapshot(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed group advanced progress", after, err)
			}
			if _, err := owner.Database().Find(escaped, "account", store.Row{"id": "3"}); err == nil {
				t.Fatal("failed callback context escaped")
			}
			values, err := owner.storage.SyncCache().GetSourceRPCRawRequestResults(ctx, 1, []any{request})
			if err != nil || len(values) != 1 || values[0] == nil || *values[0] != `"0x2a"` {
				t.Fatal("refetch sync commit was rolled back with business", values, err)
			}
			applied, err := owner.HistoricalGroup(ctx, multiCP(1, 4), events, nil)
			if err != nil || !applied || calls != 4 {
				t.Fatal("explicit retry failed", applied, calls, err)
			}
			if owner.Service().Metrics().Completed["Token:block"] != 2 || owner.Service().EventCount()["Token:block"] != 3 || owner.Service().ColumnAccessPattern()["Token:block"].Count != 2 {
				t.Fatal("successful replay count policy", owner.Service().Metrics(), owner.Service().EventCount(), owner.Service().ColumnAccessPattern())
			}
			if applied, err = owner.HistoricalGroup(ctx, multiCP(1, 4), events, nil); err != nil || applied || calls != 4 {
				t.Fatal("durable replay repeated handlers", applied, calls, err)
			}
			if err := owner.Close(ctx); err != nil {
				t.Fatal(err)
			}
			owner = f.start(t)
			values, err = owner.storage.SyncCache().GetSourceRPCRawRequestResults(ctx, 1, []any{request})
			if err != nil || len(values) != 1 || values[0] == nil || *values[0] != `"0x2a"` {
				t.Fatal("reopen lost independently committed refetch", values, err)
			}
		})
	}
}
