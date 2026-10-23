package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sourceZeroAddress = "0x0000000000000000000000000000000000000000"

func actionCheckpoint(block uint64) checkpoint.Checkpoint {
	return checkpoint.Checkpoint{ChainID: 1, BlockNumber: block}
}

func sourceBackend(t *testing.T, ctx context.Context, dialect string) Backend {
	t.Helper()
	var b Backend
	var e error
	if dialect == "postgres" {
		url := os.Getenv("PONDER_TEST_POSTGRES")
		if url == "" {
			t.Skip("explicit task database required; no parity credit")
		}
		b, e = OpenPostgres(ctx, url)
	} else {
		b, e = OpenPGlite(ctx, "memory://")
	}
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// The original raw writes followed by commitBlock become one atomic native
// Apply call. Every original row, checkpoint, NULL and assertion is retained;
// internal table names use the task's owned shadow namespace.
func TestUpstreamDatabaseActions(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"finalize()", "createIndexes()", "revert()", "revert() with composite primary key"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					b := sourceBackend(t, ctx, dialect)
					account := schema.Table{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint"}}}
					if name == "createIndexes()" {
						account.Indexes = []schema.Index{{Name: "balance_index", Columns: []string{"balance"}}}
					}
					if name == "revert() with composite primary key" {
						account = schema.Table{Name: "Test", Columns: []schema.Column{{Name: "A", Property: "a", Type: "integer", NotNull: true}, {Name: "B", Property: "b", Type: "integer", NotNull: true}, {Name: "C", Property: "c", Type: "integer"}}, PrimaryKey: []string{"A", "B"}}
					}
					s, e := New(ctx, b, schema.Schema{Tables: []schema.Table{account}}, fmt.Sprintf("ponder_go_actions_%x", time.Now().UnixNano()), schema.Multichain)
					if e != nil {
						t.Fatal(e)
					}
					apply := func(block uint64, sql string, args ...any) {
						t.Helper()
						if e := s.Apply(ctx, Event{ID: fmt.Sprint(block), Checkpoint: actionCheckpoint(block)}, func(w *Writer) error { _, e := w.SQL(ctx, sql, args...); return e }); e != nil {
							t.Fatal(e)
						}
					}
					switch name {
					case "createIndexes()":
						r, e := b.Query(ctx, `SELECT indexname AS name FROM pg_indexes WHERE schemaname=$1 AND tablename='account'`, s.Namespace)
						if e != nil {
							t.Fatal(e)
						}
						found := false
						for _, row := range r.Rows {
							found = found || row["name"] == "balance_index"
						}
						if !found {
							t.Fatalf("source toContain(balance_index): %v", r.Rows)
						}
					case "finalize()", "revert()":
						if name == "finalize()" {
							zero, _ := checkpoint.Encode(actionCheckpoint(0))
							if e = b.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_progress")+" VALUES (1,$1)", zero); e != nil {
								t.Fatal(e)
							}
							if e = b.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_finalized")+" VALUES (1,$1,$1)", zero); e != nil {
								t.Fatal(e)
							}
						}
						apply(9, `INSERT INTO account (address,balance) VALUES ($1,$2)`, sourceZeroAddress, big.NewInt(10))
						next := uint64(10)
						if name == "finalize()" {
							next = 11
						}
						if e = s.Apply(ctx, Event{ID: "next", Checkpoint: actionCheckpoint(next)}, func(w *Writer) error {
							if _, e := w.SQL(ctx, `UPDATE account SET balance=$1 WHERE address=$2`, big.NewInt(88), sourceZeroAddress); e != nil {
								return e
							}
							_, e := w.SQL(ctx, `INSERT INTO account (address) VALUES ($1)`, "0x0000000000000000000000000000000000000001")
							return e
						}); e != nil {
							t.Fatal(e)
						}
						if name == "finalize()" {
							if e = s.Finalize(ctx, actionCheckpoint(10)); e != nil {
								t.Fatal(e)
							}
							r, e := b.Query(ctx, "SELECT operation_id FROM "+s.internal("_ponder_go_journal"))
							if e != nil {
								t.Fatal(e)
							}
							if len(r.Rows) != 2 {
								t.Fatalf("source toHaveLength(2): %v", r.Rows)
							}
						} else {
							apply(11, `DELETE FROM account WHERE address=$1`, sourceZeroAddress)
							if e = s.Revert(ctx, actionCheckpoint(9)); e != nil {
								t.Fatal(e)
							}
							r, e := s.Read(ctx, "account")
							if e != nil {
								t.Fatal(e)
							}
							if len(r.Rows) != 1 {
								t.Fatalf("source toHaveLength(1): %v", r.Rows)
							}
							// Read through native typed Find to preserve the 10n assertion.
							if e = s.Apply(ctx, Event{ID: "inspect", Checkpoint: actionCheckpoint(9)}, func(w *Writer) error {
								row, e := w.Find(ctx, "account", Row{"address": sourceZeroAddress})
								if e != nil {
									return e
								}
								if !reflect.DeepEqual(row, Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}) {
									return fmt.Errorf("source toStrictEqual: %v", row)
								}
								return nil
							}); e != nil {
								t.Fatal(e)
							}
						}
					case "revert() with composite primary key":
						apply(11, `INSERT INTO "Test" ("A","B") VALUES (1,1)`)
						apply(12, `UPDATE "Test" SET "C"=1 WHERE "A"=1 AND "B"=1`)
						if e = s.Revert(ctx, actionCheckpoint(11)); e != nil {
							t.Fatal(e)
						}
						r, e := b.Query(ctx, `SELECT row_to_json(t)::text AS row FROM (SELECT "A" AS a,"B" AS b,"C" AS c FROM `+s.table("Test")+`) t`)
						if e != nil {
							t.Fatal(e)
						}
						if len(r.Rows) != 1 {
							t.Fatalf("source toHaveLength(1): %v", r.Rows)
						}
						var row Row
						d := json.NewDecoder(strings.NewReader(fmt.Sprint(r.Rows[0]["row"])))
						d.UseNumber()
						if e = d.Decode(&row); e != nil {
							t.Fatal(e)
						}
						if !reflect.DeepEqual(row, Row{"a": json.Number("1"), "b": json.Number("1"), "c": nil}) {
							t.Fatalf("source toStrictEqual: %v", row)
						}
					}
				})
			}
		})
	}
}

func TestUpstreamIndexingDelete(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			m := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
			s, e := New(ctx, b, m, fmt.Sprintf("ponder_go_delete_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			e = s.Apply(ctx, Event{ID: "delete", Checkpoint: actionCheckpoint(1)}, func(w *Writer) error {
				key := Row{"address": sourceZeroAddress}
				deleted, e := w.DeleteRow(ctx, "account", key)
				if e != nil {
					return e
				}
				if deleted {
					return fmt.Errorf("source toBe(false)")
				}
				if e = w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(12)}, Insert); e != nil {
					return e
				}
				if e = w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(12)}, Update); e != nil {
					return e
				}
				deleted, e = w.DeleteRow(ctx, "account", key)
				if e != nil {
					return e
				}
				if !deleted {
					return fmt.Errorf("source toBe(true)")
				}
				row, e := w.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				if row != nil {
					return fmt.Errorf("source toBe(null): %v", row)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
