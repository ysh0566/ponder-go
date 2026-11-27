package store

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// Observing Done allows the test to cancel after the request enters the wait,
// while another transaction still owns the only database connection.
type observedPGliteWait struct {
	context.Context
	waiting chan struct{}
}

func (c observedPGliteWait) Done() <-chan struct{} {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestPGliteQueuedCancellation(t *testing.T) {
	ctx := t.Context()
	db, err := OpenPGlite(ctx, "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Exec(ctx, "CREATE TABLE queue_test(id int primary key)"); err != nil {
		t.Fatal(err)
	}
	owner, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Rollback(context.Background())
	if err := owner.Exec(ctx, "INSERT INTO queue_test VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"query", func(c context.Context) error {
			_, err := db.Query(c, "INSERT INTO queue_test VALUES (2) RETURNING id")
			return err
		}},
		{"exec", func(c context.Context) error {
			return db.Exec(c, "INSERT INTO queue_test VALUES (3)")
		}},
		{"begin", func(c context.Context) error {
			tx, err := db.Begin(c)
			if err == nil {
				tx.Rollback(context.Background())
			}
			return err
		}},
		{"listen", func(c context.Context) error {
			l, err := Listen(c, db, "queue_test")
			if err == nil {
				l.Close()
			}
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pending, cancel := context.WithCancel(ctx)
			defer cancel()
			waiting := make(chan struct{}, 1)
			result := make(chan error, 1)
			go func() {
				result <- tc.run(observedPGliteWait{pending, waiting})
			}()
			select {
			case <-waiting:
				cancel()
			case <-time.After(3 * time.Second):
				t.Fatal("queued operation did not observe its context")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("queued operation returned %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("queued operation waited for the active transaction")
			}
		})
	}

	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- db.Exec(deadline, "INSERT INTO queue_test VALUES (4)") }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued exec returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued exec stayed blocked past its deadline")
	}
	if err := owner.Exec(ctx, "INSERT INTO queue_test VALUES (5)"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, "SELECT array_agg(id ORDER BY id)::text AS ids FROM queue_test")
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["ids"] != "{1,5}" {
		t.Fatalf("rows after cancellation %v, error %v", rows, err)
	}
	l, err := Listen(ctx, db, "queue_test")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := db.Exec(ctx, "SELECT pg_notify('queue_test', 'available')"); err != nil {
		t.Fatal(err)
	}
	waitNotification(t, l, "queue_test", "available")
}

// Encoding is part of the actual wire operation. The hook gives the test a
// deterministic point after the transaction starts a request but before send.
type pgliteTestParam func()

func (f pgliteTestParam) MarshalJSON() ([]byte, error) {
	f()
	return []byte(`"1"`), nil
}

func TestPGliteTransactionQueue(t *testing.T) {
	ctx := t.Context()
	db, err := OpenPGlite(ctx, "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Exec(ctx, "CREATE TABLE queue_test(id int)"); err != nil {
		t.Fatal(err)
	}
	assertIDs := func(t *testing.T, want string) {
		t.Helper()
		rows, err := db.Query(ctx, "SELECT coalesce(array_agg(id ORDER BY id), '{}'::int[])::text AS ids FROM queue_test")
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["ids"] != want {
			t.Fatalf("rows %v, want ids %s, error %v", rows, want, err)
		}
	}
	t.Run("canceled during encoding", func(t *testing.T) {
		request, cancel := context.WithCancel(ctx)
		defer cancel()
		err := db.Exec(request, "INSERT INTO queue_test VALUES ($1)", pgliteTestParam(cancel))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled argument encoding returned %v", err)
		}
		assertIDs(t, "{}")
	})

	for _, finish := range []string{"commit", "canceled commit"} {
		t.Run(finish, func(t *testing.T) {
			if err := db.Exec(ctx, "DELETE FROM queue_test"); err != nil {
				t.Fatal(err)
			}
			tx, err := db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			encoding, resume := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(resume) })
			defer release()
			first := make(chan error, 1)
			go func() {
				param := pgliteTestParam(func() { close(encoding); <-resume })
				first <- tx.Exec(ctx, "INSERT INTO queue_test VALUES ($1)", param)
			}()
			select {
			case <-encoding:
			case <-time.After(3 * time.Second):
				t.Fatal("first statement did not enter encoding")
			}
			queued, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			second := make(chan error, 1)
			go func() {
				second <- tx.Exec(queued, "INSERT INTO queue_test VALUES (2)")
			}()
			select {
			case err := <-second:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("queued transaction statement returned %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("queued transaction statement stayed blocked past its deadline")
			}
			commit, stop := context.WithCancel(ctx)
			defer stop()
			if finish == "canceled commit" {
				stop()
			}
			result := make(chan error, 1)
			go func() { result <- tx.Commit(commit) }()
			release()
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, commit.Err()) {
				t.Fatalf("commit returned %v, want %v", err, commit.Err())
			}
			if finish == "canceled commit" {
				assertIDs(t, "{}")
			} else {
				assertIDs(t, "{1}")
			}
		})
	}
}

type cancelAfterPGliteWrite struct {
	io.WriteCloser
	cancel context.CancelFunc
}

func (w cancelAfterPGliteWrite) Write(data []byte) (int, error) {
	n, err := w.WriteCloser.Write(data)
	if err == nil {
		w.cancel()
	}
	return n, err
}

func TestPGliteSubmittedCommit(t *testing.T) {
	ctx := t.Context()
	db, err := OpenPGlite(ctx, "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := tx.Exec(ctx, "CREATE TABLE committed_after_send(id integer)"); err != nil {
		t.Fatal(err)
	}
	commit, cancel := context.WithCancel(ctx)
	defer cancel()
	p := db.(*pgliteBackend)
	in := p.in
	p.in = cancelAfterPGliteWrite{in, cancel}
	err = tx.Commit(commit)
	p.in = in
	if err != nil || !errors.Is(commit.Err(), context.Canceled) {
		t.Fatalf("submitted COMMIT returned %v, context %v", err, commit.Err())
	}
	row, err := db.Query(ctx, "SELECT count(*)::text AS n FROM committed_after_send")
	if err != nil || len(row.Rows) != 1 || row.Rows[0]["n"] != "0" {
		t.Fatalf("next query returned %v, error %v", row, err)
	}
}
