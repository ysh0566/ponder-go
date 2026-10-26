package runtime

// Supplemental implementation-contract tests, zero upstream-original credit.
// The refusing provider is a boundary spy, not an implementation of the real
// owner's token authentication. Actual owner/lease tests live in indexing.
import (
	"context"
	"errors"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

type persistenceBoundaryStore struct{ calls int }

func (s *persistenceBoundaryStore) GetEventData(context.Context, store.SourceEventDataArgs) (store.SourceEventData, error) {
	s.calls++
	return store.SourceEventData{}, errors.New("unexpected boundary-spy event read")
}
func (s *persistenceBoundaryStore) GetSafeCrashRecoveryBlock(context.Context, uint64, int64) (*store.SourceRecoveryBlock, error) {
	s.calls++
	return nil, errors.New("unexpected boundary-spy recovery read")
}
func (s *persistenceBoundaryStore) WithTransaction(context.Context, func(*store.SourceSyncStore) error) error {
	s.calls++
	return errors.New("unexpected boundary-spy transaction")
}

type persistenceBoundaryProvider struct {
	*persistenceBoundaryStore
	calls           int
	schedule, token context.Context
	io, work        context.Context
	reject          error
}

func (p *persistenceBoundaryProvider) WithHistoricalPersistenceContext(schedule, token context.Context, fn func(context.Context, context.Context) error) error {
	p.calls++
	p.schedule = schedule
	p.token = token
	if p.reject != nil {
		return p.reject
	}
	return fn(p.io, p.work)
}

func TestSourceHistoricalPersistenceStandaloneIdentity(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "live"
		if cancelled {
			name = "already_cancelled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			marker := errors.New("exact legacy callback error")
			calls := 0
			database := &persistenceBoundaryStore{}
			err := sourceWithHistoricalPersistence(ctx, database, nil, func(io, work context.Context) error {
				calls++
				if io != ctx || work != ctx {
					t.Error("standalone replaced original context")
				}
				return marker
			})
			if err != marker || calls != 1 || database.calls != 0 {
				t.Fatalf("legacy identity changed: error=%v callback=%d store=%d", err, calls, database.calls)
			}
			if err = sourceValidateHistoricalPersistence(ctx, database, nil); err != nil {
				t.Fatalf("legacy validation introduced new cancellation behavior: %v", err)
			}
		})
	}
}

func TestSourceHistoricalPersistenceProviderBoundary(t *testing.T) {
	schedule := context.WithValue(context.Background(), struct{ label string }{"schedule"}, 1)
	token := context.WithValue(context.Background(), struct{ label string }{"token"}, 1)
	io := context.WithValue(context.Background(), struct{ label string }{"io"}, 1)
	work := context.WithValue(context.Background(), struct{ label string }{"work"}, 1)
	marker := errors.New("exact delegated callback error")
	t.Run("token_without_provider", func(t *testing.T) {
		called := false
		err := sourceWithHistoricalPersistence(schedule, &persistenceBoundaryStore{}, token, func(context.Context, context.Context) error { called = true; return nil })
		if err == nil || called {
			t.Fatalf("token silently ignored: error=%v callback=%v", err, called)
		}
	})
	t.Run("provider_without_token", func(t *testing.T) {
		p := &persistenceBoundaryProvider{persistenceBoundaryStore: &persistenceBoundaryStore{}}
		called := false
		err := sourceWithHistoricalPersistence(schedule, p, nil, func(context.Context, context.Context) error { called = true; return nil })
		if err == nil || called || p.calls != 0 {
			t.Fatalf("missing token delegated: error=%v callback=%v provider=%d", err, called, p.calls)
		}
	})
	t.Run("exact_provider_contexts_and_error", func(t *testing.T) {
		p := &persistenceBoundaryProvider{persistenceBoundaryStore: &persistenceBoundaryStore{}, io: io, work: work}
		called := 0
		err := sourceWithHistoricalPersistence(schedule, p, token, func(actualIO, actualWork context.Context) error {
			called++
			if actualIO != io || actualWork != work {
				t.Error("provider contexts replaced or conflated")
			}
			return marker
		})
		if err != marker || called != 1 || p.calls != 1 || p.schedule != schedule || p.token != token || p.persistenceBoundaryStore.calls != 0 {
			t.Fatalf("provider delegation changed: error=%v callback=%d provider=%d", err, called, p.calls)
		}
	})
	t.Run("provider_rejection_is_exact", func(t *testing.T) {
		p := &persistenceBoundaryProvider{persistenceBoundaryStore: &persistenceBoundaryStore{}, reject: marker}
		called := false
		err := sourceWithHistoricalPersistence(schedule, p, token, func(context.Context, context.Context) error { called = true; return nil })
		if err != marker || called || p.calls != 1 {
			t.Fatalf("provider rejection lost: error=%v callback=%v provider=%d", err, called, p.calls)
		}
	})
}

