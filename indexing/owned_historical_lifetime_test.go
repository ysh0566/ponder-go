package indexing

// Supplemental permit tests, zero upstream-original credit; not actual SQL evidence.
// The separate real-SQL diagnostic overlay remains required before acceptance.

import (
	"context"
	"errors"
	"testing"
)

func draftPersistenceScope(parent context.Context) (*HistoricalSync, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s := &HistoricalSync{ctx: ctx, cancel: cancel, gate: newOwnedGate()}
	bound := context.WithValue(ctx, historicalScopeKey{}, s)
	s.token = &historicalPersistenceToken{Context: bound, scope: s}
	return s, bound
}

func TestHistoricalPersistencePermitTokens(t *testing.T) {
	s, schedule := draftPersistenceScope(context.Background())
	defer s.cancel()
	other, otherSchedule := draftPersistenceScope(context.Background())
	defer other.cancel()
	called := false
	fn := func(context.Context, context.Context) error { called = true; return nil }
	for _, token := range []context.Context{nil, context.Background(), other.PersistenceContext(), context.WithoutCancel(s.PersistenceContext()), context.WithValue(s.PersistenceContext(), "fake", true)} {
		if err := s.WithHistoricalPersistenceContext(schedule, token, fn); err == nil || called {
			t.Fatal("foreign/missing/derived token entered persistence", err, called)
		}
	}
	if err := s.WithHistoricalPersistenceContext(otherSchedule, s.PersistenceContext(), fn); err == nil || called {
		t.Fatal("foreign schedule entered persistence", err, called)
	}
	if err := s.WithHistoricalPersistenceContext(schedule, s.PersistenceContext(), fn); err != nil || !called {
		t.Fatal("canonical owner token rejected", err, called)
	}
	s.sealed.Store(true)
	called = false
	if err := s.WithHistoricalPersistenceContext(schedule, s.PersistenceContext(), fn); err == nil || called {
		t.Fatal("sealed token entered persistence", err, called)
	}
}

func TestHistoricalPersistencePermitSoftAndHardCancellation(t *testing.T) {
	s, root := draftPersistenceScope(context.Background())
	defer s.cancel()
	schedule, softStop := context.WithCancel(root)
	defer softStop()
	marker := errors.New("ordinary callback error")
	err := s.WithHistoricalPersistenceContext(schedule, s.PersistenceContext(), func(ioCtx, workCtx context.Context) error {
		softStop()
		if ioCtx.Err() != nil || !errors.Is(workCtx.Err(), context.Canceled) {
			t.Fatal("soft stop altered IO lifetime", ioCtx.Err(), workCtx.Err())
		}
		_, release, err := s.begin(ioCtx)
		if err != nil {
			t.Fatal("admitted SQL child was cancelled by soft stop", err)
		}
		release()
		if _, _, err := s.begin(workCtx); !errors.Is(err, context.Canceled) {
			t.Fatal("soft-stopped RPC child admitted", err)
		}
		return marker
	})
	if !errors.Is(err, marker) || errors.Is(err, context.Canceled) || s.ctx.Err() != nil {
		t.Fatal("ordinary error was turned into shutdown", err, s.ctx.Err())
	}
	err = s.WithHistoricalPersistenceContext(root, s.PersistenceContext(), func(ioCtx, workCtx context.Context) error {
		s.cancel() // Same hard lifetime cancel invoked by actual Owned.Close.
		<-ioCtx.Done()
		<-workCtx.Done()
		if !errors.Is(ioCtx.Err(), context.Canceled) || !errors.Is(workCtx.Err(), context.Canceled) {
			t.Fatal("hard cancellation lost")
		}
		return ioCtx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHistoricalPersistencePermitSealChildren(t *testing.T) {
	s, schedule := draftPersistenceScope(context.Background())
	defer s.cancel()
	var escaped context.Context
	err := s.WithHistoricalPersistenceContext(schedule, s.PersistenceContext(), func(ioCtx, workCtx context.Context) error {
		escaped = ioCtx
		s.lifecycleMu.Lock()
		s.sealed.Store(true)
		s.lifecycleMu.Unlock()
		if err := s.WithHistoricalPersistenceContext(schedule, s.PersistenceContext(), func(context.Context, context.Context) error { t.Fatal("new root admitted after seal"); return nil }); err == nil {
			t.Fatal("sealed root accepted")
		}
		if _, _, err := s.begin(schedule); err == nil {
			t.Fatal("unpermitted operation admitted after seal")
		}
		for _, child := range []context.Context{ioCtx, workCtx} {
			_, release, err := s.begin(child)
			if err != nil {
				t.Fatal("already-permitted child rejected by root seal", err)
			}
			release()
		}
		if ioCtx.Err() != nil || workCtx.Err() != nil {
			t.Fatal("seal cancelled admitted context")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.begin(escaped); err == nil {
		t.Fatal("completed lease was reusable")
	}
	waitOwnedGate(s.gate)
	s.gate.mu.Lock()
	pending := len(s.gate.pending)
	s.gate.mu.Unlock()
	if pending != 0 {
		t.Fatal("joined root retained descendants", pending)
	}
}
