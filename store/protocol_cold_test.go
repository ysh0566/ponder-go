package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

func TestProtocolColdRewind(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, name := range []string{"disabled reopen and reenable", "closed adapter rewind", "cold rewind failure is atomic"} {
			t.Run(dialect+"/"+name, func(t *testing.T) {
				ctx := context.Background()
				dir := filepath.Join(t.TempDir(), "db")
				open := func() Backend {
					t.Helper()
					var b Backend
					var e error
					if dialect == "postgres" {
						url := os.Getenv("PONDER_TEST_POSTGRES")
						if url == "" {
							t.Skip("explicit task PostgreSQL required; no parity credit")
						}
						b, e = OpenPostgres(ctx, url)
					} else {
						b, e = OpenPGlite(ctx, dir)
					}
					if e != nil {
						t.Fatal(e)
					}
					return b
				}
				b := open()
				defer func() { b.Close() }()
				ns := fmt.Sprintf("ponder_go_cold_%x", time.Now().UnixNano())
				newStore := func() *Store {
					t.Helper()
					s, e := New(ctx, b, sourceActionsModel(), ns, schema.Multichain)
					if e != nil {
						t.Fatal(e)
					}
					return s
				}
				s := newStore()
				cache, e := s.EnableProtocolCache(ctx)
				if e != nil {
					t.Fatal(e)
				}
				cacheNamespace := cache.Namespace()
				inputs := map[string]map[string]any{}
				for _, v := range protocolVectors(t) {
					if v.Name == v.Table+"/full" {
						inputs[v.Table] = v.Input
					}
				}
				block := inputs["blocks"]
				block["transactions"] = []map[string]any{inputs["transactions"]}
				raw, e := json.Marshal(block)
				if e != nil {
					t.Fatal(e)
				}
				header := SyncBlock{ChainID: 1, Number: 1, Hash: fmt.Sprint(block["hash"]), ParentHash: fmt.Sprint(block["parentHash"]), Timestamp: 0x123, Payload: raw}
				if e = s.CompleteBlock(ctx, header, "native:chain1"); e != nil {
					t.Fatal(e)
				}
				for table, input := range inputs {
					for _, chain := range []uint64{1, 2} {
						if e = cache.Insert(ctx, table, chain, []map[string]any{input}); e != nil {
							t.Fatal(e)
						}
					}
				}
				for _, chain := range []uint64{1, 2} {
					if e = s.RecordFactoryChildren(ctx, "factory", chain, 1, header.Hash, []FactoryChild{{Address: sourceZeroAddress}}); e != nil {
						t.Fatal(e)
					}
					if e = cache.CachePut(ctx, fmt.Sprintf(`[%d,"eth_getLogs",[]]`, chain), json.RawMessage(`[123]`)); e != nil {
						t.Fatal(e)
					}
				}
				if e = s.Apply(ctx, event("one", 1, 1), func(w *Writer) error {
					return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert)
				}); e != nil {
					t.Fatal(e)
				}
				if name == "closed adapter rewind" {
					if e = cache.Close(); e != nil {
						t.Fatal(e)
					}
				} else {
					if e = b.Close(); e != nil {
						t.Fatal(e)
					}
					b = open()
					s = newStore()
					if s.protocol.Load() != nil {
						t.Fatal("cold store unexpectedly enabled cache")
					}
				}
				if name == "cold rewind failure is atomic" {
					if e = b.Exec(ctx, "CREATE FUNCTION "+schema.Quote(cacheNamespace)+".reject_cold() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'cold rewind fault'; END $$"); e != nil {
						t.Fatal(e)
					}
					if e = b.Exec(ctx, "CREATE TRIGGER reject_cold BEFORE DELETE ON "+schema.Quote(cacheNamespace)+".blocks FOR EACH ROW EXECUTE FUNCTION "+schema.Quote(cacheNamespace)+".reject_cold()"); e != nil {
						t.Fatal(e)
					}
					if e = s.RevertAndRewind(ctx, checkpoint.Checkpoint{ChainID: 1}); e == nil {
						t.Fatal("cold cache DELETE did not participate in rewind")
					}
					rows, e := s.Read(ctx, "account")
					if e != nil || len(rows.Rows) != 1 || fmt.Sprint(rows.Rows[0]["balance"]) != "10" {
						t.Fatal("failed cold rewind leaked undo", rows, e)
					}
					prior, e := s.SyncedBlock(ctx, 1, 1)
					if e != nil || prior == nil {
						t.Fatal("failed cold rewind lost native header", prior, e)
					}
					if e = b.Exec(ctx, "DROP TRIGGER reject_cold ON "+schema.Quote(cacheNamespace)+".blocks"); e != nil {
						t.Fatal(e)
					}
					if e = b.Exec(ctx, "DROP FUNCTION "+schema.Quote(cacheNamespace)+".reject_cold()"); e != nil {
						t.Fatal(e)
					}
				}
				if e = s.RevertAndRewind(ctx, checkpoint.Checkpoint{ChainID: 1}); e != nil {
					t.Fatal(e)
				}
				for table := range inputs {
					rows, e := b.Query(ctx, "SELECT chain_id::text AS chain FROM "+schema.Quote(cacheNamespace)+"."+schema.Quote(table))
					if e != nil || len(rows.Rows) != 1 || rows.Rows[0]["chain"] != "2" {
						t.Fatalf("cold/closed cache retained orphan or removed other chain %s: %#v %v", table, rows, e)
					}
				}
				for _, table := range []string{"factory_addresses", "rpc_request_results"} {
					rows, e := b.Query(ctx, "SELECT chain_id::text AS chain FROM "+schema.Quote(cacheNamespace)+"."+schema.Quote(table))
					if e != nil || len(rows.Rows) != 1 || rows.Rows[0]["chain"] != "2" {
						t.Fatal(table, rows, e)
					}
				}
				if s.protocol.Load() != nil && name != "closed adapter rewind" {
					t.Fatal("disabled rewind enabled RPC caching")
				}
				// A new adapter can reuse the owned schema after invalidation.
				if e = b.Close(); e != nil {
					t.Fatal(e)
				}
				b = open()
				s = newStore()
				cache, e = s.EnableProtocolCache(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.CompleteBlock(ctx, header, "native:chain1"); e != nil {
					t.Fatal(e)
				}
				rows, e := cache.Rows(ctx, "blocks", 1, 0, 10)
				if e != nil || len(rows.Rows) != 1 {
					t.Fatal("reenable could not replay", rows, e)
				}
			})
		}
	}
}

func TestProtocolDisabledOwnership(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			s, e := New(ctx, b, sourceActionsModel(), fmt.Sprintf("ponder_go_disabled_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			m, e := NewSyncMigrator(b, s.Namespace)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.RewindSync(ctx, 1, 0); e != nil {
				t.Fatal(e)
			}
			r, e := b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", m.LatestSchema())
			if e != nil || len(r.Rows) != 0 {
				t.Fatal("disabled rewind created cache schema", r, e)
			}
			if e = b.Exec(ctx, "CREATE SCHEMA "+schema.Quote(m.LatestSchema())); e != nil {
				t.Fatal(e)
			}
			if e = b.Exec(ctx, "CREATE TABLE "+schema.Quote(m.LatestSchema())+".unrelated(value int)"); e != nil {
				t.Fatal(e)
			}
			if e = s.RewindSync(ctx, 1, 0); e == nil {
				t.Fatal("disabled rewind adopted unrelated namespace")
			}
			r, e = b.Query(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema=$1", m.LatestSchema())
			if e != nil || len(r.Rows) != 1 || r.Rows[0]["table_name"] != "unrelated" {
				t.Fatal("unowned schema touched", r, e)
			}
		})
	}
}
