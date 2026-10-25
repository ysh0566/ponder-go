package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

// This is a native failure probe, not source parity credit. The queue admission
// and constraint violation are real; only the Store mutex is deliberately held
// to make the late-write boundary deterministic.
func TestIndependentStorageLateCacheSQLFailure(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			suffix := fmt.Sprintf("%x", time.Now().UnixNano())
			root := t.TempDir()
			options := IndependentStorageOptions{
				PostgresURL:       os.Getenv("PONDER_TEST_POSTGRES"),
				BusinessDirectory: filepath.Join(root, "business"), SyncDirectory: filepath.Join(root, "sync"),
				BusinessNamespace: "ponder_go_lateb_" + suffix, SyncNamespace: "ponder_go_lates_" + suffix,
				Schema: sourceAccountModel(), Ordering: schema.Multichain,
			}
			if dialect == "postgres" && options.PostgresURL == "" {
				t.Fatal("explicit task PostgreSQL URL required")
			}
			if dialect == "postgres" {
				probe, e := OpenPostgres(ctx, options.PostgresURL)
				if e != nil {
					t.Fatal(e)
				}
				func() {
					defer probe.Close()
					requireNewSessionFixtureNamespaces(t, ctx, probe, append(independentNamespaceFamily(options.BusinessNamespace), independentNamespaceFamily(options.SyncNamespace)...))
				}()
			}
			open := func() (*IndependentStorage, error) {
				if dialect == "postgres" {
					return OpenIndependentPostgres(ctx, options)
				}
				return OpenIndependentPGlite(ctx, options)
			}
			owner, err := open()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				// Only exact namespaces belonging to this successfully created fixture.
				for _, domain := range []struct {
					backend Backend
					names   []string
				}{
					{owner.businessBackend, []string{options.BusinessNamespace, options.BusinessNamespace + "_sync"}},
					{owner.syncBackend, independentNamespaceFamily(options.SyncNamespace)},
				} {
					for _, name := range domain.names {
						if e := domain.backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(name)+" CASCADE"); e != nil {
							t.Error(e)
						}
					}
				}
				if e := owner.Close(context.Background()); e != nil {
					t.Error(e)
				}
			})
			cache := owner.SyncCache()
			if err = owner.syncBackend.Exec(ctx, "ALTER TABLE "+cache.table("rpc_request_results")+" ADD CONSTRAINT reject_late_result CHECK (result <> '\"0xbad\"')"); err != nil {
				t.Fatal(err)
			}
			adapter := cache.AsyncSourceRPCRequestCache(1)
			// Registered after owner cleanup, so all queue completion precedes teardown.
			t.Cleanup(func() {
				if e := adapter.Close(context.Background()); e != nil {
					t.Error(e)
				}
			})
			cache.s.mu.Lock()
			locked := true
			defer func() {
				if locked {
					cache.s.mu.Unlock()
				}
			}()
			key := `[1,"eth_getBalance",["0x0000000000000000000000000000000000000000","0x2"]]`
			var future <-chan error
			rollback := errors.New("business callback rollback")
			err = owner.BusinessActions().HistoricalBatch(ctx, 1, func(w *Writer) error {
				if _, e := w.Indexing().InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(7)}, Conflict{}); e != nil {
					return e
				}
				future = adapter.BeginCachePut(ctx, key, json.RawMessage(`"0xbad"`))
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatalf("business outcome: %v", err)
			}
			adapter.state.mu.Lock()
			pending := len(adapter.state.pending)
			adapter.state.mu.Unlock()
			if pending != 1 {
				t.Fatalf("late write not admitted: %d", pending)
			}
			select {
			case err := <-future:
				t.Fatalf("write settled before release: %v", err)
			default:
			}
			cache.s.mu.Unlock()
			locked = false
			select {
			case err = <-future:
				if err == nil || !strings.Contains(err.Error(), "reject_late_result") {
					t.Fatalf("expected actual SQL constraint failure, got %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// Wait means settled; it intentionally does not turn the failed SQL into a
			// durability success. Check the actual SQL rows and public cache miss below.
			if err = adapter.WaitCacheWrites(ctx); err != nil {
				t.Fatal(err)
			}
			rows, err := owner.syncBackend.Query(ctx, "SELECT * FROM "+cache.table("rpc_request_results"))
			if err != nil {
				t.Fatal(err)
			}
			if len(rows.Rows) != 0 {
				t.Fatalf("failed cache write persisted: %#v", rows.Rows)
			}
			if _, hit, e := adapter.CacheGet(ctx, key); e != nil || hit {
				t.Fatalf("failed cache lookup: hit=%v err=%v", hit, e)
			}
			business, err := owner.BusinessStore().Read(ctx, "account")
			if err != nil {
				t.Fatal(err)
			}
			if len(business.Rows) != 0 {
				t.Fatalf("business rollback persisted: %#v", business.Rows)
			}
			// A later successful write still works: the failure neither poisons the
			// adapter nor permits an errored job to masquerade as a cache hit.
			if err = <-adapter.BeginCachePut(ctx, key, json.RawMessage(`"0x7"`)); err != nil {
				t.Fatal(err)
			}
			if err = adapter.WaitCacheWrites(ctx); err != nil {
				t.Fatal(err)
			}
			if value, hit, e := adapter.CacheGet(ctx, key); e != nil || !hit || string(value) != `"0x7"` {
				t.Fatalf("later cache write: %s %v %v", value, hit, e)
			}
		})
	}
}
