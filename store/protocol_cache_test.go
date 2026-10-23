package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

type protocolVector struct {
	Name, Table string
	Chain       uint64
	Input       map[string]any
	Expected    Row
}

func protocolVectors(t *testing.T) []protocolVector {
	t.Helper()
	data, e := os.ReadFile("../testdata/protocol/encoding.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct{ Cases []protocolVector }
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	return fixture.Cases
}
func TestProtocolEncodingDifferential(t *testing.T) {
	for _, v := range protocolVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			row, e := EncodeProtocolRow(v.Table, v.Chain, v.Input)
			actual, _ := json.Marshal(row)
			expected, _ := json.Marshal(v.Expected)
			if e != nil || string(actual) != string(expected) {
				t.Fatalf("source encoder differs:\ngot %#v\nwant %#v\n%v", row, v.Expected, e)
			}
		})
	}
}
func TestProtocolCacheBoundaries(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, name := range []string{"duplicates and failed batch", "reorg and replay", "chain isolation and close", "atomic completion and failure"} {
			t.Run(dialect+"/"+name, func(t *testing.T) {
				ctx := context.Background()
				b := sourceBackend(t, ctx, dialect)
				s, e := New(ctx, b, sourceActionsModel(), fmt.Sprintf("ponder_go_protocol_%x", time.Now().UnixNano()), schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				cache, e := s.EnableProtocolCache(ctx)
				if e != nil {
					t.Fatal(e)
				}
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
				syncBlock := SyncBlock{ChainID: 1, Number: 1, Timestamp: 0x123, Hash: fmt.Sprint(block["hash"]), ParentHash: fmt.Sprint(block["parentHash"]), Payload: raw}
				key := `[1,"eth_getLogs",[{"blockHash":"a"}]]`
				switch name {
				case "duplicates and failed batch":
					for table, input := range inputs {
						if e = cache.Insert(ctx, table, 1, []map[string]any{input, input}); e != nil {
							t.Fatal(e)
						}
						rows, e := cache.Rows(ctx, table, 1, 0, 10)
						if e != nil || len(rows.Rows) != 1 {
							t.Fatal(table, rows, e)
						}
					}
					if e = cache.Insert(ctx, "logs", 2, []map[string]any{inputs["logs"], {"blockNumber": "0x1"}}); e == nil {
						t.Fatal("malformed batch accepted")
					}
					rows, e := cache.Rows(ctx, "logs", 2, 0, 10)
					if e != nil || len(rows.Rows) != 0 {
						t.Fatal("partial batch committed", rows, e)
					}
				case "reorg and replay":
					if e = s.CompleteBlock(ctx, syncBlock, "native:1"); e != nil {
						t.Fatal(e)
					}
					for table, input := range inputs {
						if e = cache.Insert(ctx, table, 1, []map[string]any{input}); e != nil {
							t.Fatal(e)
						}
					}
					if e = s.RecordFactoryChildren(ctx, "factory", 1, 1, syncBlock.Hash, []FactoryChild{{Address: sourceZeroAddress, LogIndex: 0}}); e != nil {
						t.Fatal(e)
					}
					if e = cache.CachePut(ctx, key, json.RawMessage(`[1]`)); e != nil {
						t.Fatal(e)
					}
					if e = s.Apply(ctx, event("one", 1, 1), func(w *Writer) error {
						return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert)
					}); e != nil {
						t.Fatal(e)
					}
					// Fail after business undo, at the cache DELETE. The entire
					// indexing/provenance/cache transaction must roll back.
					if e = b.Exec(ctx, "CREATE FUNCTION "+cache.table("reject_rewind")+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected cache rewind failure'; END $$"); e != nil {
						t.Fatal(e)
					}
					if e = b.Exec(ctx, "CREATE TRIGGER reject_rewind BEFORE DELETE ON "+cache.table("blocks")+" FOR EACH ROW EXECUTE FUNCTION "+cache.table("reject_rewind")+"()"); e != nil {
						t.Fatal(e)
					}
					if e = s.RevertAndRewind(ctx, checkpoint.Checkpoint{ChainID: 1}); e == nil {
						t.Fatal("rewind fault was ignored")
					}
					rows, e := s.Read(ctx, "account")
					if e != nil || len(rows.Rows) != 1 || fmt.Sprint(rows.Rows[0]["balance"]) != "10" {
						t.Fatal("failed cache rewind committed business undo", rows, e)
					}
					cached, e := cache.Rows(ctx, "blocks", 1, 0, 10)
					if e != nil || len(cached.Rows) != 1 {
						t.Fatal("failed rewind lost provenance", cached, e)
					}
					if e = b.Exec(ctx, "DROP TRIGGER reject_rewind ON "+cache.table("blocks")); e != nil {
						t.Fatal(e)
					}
					if e = b.Exec(ctx, "DROP FUNCTION "+cache.table("reject_rewind")+"()"); e != nil {
						t.Fatal(e)
					}
					if e = s.RevertAndRewind(ctx, checkpoint.Checkpoint{ChainID: 1}); e != nil {
						t.Fatal(e)
					}
					for table := range inputs {
						rows, e := cache.Rows(ctx, table, 1, 0, 10)
						if e != nil || len(rows.Rows) != 0 {
							t.Fatal("orphan protocol rows", table, rows, e)
						}
					}
					if _, ok, e := cache.CacheGet(ctx, key); e != nil || ok {
						t.Fatal("orphan RPC result", ok, e)
					}
					for _, relation := range []string{"factory_addresses", "intervals"} {
						r, e := b.Query(ctx, "SELECT count(*)::text AS count FROM "+cache.table(relation)+" WHERE chain_id=1"+map[string]string{"intervals": " AND NOT isempty(blocks)"}[relation])
						if e != nil || r.Rows[0]["count"] != "0" {
							t.Fatal("orphan "+relation, r, e)
						}
					}
					if e = s.CompleteBlock(ctx, syncBlock, "native:1"); e != nil {
						t.Fatal(e)
					}
				case "chain isolation and close":
					if e = cache.CachePut(ctx, key, json.RawMessage(`[123]`)); e != nil {
						t.Fatal(e)
					}
					if raw, ok, e := cache.CacheGet(ctx, key); e != nil || !ok || string(raw) != "[123]" {
						t.Fatal(raw, ok, e)
					}
					if _, ok, e := cache.CacheGet(ctx, `[2,"eth_getLogs",[{"blockHash":"a"}]]`); e != nil || ok {
						t.Fatal("chain cache leaked", ok, e)
					}
					if e = cache.Close(); e != nil {
						t.Fatal(e)
					}
					if _, _, e = cache.CacheGet(ctx, key); e == nil {
						t.Fatal("read after cache close")
					}
					if e = cache.Insert(ctx, "logs", 1, []map[string]any{inputs["logs"]}); e == nil {
						t.Fatal("write after cache close")
					}
				case "atomic completion and failure":
					if e = b.Exec(ctx, "ALTER TABLE "+cache.table("intervals")+" ADD CONSTRAINT reject_completion CHECK (false)"); e != nil {
						t.Fatal(e)
					}
					if e = s.CompleteBlock(ctx, syncBlock, "native:1"); e == nil {
						t.Fatal("completion constraint ignored")
					}
					cached, e := s.SyncedBlock(ctx, 1, 1)
					if e != nil || cached != nil {
						t.Fatal("native provenance partially committed", cached, e)
					}
					for _, table := range []string{"blocks", "transactions"} {
						rows, e := cache.Rows(ctx, table, 1, 0, 10)
						if e != nil || len(rows.Rows) != 0 {
							t.Fatal("protocol provenance partially committed", rows, e)
						}
					}
					if e = b.Exec(ctx, "ALTER TABLE "+cache.table("intervals")+" DROP CONSTRAINT reject_completion"); e != nil {
						t.Fatal(e)
					}
					if e = s.CompleteBlock(ctx, syncBlock, "native:1"); e != nil {
						t.Fatal(e)
					}
				}
			})
		}
	}
}
