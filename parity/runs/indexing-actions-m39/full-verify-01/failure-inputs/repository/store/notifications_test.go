package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func waitNotification(t *testing.T, listener NotificationListener, channel, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, e := listener.Wait(ctx)
	if e != nil || n != (Notification{Channel: channel, Payload: payload}) {
		t.Fatalf("notification got %+v, %v; want %q/%q", n, e, channel, payload)
	}
}
func expectNoNotification(t *testing.T, listener NotificationListener) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if n, e := listener.Wait(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("unexpected notification or transport error: %+v %v", n, e)
	}
}

// Supplementary native transport checks, separate from source parity credit.
func TestDatabaseNotifications(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"transaction rollback and commit", "shared listener lifecycle", "slow consumers and channel routing", "subscription cancellation", "backend close wakes waiters"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					b := sourceBackend(t, ctx, dialect)
					channel := fmt.Sprintf("task_notify_%x", time.Now().UnixNano())
					listener, e := Listen(ctx, b, channel)
					if e != nil {
						t.Fatal(e)
					}
					defer listener.Close()
					switch name {
					case "transaction rollback and commit":
						tx, e := b.Begin(ctx)
						if e != nil {
							t.Fatal(e)
						}
						if e = tx.Exec(ctx, "SELECT pg_notify($1,$2)", channel, "rolled back"); e != nil {
							t.Fatal(e)
						}
						if e = tx.Rollback(ctx); e != nil {
							t.Fatal(e)
						}
						expectNoNotification(t, listener)
						tx, e = b.Begin(ctx)
						if e != nil {
							t.Fatal(e)
						}
						if e = tx.Exec(ctx, "SELECT pg_notify($1,$2)", channel, "committed"); e != nil {
							t.Fatal(e)
						}
						if e = tx.Commit(ctx); e != nil {
							t.Fatal(e)
						}
						waitNotification(t, listener, channel, "committed")
					case "shared listener lifecycle":
						second, e := Listen(ctx, b, channel)
						if e != nil {
							t.Fatal(e)
						}
						defer second.Close()
						if e = b.Exec(ctx, "SELECT pg_notify($1,$2)", channel, "both"); e != nil {
							t.Fatal(e)
						}
						waitNotification(t, listener, channel, "both")
						waitNotification(t, second, channel, "both")
						if e = listener.Close(); e != nil {
							t.Fatal(e)
						}
						if e = b.Exec(ctx, "SELECT pg_notify($1,$2)", channel, "remaining"); e != nil {
							t.Fatal(e)
						}
						waitNotification(t, second, channel, "remaining")
						if e = second.Close(); e != nil {
							t.Fatal(e)
						}
						third, e := Listen(ctx, b, channel)
						if e != nil {
							t.Fatal(e)
						}
						defer third.Close()
						if e = b.Exec(ctx, "SELECT pg_notify($1,$2)", channel, "resubscribed"); e != nil {
							t.Fatal(e)
						}
						waitNotification(t, third, channel, "resubscribed")
					case "slow consumers and channel routing":
						otherChannel := `unicode_界_"_channel`
						other, e := Listen(ctx, b, otherChannel)
						if e != nil {
							t.Fatal(e)
						}
						defer other.Close()
						if e = b.Exec(ctx, "SELECT pg_notify($1,i::text) FROM generate_series(1,300) AS i", channel); e != nil {
							t.Fatal(e)
						}
						if e = b.Exec(ctx, "SELECT pg_notify($1,$2)", otherChannel, "界\n\"escaped"); e != nil {
							t.Fatal(e)
						}
						for i := 1; i <= 300; i++ {
							waitNotification(t, listener, channel, fmt.Sprint(i))
						}
						waitNotification(t, other, otherChannel, "界\n\"escaped")
						r, e := b.Query(ctx, "SELECT 42 AS answer")
						if e != nil || fmt.Sprint(r.Rows[0]["answer"]) != "42" {
							t.Fatalf("SQL/notification framing corrupted: %+v %v", r, e)
						}
					case "subscription cancellation":
						lifetime, cancel := context.WithCancel(ctx)
						second, e := Listen(lifetime, b, channel)
						if e != nil {
							cancel()
							t.Fatal(e)
						}
						defer second.Close()
						cancel()
						deadline, stop := context.WithTimeout(ctx, 5*time.Second)
						defer stop()
						if _, e = second.Wait(deadline); !errors.Is(e, context.Canceled) {
							t.Fatalf("subscription lifetime: %v", e)
						}
						if e = b.Exec(ctx, "SELECT pg_notify($1,$2)", channel, "alive"); e != nil {
							t.Fatal(e)
						}
						waitNotification(t, listener, channel, "alive")
					case "backend close wakes waiters":
						results := make(chan error, 2)
						for i := 0; i < 2; i++ {
							go func() {
								deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
								defer cancel()
								_, e := listener.Wait(deadline)
								results <- e
							}()
						}
						if e = b.Close(); e != nil {
							t.Fatal(e)
						}
						for i := 0; i < 2; i++ {
							if e = <-results; e == nil || errors.Is(e, context.DeadlineExceeded) {
								t.Fatalf("waiter did not wake on close: %v", e)
							}
						}
					}
				})
			}
		})
	}
}
