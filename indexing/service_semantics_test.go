package indexing

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

// These are supplemental service contracts, not replay credit for source tests.
// Each service uses a live writer-owned IndexingStore (and its real cache).
func serviceSemanticStore(t *testing.T, test func(*testing.T, *Database, *ErrorState)) {
	t.Helper()
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			s := adapterStore(t, dialect)
			err := s.Apply(context.Background(), adapterEvent("service-semantics", 1), func(w *store.Writer) error {
				state := &ErrorState{}
				db := NewDatabase(w.Indexing(), state)
				if db.Underlying().Cache == nil {
					t.Fatal("missing real indexing cache")
				}
				test(t, db, state)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func semanticOptions(t *testing.T, db *Database, state *ErrorState, handler Handler) (Options, Object, []*balanceRequester) {
	t.Helper()
	chains := []Object{{"id": 1, "name": "one"}, {"id": 2, "name": "two"}}
	requesters := []*balanceRequester{{}, {}}
	clients, err := NewClientSet(chains, []rpc.Requester{requesters[0], requesters[1]})
	if err != nil {
		t.Fatal(err)
	}
	cb := Object{"name": "Token:Transfer", "fn": handler, "filter": Object{"type": "log", "include": DefaultFilterInclude("log", false)}}
	o := Options{Build: Build{Chains: chains, Contracts: []Object{{"Token": Object{"address": "one"}}, {"Token": Object{"address": "two"}}}, EventCallbacks: [][]Object{{cb}, {cb}}, IndexingFunctions: []Object{{"name": "Token:Transfer"}}}, Database: db, Clients: clients, Errors: state, Now: func() time.Time { return time.Unix(0, 0) }}
	return o, cb, requesters
}
func semanticEvent(chain, cb Object, number int64) Object {
	return Object{"type": "log", "chain": chain, "eventCallback": cb, "event": Object{"block": Object{"number": big.NewInt(number)}, "transaction": Object{"gas": big.NewInt(21000), "maxFeePerGas": big.NewInt(7)}, "args": Object{"amount": big.NewInt(1)}}}
}
func mustSemanticService(t *testing.T, o Options) *Service {
	t.Helper()
	s, e := NewService(o)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestServiceSupplementalTwoChainIdentityAndSetup(t *testing.T) {
	serviceSemanticStore(t, func(t *testing.T, db *Database, state *ErrorState) {
		var args *HandlerArgs
		var contextIdentity *Context
		var chainIdentity uintptr
		var retained EventView
		var transaction *PropertyView
		seenClients := map[int]*Client{}
		phase := 0
		var o Options
		var requesters []*balanceRequester
		check := func(a *HandlerArgs, chain int, tag string) error {
			if args == nil {
				args = a
				contextIdentity = a.Context
				chainIdentity = objectID(a.Context.Chain)
			}
			if args != a || contextIdentity != a.Context || chainIdentity != objectID(a.Context.Chain) || a.Context.DB != db {
				t.Fatal("stable args/context/chain/database identity lost")
			}
			if a.Context.Chain["id"] != chain || objectID(a.Context.Contracts) != objectID(o.Build.Contracts[chain-1]) {
				t.Fatal("chain/contracts not switched")
			}
			if prior := seenClients[chain]; prior != nil && prior != a.Context.Client {
				t.Fatal("client identity changed")
			}
			seenClients[chain] = a.Context.Client
			if _, e := a.Context.Client.GetBalance(context.Background(), "0xabc"); e != nil {
				return e
			}
			if requesters[chain-1].params[1] != tag {
				t.Fatalf("RPC tag %v want %s", requesters[chain-1].params, tag)
			}
			return nil
		}
		handler := Handler(func(_ context.Context, a *HandlerArgs) error {
			chain := 1
			if phase == 2 {
				chain = 2
			}
			if e := check(a, chain, "0x2"); e != nil {
				return e
			}
			p, ok := a.Event.Property("transaction")
			if !ok {
				t.Fatal("missing actual property facade")
			}
			if transaction != nil && transaction != p {
				t.Fatal("historical proxy identity changed")
			}
			transaction = p
			if _, e := p.Get("gas"); e != nil {
				return e
			}
			retained = a.Event
			return nil
		})
		var cb Object
		o, cb, requesters = semanticOptions(t, db, state, handler)
		setup := Object{"name": "Token:setup", "chain": o.Build.Chains[0], "block": 0, "fn": Handler(func(_ context.Context, a *HandlerArgs) error {
			if phase == 0 && a.Event != nil {
				t.Fatal("initial setup event must be undefined (nil facade)")
			}
			if phase == 4 && !a.Event.SameIdentity(retained) {
				t.Fatal("source setup-after-event must retain event reference")
			}
			return check(a, 1, "0x0")
		})}
		o.Build.SetupCallbacks = [][]Object{{setup}}
		s := mustSemanticService(t, o)
		if e := s.ProcessSetupEvents(context.Background()); e != nil {
			t.Fatal(e)
		}
		for _, p := range []int{1, 2, 3} {
			phase = p
			chain := 0
			if p == 2 {
				chain = 1
			}
			if e := s.ProcessHistoricalEvents(context.Background(), []Object{semanticEvent(o.Build.Chains[chain], cb, 2)}, nil); e != nil {
				t.Fatal(e)
			}
		}
		if seenClients[1] == seenClients[2] {
			t.Fatal("two chains share client")
		}
		phase = 4
		if e := s.ProcessSetupEvents(context.Background()); e != nil {
			t.Fatal(e)
		}
		if s.EventCount()["Token:Transfer"] != 3 || len(s.Metrics().Durations) != 5 {
			t.Fatal("dispatch counts/durations", s.EventCount(), s.Metrics())
		}
	})
}
func TestServiceSupplementalStickyErrorTiming(t *testing.T) {
	serviceSemanticStore(t, func(t *testing.T, db *Database, state *ErrorState) {
		for _, setup := range []bool{false, true} {
			for _, returned := range []bool{false, true} {
				state.ClearRetryableError()
				var caught error
				reported := 0
				o, cb, _ := semanticOptions(t, db, state, nil)
				fn := Handler(func(_ context.Context, a *HandlerArgs) error {
					if setup {
						p, e := NewPropertyView("transaction", NewColumnAccessPattern([]string{"Token:setup"}), state, func(string) {})
						if e != nil {
							t.Fatal(e)
						}
						p.Bind("Token:setup", Object{})
						_, caught = p.Get("maxPriorityFeePerGas")
					} else {
						p, ok := a.Event.Property("transaction")
						if !ok {
							t.Fatal("missing proxy")
						}
						_, caught = p.Get("maxPriorityFeePerGas")
					}
					if caught == nil {
						t.Fatal("expected genuine missing access")
					}
					if returned {
						return errors.New("ordinary callback failure")
					}
					return nil
				})
				cb["fn"] = fn
				o.OnError = func(ob ErrorObservation) {
					reported++
					if ob.Err != caught {
						t.Fatal("error observer identity changed")
					}
				}
				o.Build.SetupCallbacks = [][]Object{{{"name": "Token:setup", "chain": o.Build.Chains[0], "fn": fn}}}
				s := mustSemanticService(t, o)
				var err error
				if setup {
					err = s.ProcessSetupEvents(context.Background())
				} else {
					err = s.ProcessHistoricalEvents(context.Background(), []Object{semanticEvent(o.Build.Chains[0], cb, 2)}, nil)
				}
				if err != caught || state.GetRetryableError() != nil {
					t.Fatal("sticky error lost precedence/identity or not cleared", err, caught)
				}
				if s.Metrics().HasError != setup || reported != map[bool]int{true: 1, false: 0}[setup] {
					t.Fatal("invalid event access classification", setup, s.Metrics(), reported)
				}
				wantDuration := 0
				if !setup && !returned {
					wantDuration = 1
				}
				if len(s.Metrics().Durations) != wantDuration {
					t.Fatal("sticky duration order", setup, returned, s.Metrics())
				}
				if s.EventCount()["Token:Transfer"] != 0 {
					t.Fatal("failed handler counted")
				}
			}
		}
	})
}
func TestServiceSupplementalConvergenceAndInvalidation(t *testing.T) {
	serviceSemanticStore(t, func(t *testing.T, db *Database, state *ErrorState) {
		readNew := false
		var caught error
		resolved := 0
		o, cb, _ := semanticOptions(t, db, state, Handler(func(_ context.Context, a *HandlerArgs) error {
			p, _ := a.Event.Property("transaction")
			if _, e := p.Get("gas"); e != nil {
				return e
			}
			if _, e := p.Get("maxFeePerGas"); e != nil {
				return e
			}
			if readNew {
				_, caught = p.Get("maxPriorityFeePerGas")
				return caught
			}
			return nil
		}))
		o.OnResolved = func(counts map[string]int) {
			resolved++
			if counts["transaction"] != 2 || counts["total"] != 2 {
				t.Fatal(counts)
			}
		}
		s := mustSemanticService(t, o)
		events := make([]Object, 99)
		for i := range events {
			events[i] = semanticEvent(o.Build.Chains[0], cb, 2)
		}
		if e := s.ProcessHistoricalEvents(context.Background(), events, nil); e != nil {
			t.Fatal(e)
		}
		p := s.ColumnAccessPattern()["Token:Transfer"]
		if p.Resolved || p.Count != 99 || resolved != 0 {
			t.Fatal("premature convergence", p)
		}
		if e := s.ProcessHistoricalEvents(context.Background(), events[:1], nil); e != nil {
			t.Fatal(e)
		}
		want := []string{"transaction.gas", "transaction.maxFeePerGas", "log.address", "log.data", "log.logIndex", "log.removed", "log.topics", "transaction.transactionIndex", "transaction.from", "transaction.to", "transaction.hash", "transaction.type", "block.timestamp", "block.number", "block.hash"}
		f := cb["filter"].(Object)
		if !p.Resolved || p.Count != 100 || resolved != 1 || !reflect.DeepEqual(f["include"], want) {
			t.Fatal("convergence/include order", p, f, resolved)
		}
		readNew = true
		err := s.ProcessHistoricalEvents(context.Background(), events[:1], nil)
		if err != caught || caught == nil || p.Resolved || p.Count != 0 || !reflect.DeepEqual(f["include"], DefaultFilterInclude("log", false)) {
			t.Fatal("invalidation did not reset profile/include", err, p, f)
		}
		if !reflect.DeepEqual(p.Transaction, []string{"gas", "maxFeePerGas", "maxPriorityFeePerGas"}) || s.EventCount()["Token:Transfer"] != 100 {
			t.Fatal("access insertion order or failed count", p)
		}
	})
}
func TestServiceSupplementalCancellationAndReentrancy(t *testing.T) {
	serviceSemanticStore(t, func(t *testing.T, db *Database, state *ErrorState) {
		var s *Service
		calls := 0
		o, cb, _ := semanticOptions(t, db, state, Handler(func(ctx context.Context, _ *HandlerArgs) error {
			calls++
			if e := s.ProcessRealtimeEvents(ctx, nil); e == nil || !strings.Contains(e.Error(), "already active") {
				t.Fatal("reentrant dispatch accepted", e)
			}
			return nil
		}))
		s = mustSemanticService(t, o)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := s.ProcessHistoricalEvents(ctx, []Object{semanticEvent(o.Build.Chains[0], cb, 2)}, nil); !errors.Is(e, context.Canceled) || calls != 0 {
			t.Fatal("cancelled dispatch executed", e, calls)
		}
		for i := 0; i < 2; i++ {
			if e := s.ProcessHistoricalEvents(context.Background(), []Object{semanticEvent(o.Build.Chains[0], cb, 2)}, nil); e != nil {
				t.Fatal(e)
			}
		}
		if calls != 2 || s.Metrics().HasError {
			t.Fatal("guard not released or outer dispatch marked failed")
		}
	})
}
