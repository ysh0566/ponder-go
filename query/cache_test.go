package query

import (
	"context"
	"crypto/sha256"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"ponder.local/ponder-go/store"
)

func TestSQLCacheCancellation(t *testing.T) {
	cache := &resultCache{}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	load := func(ctx context.Context) (store.Result, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return store.Result{Rows: []map[string]any{{"v": 1}}}, nil
		case <-ctx.Done():
			return store.Result{}, ctx.Err()
		}
	}
	short, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, err := cache.read(short, "same", 0, true, load); first <- err }()
	<-started
	go func() { _, err := cache.read(t.Context(), "same", 0, true, load); second <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		cache.mu.Lock()
		joined := cache.entries[sha256.Sum256([]byte("same"))].pending.waiters == 2
		cache.mu.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second request did not join")
		}
		runtime.Gosched()
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-second; err != nil || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
}

func TestSQLCacheRetriedAfterLastReaderLeaves(t *testing.T) {
	cache := &resultCache{}
	ctx, cancel := context.WithCancel(t.Context())
	started, aborted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	caller := make(chan error, 1)
	go func() {
		_, err := cache.read(ctx, "query", 0, true, func(work context.Context) (store.Result, error) {
			close(started)
			<-work.Done()
			close(aborted)
			<-release
			return store.Result{}, work.Err()
		})
		caller <- err
	}()
	<-started
	cache.mu.Lock()
	old := cache.entries[sha256.Sum256([]byte("query"))].pending
	cache.mu.Unlock()
	cancel()
	<-aborted
	if err := <-caller; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	good := func(context.Context) (store.Result, error) {
		return store.Result{Rows: []map[string]any{{"v": 2}}}, nil
	}
	replacement, err := cache.read(t.Context(), "query", 0, true, good)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	<-old.done
	result, err := cache.read(t.Context(), "query", 0, true, func(context.Context) (store.Result, error) { return store.Result{}, errors.New("replacement evicted") })
	if err != nil || result != replacement {
		t.Fatal(result, err)
	}
	runtime.KeepAlive(replacement)
}

func TestSQLCacheBoundedAndErrorsAreRetryable(t *testing.T) {
	cache := &resultCache{}
	boom := errors.New("transient read")
	if _, err := cache.read(t.Context(), "q", 0, true, func(context.Context) (store.Result, error) { return store.Result{}, boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	ok := func(context.Context) (store.Result, error) { return store.Result{}, nil }
	if _, err := cache.read(t.Context(), "q", 0, true, ok); err != nil {
		t.Fatal(err)
	}
	for i := range maxCompletedQueries + 4 {
		if _, err := cache.read(t.Context(), string(rune('a'+i)), 0, true, ok); err != nil {
			t.Fatal(err)
		}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) > maxCompletedQueries || cache.order.Len() != len(cache.entries) {
		t.Fatal(len(cache.entries), cache.order.Len())
	}
}
