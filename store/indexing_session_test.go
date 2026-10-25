package store

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

func sessionUnitStore() *Store {
	return &Store{Schema: schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "text"}}}}}, profileState: NewCacheProfile()}
}
func sessionUnitWriter(s *Store) *Writer { return &Writer{s: s} }
func TestIndexingSessionOwnershipAndSnapshots(t *testing.T) {
	s := sessionUnitStore()
	session := NewIndexingSession(s)
	if _, err := session.Bind(sessionUnitWriter(sessionUnitStore())); err == nil {
		t.Fatal("wrong owner accepted")
	}
	w := sessionUnitWriter(s)
	db, err := session.Bind(w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.Bind(sessionUnitWriter(s)); err == nil {
		t.Fatal("concurrent binding accepted")
	}
	if err = session.WithReadOnly(context.Background(), 1, func(*IndexingStore) error { t.Fatal("nested readonly entered"); return nil }); err == nil {
		t.Fatal("nested readonly accepted")
	}

	if err = session.Finish(db, nil); err == nil {
		t.Fatal("active transaction finished")
	}
	db.Cache.tables["account"].rows["key"] = Row{"value": "committed"}
	db.Close()
	if err = session.Finish(db, nil); err != nil {
		t.Fatal(err)
	}
	second, err := session.Bind(sessionUnitWriter(s))
	if err != nil {
		t.Fatal(err)
	}
	if second == db || second.Cache == db.Cache {
		t.Fatal("binding handle reused")
	}
	if second.Cache.tables["account"].rows["key"]["value"] != "committed" {
		t.Fatal("committed cache lost")
	}
	db.Cache.tables["account"].rows["key"]["value"] = "escaped"
	if second.Cache.tables["account"].rows["key"]["value"] != "committed" {
		t.Fatal("old cache aliases new cache")
	}
	if _, err = db.Find(context.Background(), "account", Row{"id": "key"}); err == nil {
		t.Fatal("old handle revived")
	}
	if err = session.Finish(db, nil); err == nil {
		t.Fatal("old finish affected new binding")
	}
	second.Close()
	if err = session.Finish(second, errors.New("uncertain commit")); err != nil {
		t.Fatal(err)
	}
	third, err := session.Bind(sessionUnitWriter(s))
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Cache.tables["account"].rows) != 0 || third.Cache.tables["account"].complete {
		t.Fatal("failed transaction retained rows")
	}
	if third.Cache.profileState != db.Cache.profileState {
		t.Fatal("learned profiles lost")
	}
	third.Close()
	if err = session.Finish(third, nil); err != nil {
		t.Fatal(err)
	}
}
func TestIndexingSessionRejectUnflushedAndReadOnlyWrites(t *testing.T) {
	s := sessionUnitStore()
	session := NewIndexingSession(s, CacheOptions{Complete: true})
	db, err := session.Bind(sessionUnitWriter(s))
	if err != nil {
		t.Fatal(err)
	}
	db.Cache.tables["account"].inserts["key"] = Row{"id": "key"}
	db.Close()
	if err = session.Finish(db, nil); err == nil {
		t.Fatal("unflushed success accepted")
	}
	db, err = session.Bind(sessionUnitWriter(s))
	if err != nil {
		t.Fatal(err)
	}
	if db.Cache.tables["account"].complete {
		t.Fatal("unflushed failure retained complete hint")
	}
	db.readonly = true
	ctx := context.Background()
	if _, err = db.InsertOne(ctx, "account", Row{"id": "key"}, Conflict{}); err == nil {
		t.Fatal("readonly insert accepted")
	}
	if _, err = db.InsertMany(ctx, "account", nil, Conflict{}); err == nil {
		t.Fatal("readonly empty insert accepted")
	}
	if _, err = db.Update(ctx, "account", Row{"id": "key"}, Row{}); err == nil {
		t.Fatal("readonly update accepted")
	}
	if _, err = db.UpdateFunc(ctx, "account", Row{"id": "key"}, func(Row) (Row, error) { t.Fatal("update callback invoked"); return nil, nil }); err == nil {
		t.Fatal("readonly update callback accepted")
	}
	if _, err = db.Delete(ctx, "account", Row{"id": "key"}); err == nil {
		t.Fatal("readonly delete accepted")
	}
	if _, err = db.SQL(ctx, "DELETE FROM account"); err == nil {
		t.Fatal("readonly SQL write accepted")
	}
	db.Close()
	if err = session.Finish(db, nil); err != nil {
		t.Fatal(err)
	}
}

