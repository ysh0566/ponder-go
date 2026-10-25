package runtime

import (
	"context"
	"errors"
	"testing"

	"ponder.local/ponder-go/store"
)

type sourceLocalCapabilityControl struct {
	*store.SourceSyncStore
	fail  error
	bound *store.SourceSyncStore
}

func (s *sourceLocalCapabilityControl) WithTransaction(ctx context.Context, fn func(*store.SourceSyncStore) error) error {
	if s.fail != nil {
		return s.fail
	}
	return fn(s.bound)
}
func TestSourceLocalStoreCapability(t *testing.T) {
	var concrete *store.SourceSyncStore
	var wrapper *sourceLocalCapabilityControl
	for _, s := range []SourceLocalHistoricalStore{nil, concrete, wrapper} {
		if !sourceLocalStoreMissing(s) {
			t.Fatal("typed nil store accepted")
		}
	}
	bound := &store.SourceSyncStore{}
	wrapped := &sourceLocalCapabilityControl{bound: bound}
	var s SourceLocalHistoricalStore = wrapped
	if sourceLocalStoreMissing(s) {
		t.Fatal("present store rejected")
	}
	marker := errors.New("transaction boundary error")
	if err := s.WithTransaction(context.Background(), func(actual *store.SourceSyncStore) error {
		if actual != bound {
			t.Fatal("bound pointer identity changed")
		}
		return marker
	}); !errors.Is(err, marker) {
		t.Fatal("callback error lost", err)
	}
	wrapped.fail = marker
	called := false
	if err := s.WithTransaction(context.Background(), func(*store.SourceSyncStore) error { called = true; return nil }); !errors.Is(err, marker) || called {
		t.Fatal("transaction failure not preserved", err)
	}
}
