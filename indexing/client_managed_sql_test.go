package indexing

// Supplemental real-SQL cache completion evidence. This uses actual persistent
// PostgreSQL/PGlite transactions and the production asynchronous cache queue.
// It does NOT stand in for ManagedRunner activation or original-source credit.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type managedSQLFaultBackend struct {
	store.Backend
	target   string
	inserted chan struct{}
	release  chan struct{}
	failed   chan error
	once     sync.Once
}
type managedSQLFaultTransaction struct {
	store.Transaction
	owner *managedSQLFaultBackend
}

func (b *managedSQLFaultBackend) Begin(ctx context.Context) (store.Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &managedSQLFaultTransaction{Transaction: tx, owner: b}, nil
}
func (tx *managedSQLFaultTransaction) Exec(ctx context.Context, query string, args ...any) error {
	if err := tx.Transaction.Exec(ctx, query, args...); err != nil {
		return err
	}
	if !strings.HasPrefix(query, "INSERT INTO "+tx.owner.target+"(") {
		return nil
	}
	selected := false
	tx.owner.once.Do(func() { selected = true; close(tx.owner.inserted) })
	if !selected {
		return nil
	}
	select {
	case <-tx.owner.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	// The preceding actual INSERT is in the same real transaction. This actual
	// SQL error aborts it; the production cache worker must roll back, not commit.
	_, err := tx.Transaction.Query(ctx, "SELECT 1 / 0 AS deliberately_failed_cache_completion")
	tx.owner.failed <- err
	return err
}

func TestManagedSQLCacheFailureCompletion(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			root := os.Getenv("PONDER_M44_TEST_ROOT")
			if root == "" {
				t.Fatal("PONDER_M44_TEST_ROOT required for retained SQL evidence")
			}
			directory, err := os.MkdirTemp(root, "managed-client-sql-")
			if err != nil {
				t.Fatal(err)
			}
			namespace := fmt.Sprintf("ponder_go_m44_cf_%x", time.Now().UnixNano())
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			var backend store.Backend
			if dialect == "postgres" {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if url == "" {
					t.Fatal("explicit task PostgreSQL required; no skip")
				}
				backend, err = store.OpenPostgres(ctx, url)
			} else {
				backend, err = store.OpenPGlite(ctx, filepath.Join(directory, "cache"))
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			}()
			for _, suffix := range []string{"", "_sync", "_legacy", "_rpc_1"} {
				rows, err := backend.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", namespace+suffix)
				if err != nil || len(rows.Rows) != 0 {
					t.Fatal("fixture namespace not fresh", namespace+suffix, err)
				}
			}
			t.Logf("retained SQL evidence: directory=%s namespace=%s", directory, namespace)
			model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
			database, err := store.New(ctx, backend, model, namespace, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			protocol, err := database.EnableProtocolCache(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer protocol.Close()
			fault := &managedSQLFaultBackend{Backend: backend, target: schema.Quote(protocol.Namespace()) + "." + schema.Quote("rpc_request_results"), inserted: make(chan struct{}), release: make(chan struct{}), failed: make(chan error, 1)}
			database.Backend = fault
			adapter := protocol.AsyncSourceRPCRequestCache(1)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(fault.release) }) }
			defer func() {
				release()
				closeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				if err := adapter.Close(closeCtx); err != nil {
					t.Error(err)
				}
			}()
			requester := admissionRequester(func(_ context.Context, _ string, _, out any) error {
				return json.Unmarshal([]byte(`"0x2a"`), out)
			})
			chain := Object{"id": 1, "cacheRpcRequests": true}
			clients, err := NewStrictClientSet([]Object{chain}, []rpc.Requester{requester}, adapter)
			if err != nil {
				t.Fatal(err)
			}
			clients.managedRoots = newManagedClientRoots()
			defer func() {
				release()
				closeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				if err := clients.Close(closeCtx); err != nil {
					t.Error(err)
				}
			}()
			client, err := clients.GetClient(chain)
			if err != nil {
				t.Fatal(err)
			}
			balance, err := client.GetBalance(ctx, "0xabc", BalanceOptions{BlockNumber: big.NewInt(1)})
			if err != nil || balance.Int64() != 42 {
				t.Fatal(balance, err)
			}
			select {
			case <-fault.inserted:
			case <-ctx.Done():
				t.Fatal("real cache INSERT did not execute", ctx.Err())
			}
			if err := clients.managedRoots.sealAndWait(ctx); err != nil {
				t.Fatal(err)
			}
			if err := clients.managedRoots.failure(); err != nil {
				t.Fatal("SQL failure invented before execution", err)
			}
			if err := clients.WaitCacheWrites(managedCanceledContext()); !errors.Is(err, context.Canceled) {
				t.Fatal("root completion falsely joined SQL worker", err)
			}
			release()
			if err := clients.WaitCacheWrites(ctx); err != nil {
				t.Fatal(err)
			}
			actual := <-fault.failed
			if actual == nil || !strings.Contains(strings.ToLower(actual.Error()), "division by zero") {
				t.Fatal("expected real backend SQL division error", actual)
			}
			if err := clients.managedRoots.failure(); !errors.Is(err, actual) {
				t.Fatal("managed join lost exact real SQL error", err, actual)
			}
			rows, err := backend.Query(ctx, "SELECT count(*)::text AS count FROM "+fault.target)
			if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["count"] != "0" {
				t.Fatal("failed cache transaction persisted its INSERT", rows, err)
			}
		})
	}
}