// This backend is only a lifecycle fault injector. Actual SQL/rollback behavior
// is covered by the owned-storage integration tests on both real backends.
type sessionTestBackend struct {
	commitErr  error
	statements []string
}

func (b *sessionTestBackend) Begin(context.Context) (Transaction, error) {
	return &sessionTestTx{b: b}, nil
}
func (b *sessionTestBackend) Query(context.Context, string, ...any) (Result, error) {
	return Result{}, nil
}
func (b *sessionTestBackend) Exec(context.Context, string, ...any) error { return nil }
func (b *sessionTestBackend) Close() error                               { return nil }
func (b *sessionTestBackend) Dialect() string                            { return "test" }

type sessionTestTx struct{ b *sessionTestBackend }

func (t *sessionTestTx) Query(context.Context, string, ...any) (Result, error) { return Result{}, nil }
func (t *sessionTestTx) Exec(_ context.Context, q string, _ ...any) error {
	t.b.statements = append(t.b.statements, q)
	return nil
}
func (t *sessionTestTx) Commit(context.Context) error   { return t.b.commitErr }
func (t *sessionTestTx) Rollback(context.Context) error { return nil }
func TestIndexingSessionReadOnlyLifetime(t *testing.T) {
	s := sessionUnitStore()
	b := &sessionTestBackend{}
	s.Backend = b
	session := NewIndexingSession(s)
	var escaped *IndexingStore
	err := session.WithReadOnly(context.Background(), 1, func(db *IndexingStore) error {
		escaped = db
		if err := session.WithReadOnly(context.Background(), 1, func(*IndexingStore) error { t.Fatal("nested readonly entered"); return nil }); err == nil {
			t.Fatal("concurrent readonly accepted")
		}
		if _, err := session.Bind(sessionUnitWriter(s)); err == nil {
			t.Fatal("write during readonly accepted")
		}
		db.Cache.tables["account"].rows["key"] = Row{"id": "key"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.statements) == 0 || b.statements[0] != "SET TRANSACTION READ ONLY" {
		t.Fatal("SQL readonly not enforced")
	}
	if _, err = escaped.Find(context.Background(), "account", Row{"id": "key"}); err == nil {
		t.Fatal("escaped readonly handle active")
	}
	b.commitErr = errors.New("uncertain commit")
	if err = session.WithReadOnly(context.Background(), 1, func(db *IndexingStore) error { return nil }); !errors.Is(err, b.commitErr) {
		t.Fatal(err)
	}
	if session.tables != nil {
		t.Fatal("uncertain readonly commit retained rows")
	}
	b.commitErr = nil
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("callback panic swallowed")
			}
		}()
		_ = session.WithReadOnly(context.Background(), 1, func(db *IndexingStore) error {
			db.Cache.tables["account"].rows["key"] = Row{"id": "key"}
			panic("callback")
		})
	}()
	if session.active != nil || session.tables != nil {
		t.Fatal("panic leaked active binding or row state")
	}
	if err = session.WithReadOnly(context.Background(), 1, func(db *IndexingStore) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

// commitReportBackend reports an uncertain outcome after the real SQL commit.
// A caller must discard cache state even though SQL may already be durable.
type commitReportBackend struct {
	Backend
	fail bool
}

func (b *commitReportBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &commitReportTx{Transaction: tx, b: b}, nil
}

type commitReportTx struct {
	Transaction
	b *commitReportBackend
}

func (tx *commitReportTx) Commit(ctx context.Context) error {
	if err := tx.Transaction.Commit(ctx); err != nil {
		return err
	}
	if tx.b.fail {
		return errors.New("commit acknowledgement lost")
	}
	return nil
}

func TestIndexingSessionRealTransactions(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			var base Backend
			if dialect == "pglite" {
				var err error
				base, err = OpenPGlite(ctx, t.TempDir()+"/session-db")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := base.Close(); err != nil {
						t.Error(err)
					}
				})
			} else {
				base = sourceBackend(t, ctx, dialect)
			}
			backend := &commitReportBackend{Backend: base}
			namespace := fmt.Sprintf("ponder_go_session_%x", time.Now().UnixNano())
			if dialect == "postgres" {
				requireNewSessionFixtureNamespaces(t, ctx, base, []string{namespace, namespace + "_sync"})
			}
			s, err := New(ctx, backend, sourceAccountModel(), namespace, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			// These exact namespaces were created by this test, never pre-existing data.
			t.Cleanup(func() {
				if err := base.Exec(ctx, "DROP SCHEMA "+schema.Quote(namespace)+" CASCADE"); err != nil {
					t.Error(err)
				}
				if err := base.Exec(ctx, "DROP SCHEMA "+schema.Quote(namespace+"_sync")+" CASCADE"); err != nil {
					t.Error(err)
				}
			})
			session := NewIndexingSession(s)
			var escaped *IndexingStore
			sequence := uint64(0)
			run := func(fn func(*IndexingStore) error) error {
				sequence++
				var bound *IndexingStore
				result := s.Apply(ctx, event(fmt.Sprint(sequence), 1, sequence), func(w *Writer) error {
					var err error
					bound, err = session.Bind(w)
					if err != nil {
						return err
					}
					return fn(bound)
				})
				if bound != nil {
					if finishErr := session.Finish(bound, result); finishErr != nil {
						t.Fatal(finishErr)
					}
				}
				return result
			}
			key := Row{"address": sourceZeroAddress}
			if err = run(func(db *IndexingStore) error {
				escaped = db
				_, err := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err = run(func(db *IndexingStore) error {
				if len(db.Cache.tables["account"].rows) != 1 {
					t.Fatal("committed row not retained")
				}
				if _, err := escaped.SQL(ctx, "SELECT * FROM "+schema.Quote(namespace)+".account"); err == nil {
					t.Fatal("escaped SQL handle resurrected")
				}
				_, err := db.Update(ctx, "account", key, Row{"balance": big.NewInt(99)})
				if err != nil {
					return err
				}
				return errors.New("rollback")
			}); err == nil {
				t.Fatal("rollback lost")
			}
			if err = run(func(db *IndexingStore) error {
				if len(db.Cache.tables["account"].rows) != 0 {
					t.Fatal("rollback retained cache")
				}
				row, err := db.Find(ctx, "account", key)
				if err == nil {
					expectSourceBalance(t, row, 10)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err = base.Exec(ctx, "ALTER TABLE "+schema.Quote(namespace)+".account ADD CONSTRAINT session_nonnegative CHECK (balance>=0)"); err != nil {
				t.Fatal(err)
			}
			if err = run(func(db *IndexingStore) error {
				_, err := db.Update(ctx, "account", key, Row{"balance": big.NewInt(-1)})
				return err
			}); err == nil {
				t.Fatal("flush constraint failure lost")
			}
			backend.fail = true
			if err = run(func(db *IndexingStore) error {
				if len(db.Cache.tables["account"].rows) != 0 {
					t.Fatal("flush failure retained rows")
				}
				_, err := db.Update(ctx, "account", key, Row{"balance": big.NewInt(11)})
				return err
			}); err == nil {
				t.Fatal("uncertain commit lost")
			}
			backend.fail = false
			if err = session.WithReadOnly(ctx, 1, func(db *IndexingStore) error {
				if len(db.Cache.tables["account"].rows) != 0 {
					t.Fatal("uncertain commit retained rows")
				}
				row, err := db.Find(ctx, "account", key)
				if err != nil {
					return err
				}
				expectSourceBalance(t, row, 11)
				result, err := db.SQL(ctx, "SELECT balance::text AS balance FROM account")
				if err != nil {
					return err
				}
				if len(result.Rows) != 1 || fmt.Sprint(result.Rows[0]["balance"]) != "11" {
					t.Fatalf("unqualified readonly SQL: %#v", result.Rows)
				}
				if _, err = db.SQL(ctx, "UPDATE "+schema.Quote(namespace)+".account SET balance=12"); err == nil {
					t.Fatal("readonly SQL mutation accepted")
				}
				if _, err = db.InsertOne(ctx, "account", Row{}, Conflict{}); err == nil {
					t.Fatal("readonly buffered mutation accepted")
				}
				return db.Cache.Prefetch(ctx, nil)
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Refuse the fixture before constructor mutation or cleanup registration if an
// exact namespace already exists; a timestamp is not an ownership proof.
func requireNewSessionFixtureNamespaces(t *testing.T, ctx context.Context, backend Backend, names []string) {
	t.Helper()
	for _, name := range names {
		result, err := backend.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", name)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 0 {
			t.Fatalf("refusing pre-existing fixture namespace %q", name)
		}
	}
}
