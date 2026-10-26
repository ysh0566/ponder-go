package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/filter"
)

type snapshotQueryTransaction struct {
	mu      sync.Mutex
	queries []string
	entered chan struct{}
	release chan struct{}
}

func (t *snapshotQueryTransaction) Query(ctx context.Context, q string, _ ...any) (Result, error) {
	t.mu.Lock()
	first := len(t.queries) == 0
	t.queries = append(t.queries, q)
	t.mu.Unlock()
	if first {
		close(t.entered)
		select {
		case <-t.release:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return Result{Rows: []map[string]any{}}, nil
}
func (*snapshotQueryTransaction) Exec(context.Context, string, ...any) error { return nil }
func (*snapshotQueryTransaction) Commit(context.Context) error               { return nil }
func (*snapshotQueryTransaction) Rollback(context.Context) error             { return nil }

// This uses actual GetEventData query planning with a transaction admission
// gate, not a database substitute or a claim of real backend coverage.
func TestSourceEventDataSingleFilterSnapshot(t *testing.T) {
	session := &filter.Session{}
	f := filter.Object{"type": "log", "chainId": 1, "include": []any{"block.number", "transaction.gas"}}
	tx := &snapshotQueryTransaction{entered: make(chan struct{}), release: make(chan struct{})}
	source := &SourceSyncStore{cache: &ProtocolCache{namespace: "snapshot"}, bound: &sourceSyncTransaction{tx: tx}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := source.GetEventData(ctx, SourceEventDataArgs{FilterSession: session, Filters: []filter.Object{f}, ChainID: 1, FromBlock: 0, ToBlock: 1, Limit: 10})
		done <- err
	}()
	select {
	case <-tx.entered:
	case <-ctx.Done():
		t.Fatal("initial SQL was not admitted")
	}
	updated := make(chan struct{})
	go func() {
		session.Update(func() { f["include"].([]any)[1] = "transaction.gasPrice"; f["extra"] = "during SQL" })
		close(updated)
	}()
	select {
	case <-updated:
	case <-ctx.Done():
		close(tx.release)
		t.Fatal("filter lock held across SQL")
	}
	close(tx.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tx.mu.Lock()
	queries := append([]string{}, tx.queries...)
	tx.mu.Unlock()
	if len(queries) != 3 || !strings.Contains(queries[1], `"gas" as "gas"`) || strings.Contains(queries[1], `"gas_price"`) {
		t.Fatal("later SQL observed learning mid-page", queries)
	}
	if _, err := source.GetEventData(ctx, SourceEventDataArgs{FilterSession: session, Filters: []filter.Object{f}, ChainID: 1, FromBlock: 0, ToBlock: 1, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	tx.mu.Lock()
	next := append([]string{}, tx.queries[3:]...)
	tx.mu.Unlock()
	if len(next) != 3 || !strings.Contains(next[1], `"gas_price" as "gasPrice"`) {
		t.Fatal("next page did not observe learned snapshot", next)
	}
}
