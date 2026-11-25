package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Delay a single renewal before the database admits it. Other clients and the
// database clock keep running while the request is queued.
type heartbeatQueue struct {
	Backend
	queued            context.Context
	entered, released chan struct{}
}

func (q *heartbeatQueue) wait(ctx context.Context) {
	if ctx == q.queued {
		close(q.entered)
		select {
		case <-q.released:
		case <-ctx.Done():
		}
	}
}

func (q *heartbeatQueue) Begin(ctx context.Context) (Transaction, error) {
	q.wait(ctx)
	return q.Backend.Begin(ctx)
}

func (q *heartbeatQueue) Query(ctx context.Context, sql string, args ...any) (Result, error) {
	q.wait(ctx)
	return q.Backend.Query(ctx, sql, args...)
}

func TestLifecycleQueuedHeartbeat(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			for _, scenario := range []string{"newer renewal", "lease expires"} {
				t.Run(scenario, func(t *testing.T) {
					queued, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					q := &heartbeatQueue{Backend: b, queued: queued, entered: make(chan struct{}), released: make(chan struct{})}
					release := sync.OnceFunc(func() { close(q.released) })
					opts := LifecycleOptions{HeartbeatInterval: time.Hour, HeartbeatTimeout: 25 * time.Second}
					d := newTestLifecycle(t, q, fmt.Sprintf("ponder_go_life_queue_%x", time.Now().UnixNano()), opts)
					if _, err := d.Migrate(ctx, lifecycleMigration()); err != nil {
						t.Fatal(err)
					}
					done := make(chan error, 1)
					go func() { done <- d.Heartbeat(queued) }()
					defer release()
					select {
					case <-q.entered:
					case <-queued.Done():
						t.Fatal(queued.Err())
					}
					if scenario == "lease expires" {
						// The original request has already enqueued. Put the lease on
						// its expiration boundary, then let database time advance.
						err := b.Exec(ctx, "UPDATE "+d.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{heartbeat_at}',to_jsonb((floor(extract(epoch FROM clock_timestamp())*1000)::bigint - $1::bigint + 30))) WHERE key='app'", opts.HeartbeatTimeout.Milliseconds())
						if err != nil {
							t.Fatal(err)
						}
						time.Sleep(60 * time.Millisecond)
						release()
						if err := <-done; !errors.Is(err, ErrLeaseLost) {
							t.Fatalf("queued renewal after expiration returned %v", err)
						}
						if err := d.MarkReady(ctx); !errors.Is(err, ErrLeaseLost) {
							t.Fatalf("expired owner write returned %v", err)
						}
						return
					}
					time.Sleep(10 * time.Millisecond)
					if err := d.Heartbeat(ctx); err != nil {
						t.Fatal(err)
					}
					readHeartbeat := func() int64 {
						t.Helper()
						rows, err := d.Metadata(ctx)
						if err != nil || len(rows) != 1 {
							t.Fatalf("read heartbeat: %v %v", rows, err)
						}
						return rows[0].Value.HeartbeatAt
					}
					later := readHeartbeat()
					release()
					if err := <-done; err != nil {
						t.Fatal(err)
					}
					if got := readHeartbeat(); got < later {
						t.Fatalf("queued renewal moved heartbeat backwards from %d to %d", later, got)
					}
				})
			}
		})
	}
}
