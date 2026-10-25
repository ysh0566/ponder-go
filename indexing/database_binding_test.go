package indexing

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"ponder.local/ponder-go/store"
)

// Pure capability tests deliberately do not simulate database semantics. Real
// SQL transaction/cache persistence is exercised by the owned-runtime fixtures.
func TestDatabaseBindingCapabilities(t *testing.T) {
	db := NewSessionDatabase(&store.IndexingSession{}, &ErrorState{})
	if !db.configured() || db.Underlying() != nil {
		t.Fatal("session exposed unscoped underlying handle")
	}
	unbound := context.Background()
	reject := func(ctx context.Context) {
		t.Helper()
		if _, release, err := db.acquire(ctx); err == nil {
			release()
			t.Fatal("accepted missing/stale/wrong-owner capability")
		}
	}
	reject(unbound)
	first := &DatabaseBinding{database: db, underlying: &store.IndexingStore{}}
	db.binding = first
	ctx := context.WithValue(unbound, databaseBindingKey{}, first)
	underlying, release, err := db.acquire(ctx)
	if err != nil || underlying != first.underlying {
		t.Fatal("lost immutable binding")
	}
	release()
	if _, _, err := db.Bind(ctx, nil); err == nil {
		t.Fatal("concurrent binding accepted")
	}
	other := NewSessionDatabase(&store.IndexingSession{}, &ErrorState{})
	if err := other.checkBinding(ctx); err == nil {
		t.Fatal("wrong owner accepted")
	}
	first.Revoke()
	if ctx.Err() != nil {
		t.Fatal("revocation cancelled independent cache context")
	}
	reject(ctx)
	second := &DatabaseBinding{database: db, underlying: &store.IndexingStore{}}
	db.binding = second
	secondCtx := context.WithValue(unbound, databaseBindingKey{}, second)
	reject(ctx)
	first.Revoke()
	if err := db.checkBinding(secondCtx); err != nil {
		t.Fatal("old revoke revoked new generation", err)
	}
	cancelCtx, cancel := context.WithCancel(secondCtx)
	cancel()
	if err := db.checkBinding(cancelCtx); !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation lost", err)
	}
	if err := db.SetProfileEvent(store.ProfileEvent{}); err == nil {
		t.Fatal("unscoped profile mutation accepted")
	}
	second.Revoke()
}

func TestDatabaseUnboundOperationsReject(t *testing.T) {
	db := NewSessionDatabase(&store.IndexingSession{}, &ErrorState{})
	ctx := context.Background()
	checks := []func() error{
		func() error { _, e := db.Find(ctx, "item", nil); return e },
		func() error { _, e := db.InsertOne(ctx, "item", nil, store.Conflict{}); return e },
		func() error { _, e := db.InsertMany(ctx, "item", nil, store.Conflict{}); return e },
		func() error { _, e := db.Update(ctx, "item", nil, nil); return e },
		func() error { _, e := db.UpdateFunc(ctx, "item", nil, nil); return e },
		func() error { _, e := db.Delete(ctx, "item", nil); return e },
		func() error { _, e := db.Select(ctx, "item", nil); return e },
		func() error { _, e := db.SQL(ctx, "SELECT 1"); return e },
		func() error { return db.Flush(ctx) },
		func() error { return db.Prefetch(ctx, nil) },
		func() error { return db.SetProfileEventContext(ctx, store.ProfileEvent{}) },
	}
	for i, check := range checks {
		if err := check(); err == nil {
			t.Fatalf("operation %d accepted unbound context", i)
		}
	}
}

// Revocation seals admission before joining in-flight operations. In particular,
// a callback nested in an admitted UpdateFunc must be able to observe revocation
// rather than deadlock behind a writer-held RWMutex.
func TestDatabaseBindingRevocationJoins(t *testing.T) {
	db := NewSessionDatabase(&store.IndexingSession{}, &ErrorState{})
	binding := &DatabaseBinding{database: db, underlying: &store.IndexingStore{}}
	db.binding = binding
	ctx := context.WithValue(context.Background(), databaseBindingKey{}, binding)
	_, release, err := db.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { binding.Revoke(); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		db.bindingMu.Lock()
		sealed := db.binding == nil
		db.bindingMu.Unlock()
		if sealed {
			break
		}
		if time.Now().After(deadline) {
			release()
			t.Fatal("revocation did not seal admission")
		}
		runtime.Gosched()
	}
	select {
	case <-done:
		release()
		t.Fatal("revocation did not join admitted operation")
	default:
	}
	if _, nestedRelease, e := db.acquire(ctx); e == nil {
		nestedRelease()
		release()
		t.Fatal("nested operation admitted after revocation")
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("revocation did not complete after join")
	}
	if ctx.Err() != nil {
		t.Fatal("revocation cancelled caller context")
	}
}