func persistenceEmptyOptions(database SourceLocalHistoricalStore, token context.Context) SourceLocalHistoricalOptions {
	return SourceLocalHistoricalOptions{
		ChainID: 1, Store: database, PersistenceContext: token, Children: NewSourceHistoricalChildren(nil),
		Progress: &SourceSyncProgress{ChainID: 1, Start: filter.Object{"number": "0x2"}, Finalized: filter.Object{"number": "0x1"}},
		From:     checkpoint.MustEncode(checkpoint.Block(1, 1, 2, false)), To: checkpoint.MustEncode(checkpoint.Block(1, 1, 1, true)),
	}
}
func TestSourceHistoricalPersistenceEarlyPathsValidate(t *testing.T) {
	type entrypoint struct {
		name string
		run  func(context.Context, SourceLocalHistoricalOptions) error
	}
	entries := []entrypoint{
		{"local_sync_empty", func(ctx context.Context, o SourceLocalHistoricalOptions) error {
			for _, err := range LocalSourceSync(ctx, o) {
				if err != nil {
					return err
				}
			}
			return nil
		}},
		{"local_sync_cached_end", func(ctx context.Context, o SourceLocalHistoricalOptions) error {
			o.Progress.Start = filter.Object{"number": "0x1"}
			o.Progress.Finalized = filter.Object{"number": "0x2"}
			o.Progress.Current = o.Progress.Finalized
			for _, err := range LocalSourceSync(ctx, o) {
				if err != nil {
					return err
				}
			}
			return nil
		}},
		{"local_events_empty", func(ctx context.Context, o SourceLocalHistoricalOptions) error {
			for _, err := range LocalSourceEvents(ctx, o) {
				if err != nil {
					return err
				}
			}
			return nil
		}},
		{"local_in_memory_empty", func(ctx context.Context, o SourceLocalHistoricalOptions) error {
			for _, err := range LocalSourceInMemoryEvents(ctx, o) {
				if err != nil {
					return err
				}
			}
			return nil
		}},
		{"refetch_empty", func(ctx context.Context, o SourceLocalHistoricalOptions) error {
			_, err := RefetchLocalSourceEvents(ctx, SourceHistoryRefetchOptions{Chain: protocol.Object{"id": uint64(1)}, Children: o.Children, Store: o.Store, PersistenceContext: o.PersistenceContext})
			return err
		}},
		{"multichain_empty", func(ctx context.Context, o SourceLocalHistoricalOptions) error {
			c := SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{{Chain: protocol.Object{"id": uint64(1)}, Local: o}}}
			for _, err := range HistoricalSourceMultichain(ctx, c) {
				if err != nil {
					return err
				}
			}
			return nil
		}},
		{"isolated_empty", func(ctx context.Context, o SourceLocalHistoricalOptions) error {
			c := SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{{Chain: protocol.Object{"id": uint64(1)}, Local: o}}}
			for _, err := range HistoricalSourceIsolated(ctx, c, 1) {
				if err != nil {
					return err
				}
			}
			return nil
		}},
		{"omnichain_empty", func(ctx context.Context, o SourceLocalHistoricalOptions) error {
			c := SourceHistoricalCoordinatorOptions{Chains: []SourceHistoricalChain{{Chain: protocol.Object{"id": uint64(1)}, Local: o}}}
			for _, err := range HistoricalSourceOmnichain(ctx, c) {
				if err != nil {
					return err
				}
			}
			return nil
		}},
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			for _, missing := range []bool{false, true} {
				name := "rejected_token"
				if missing {
					name = "missing_token"
				}
				t.Run(name, func(t *testing.T) {
					marker := errors.New("boundary provider rejected canonical admission")
					p := &persistenceBoundaryProvider{persistenceBoundaryStore: &persistenceBoundaryStore{}, reject: marker}
					ctx := context.Background()
					var token context.Context = context.WithValue(ctx, struct{}{}, 1)
					if missing {
						token = nil
					}
					err := entry.run(ctx, persistenceEmptyOptions(p, token))
					if missing {
						if err == nil || err.Error() != "owned historical persistence requires its scope token" || p.calls != 0 {
							t.Fatalf("missing token accepted or delegated: %v calls=%d", err, p.calls)
						}
					} else {
						if err != marker || p.calls != 1 || p.schedule != ctx || p.token != token {
							t.Fatalf("early path bypassed exact provider rejection: %v calls=%d", err, p.calls)
						}
					}
					if p.persistenceBoundaryStore.calls != 0 {
						t.Fatalf("storage reached before admission: %d", p.persistenceBoundaryStore.calls)
					}
				})
			}
		})
	}
}

func TestSourceHistoricalPersistenceAuthorizedEmptySync(t *testing.T) {
	for _, cached := range []bool{false, true} {
		name := "empty_range"
		if cached {
			name = "cached_end"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			token := context.WithValue(ctx, struct{}{}, 1)
			p := &persistenceBoundaryProvider{persistenceBoundaryStore: &persistenceBoundaryStore{}, io: ctx, work: ctx}
			o := persistenceEmptyOptions(p, token)
			if cached {
				o.Progress.Start = filter.Object{"number": "0x1"}
				o.Progress.Finalized = filter.Object{"number": "0x2"}
				o.Progress.Current = o.Progress.Finalized
			}
			count := 0
			for n, err := range LocalSourceSync(ctx, o) {
				if err != nil {
					t.Fatal(err)
				}
				count++
				if !cached || n != 2 {
					t.Fatalf("unexpected sync yield %v", n)
				}
			}
			want := 0
			if cached {
				want = 1
			}
			if count != want || p.calls != 1 || p.persistenceBoundaryStore.calls != 0 {
				t.Fatalf("authorized no-I/O branch changed: yields=%d provider=%d storage=%d", count, p.calls, p.persistenceBoundaryStore.calls)
			}
			if !cached && o.Progress.Current != nil { // Maps cannot be compared directly; observe their exact shared mutation.
				o.Progress.Current["identity_probe"] = true
				if o.Progress.Finalized["identity_probe"] != true {
					t.Error("empty range lost finalized/current map identity")
				}
			} else if !cached {
				t.Error("empty range did not publish finalized progress")
			}
		})
	}
}
