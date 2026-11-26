package store

import (
	"context"
	"testing"
	"time"
)

func TestManagedPostgresReadCancellation(t *testing.T) {
	o, opts, open := liveDomainsFixture(t, "postgres")
	d := open(opts)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	pool, err := OpenPostgres(ctx, o.PostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	reader := d.BusinessReader()
	progress := d.storage.BusinessStore().internal("_ponder_go_progress")
	for _, tc := range []struct {
		name, lock string
		args       []any
		read       func(context.Context) error
	}{
		{"query", "LOCK TABLE " + progress + " IN ACCESS EXCLUSIVE MODE", nil, func(ctx context.Context) error {
			_, err := reader.QueryReadOnlyInNamespace(ctx, reader.Namespace(), "SELECT * FROM "+progress)
			return err
		}},
		{"business_audit", "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", []any{o.BusinessNamespace}, func(ctx context.Context) error {
			_, err := d.Control(ctx)
			return err
		}},
		{"sync_audit", "LOCK TABLE " + d.table("head") + " IN ACCESS EXCLUSIVE MODE", nil, func(ctx context.Context) error {
			_, err := d.Control(ctx)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if err := tx.Exec(ctx, tc.lock, tc.args...); err != nil {
				t.Fatal(err)
			}
			pid, err := tx.Query(ctx, "SELECT pg_backend_pid() AS pid")
			if err != nil {
				t.Fatal(err)
			}

			read, stop := context.WithCancel(ctx)
			defer stop()
			done := make(chan error, 1)
			go func() { done <- tc.read(read) }()
			for {
				blocked, err := pool.Query(ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid))) AS blocked", pid.Rows[0]["pid"])
				if err != nil {
					t.Fatal(err)
				}
				if blocked.Rows[0]["blocked"] == true {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("read returned before it blocked: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			stop()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("blocked read succeeded after cancellation")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
	liveDomainsSeed(t, d)
}
