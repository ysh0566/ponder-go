package indexing

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

func adapterStore(t *testing.T, dialect string) *store.Store {
	t.Helper()
	ctx := context.Background()
	var backend store.Backend
	var err error
	if dialect == "postgres" {
		url := os.Getenv("PONDER_TEST_POSTGRES")
		if url == "" {
			t.Skip("explicit task-only PostgreSQL required; no parity credit")
		}
		backend, err = store.OpenPostgres(ctx, url)
	} else {
		backend, err = store.OpenPGlite(ctx, "memory://")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
	s, err := store.New(ctx, backend, model, fmt.Sprintf("ponder_go_m38_adapter_%x", time.Now().UnixNano()), schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(s.Namespace)+" CASCADE")
	})
	return s
}
func adapterEvent(id string, block uint64) store.Event {
	return store.Event{ID: id, Checkpoint: checkpoint.Checkpoint{ChainID: 1, BlockNumber: block, BlockTimestamp: block, EventType: 5}}
}
func TestDatabaseAdaptersRealBackends(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			s := adapterStore(t, dialect)
			ctx := context.Background()
			state := &adapterErrorState{}
			var escaped *Database
			err := s.Apply(ctx, adapterEvent("write", 1), func(w *store.Writer) error {
				underlying := w.Indexing()
				db := NewDatabase(underlying, state)
				escaped = db
				if db.Underlying() != underlying || db.Underlying().Cache != underlying.Cache {
					t.Fatal("real store/cache identity changed")
				}
				if _, e := db.InsertOne(ctx, "account", store.Row{"id": "alice", "balance": big.NewInt(1)}, store.Conflict{}); e != nil {
					return e
				}
				if e := db.Flush(ctx); e != nil {
					return e
				}
				underlying.Cache.Clear()
				row, e := db.Find(ctx, "account", store.Row{"id": "alice"})
				if e != nil {
					return e
				}
				if row["balance"].(*big.Int).Int64() != 1 {
					t.Fatal(row)
				}
				if _, e = db.Update(ctx, "account", store.Row{"id": "alice"}, store.Row{"balance": big.NewInt(2)}); e != nil {
					return e
				}
				if e = db.Flush(ctx); e != nil {
					return e
				}
				_, e = db.SQL(ctx, "INSERT INTO "+schema.Quote(s.Namespace)+".account (id,balance) VALUES ('alice',3)")
				var classified *DatabaseError
				if !errors.As(e, &classified) || classified.Name != "UniqueConstraintError" || state.err != nil {
					t.Fatalf("actual SQL constraint misclassified: %T %v sticky=%v", e, e, state.err)
				}
				rows, e := db.Select(ctx, "account", nil)
				if e != nil {
					return e
				}
				if len(rows) != 1 || rows[0]["balance"].(*big.Int).Int64() != 2 {
					t.Fatal(rows)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = escaped.Find(ctx, "account", store.Row{"id": "alice"}); err == nil {
				t.Fatal("escaped database remained active")
			}
			if state.err != nil {
				t.Fatal("lifetime error became sticky")
			}
			cache, err := s.EnableProtocolCache(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = s.Backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(cache.Namespace())+" CASCADE")
			})
			requester := &balanceRequester{}
			chain := Object{"id": 1, "cacheRpcRequests": true}
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{requester}, cache.SourceRPCRequestCache(1))
			if err != nil {
				t.Fatal(err)
			}
			client, _ := clients.GetClient(chain)
			clients.SetEvent(Object{"type": "setup", "block": 0})
			for i := 0; i < 2; i++ {
				if _, err = client.GetBalance(ctx, "0xabc"); err != nil {
					t.Fatal(err)
				}
				if err = clients.WaitCacheWrites(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if requester.calls != 1 {
				t.Fatal("persistent source RPC cache miss", requester.calls)
			}
			_ = cache.Close()
			if _, err = client.GetBalance(ctx, "0xabc"); err == nil {
				t.Fatal("closed cache accepted read")
			}
		})
	}
}

// PostgreSQL has a real client connection that can be terminated. PGlite is an
// in-process WASM database and has no equivalent connection-loss operation.
func TestDatabasePostgresRealConnectionLossSticky(t *testing.T) {
	s := adapterStore(t, "postgres")
	ctx := context.Background()
	state := &adapterErrorState{}
	err := s.Apply(ctx, adapterEvent("disconnect", 1), func(w *store.Writer) error {
		db := NewDatabase(w.Indexing(), state)
		if _, e := db.InsertOne(ctx, "account", store.Row{"id": "rollback", "balance": big.NewInt(1)}, store.Conflict{}); e != nil {
			return e
		}
		if e := db.Flush(ctx); e != nil {
			return e
		}
		_, caught := db.SQL(ctx, "SELECT pg_terminate_backend(pg_backend_pid())")
		if caught == nil {
			t.Fatal("connection was not terminated")
		}
		var named *DatabaseError
		if !errors.As(caught, &named) || named.Name != "DbConnectionError" || !named.Retryable() || state.err != caught {
			t.Fatalf("connection error not sticky: %T %v sticky=%v", caught, caught, state.err)
		}
		return nil // Deliberately catch the error: the sticky error remains observable.
	})
	if err == nil {
		t.Fatal("terminated transaction committed")
	}
	if state.err == nil {
		t.Fatal("caught retryable disappeared")
	}
	rows, err := s.Backend.Query(ctx, "SELECT * FROM "+schema.Quote(s.Namespace)+".account")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 0 {
		t.Fatal("failed connection left business rows")
	}
}
