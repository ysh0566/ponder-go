package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

// These are extra native codec/undo regressions, not transplanted source cases.
func TestORMJSONNullCharacterUndo(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			model := schema.Schema{Tables: []schema.Table{{Name: "document", Columns: []schema.Column{{Name: "id", Type: "integer", Primary: true}, {Name: "metadata", Type: "json"}}}}}
			namespace := fmt.Sprintf("ponder_go_json_%x", time.Now().UnixNano())
			s, e := New(ctx, b, model, namespace, schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			original := map[string]any{"actual": "a\x00b", "literal": `a\u0000b`, "nested": []any{map[string]any{"key\x00": "\x00"}}}
			if e = s.Apply(ctx, event("insert", 1, 1), func(w *Writer) error {
				_, e := w.Indexing().InsertOne(ctx, "document", Row{"id": 1, "metadata": original}, Conflict{})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			// Reopen the same owned store to read the durable snapshot, not a cache.
			s, e = New(ctx, b, model, namespace, schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			check := func(want any) {
				t.Helper()
				r, e := s.QueryReadOnly(ctx, `SELECT row_to_json(d)::text AS data FROM document d WHERE id=1`)
				if e != nil {
					t.Fatal(e)
				}
				if want == nil {
					if len(r.Rows) != 0 {
						t.Fatal(r)
					}
					return
				}
				if len(r.Rows) != 1 {
					t.Fatal(r)
				}
				var row Row
				decoder := json.NewDecoder(strings.NewReader(fmt.Sprint(r.Rows[0]["data"])))
				decoder.UseNumber()
				if e = decoder.Decode(&row); e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(row["metadata"], want) {
					t.Fatalf("metadata got %#v want %#v", row["metadata"], want)
				}
			}
			check(original)
			second := map[string]any{"actual": "c\x00d", "literal": `c\u0000d`}
			if e = s.Apply(ctx, event("update", 1, 2), func(w *Writer) error {
				db := w.Indexing()
				row, e := db.Find(ctx, "document", Row{"id": 1})
				if e != nil {
					return e
				}
				if !reflect.DeepEqual(row["metadata"], original) {
					return fmt.Errorf("cache miss snapshot lost JSON NUL: %v", row)
				}
				_, e = db.Update(ctx, "document", Row{"id": 1}, Row{"metadata": second})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			check(second)
			third := map[string]any{"actual": "raw\x00sql", "number": json.Number("90071992547409931234567890")}
			raw, _ := json.Marshal(third)
			if e = s.Apply(ctx, event("raw", 1, 3), func(w *Writer) error {
				_, e := w.SQL(ctx, `UPDATE document SET metadata=$1::json WHERE id=1`, string(raw))
				return e
			}); e != nil {
				t.Fatal(e)
			}
			check(third)
			if e = s.Apply(ctx, event("abort", 1, 4), func(w *Writer) error {
				if _, e := w.SQL(ctx, `DELETE FROM document WHERE id=1`); e != nil {
					return e
				}
				return fmt.Errorf("injected abort after journal capture")
			}); e == nil {
				t.Fatal("injected abort committed")
			}
			check(third)
			if e = s.Apply(ctx, event("delete", 1, 4), func(w *Writer) error {
				_, e := w.Indexing().Delete(ctx, "document", Row{"id": 1})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			check(nil)
			for _, step := range []struct {
				block uint64
				want  any
			}{{3, third}, {2, second}, {1, original}, {0, nil}} {
				if e = s.Revert(ctx, event("", 1, step.block).Checkpoint); e != nil {
					t.Fatal(e)
				}
				check(step.want)
			}
		})
	}
}

func TestStorageVersionRejectsOldJournal(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			namespace := fmt.Sprintf("ponder_go_oldjournal_%x", time.Now().UnixNano())
			model := sourceAccountModel()
			s, e := New(ctx, b, model, namespace, schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			if e = b.Exec(ctx, "ALTER TABLE "+s.internal("_ponder_go_journal")+" ALTER COLUMN before_row TYPE jsonb USING before_row::jsonb"); e != nil {
				t.Fatal(e)
			}
			if e = b.Exec(ctx, "UPDATE "+s.internal("_ponder_go_meta")+" SET value='old-owned-v1' WHERE key='fingerprint'"); e != nil {
				t.Fatal(e)
			}
			if _, e = New(ctx, b, model, namespace, schema.Multichain); e == nil || !strings.Contains(e.Error(), "storage format changed") {
				t.Fatalf("old journal adoption: %v", e)
			}
			r, e := b.Query(ctx, "SELECT data_type FROM information_schema.columns WHERE table_schema=$1 AND table_name='_ponder_go_journal' AND column_name='before_row'", namespace)
			if e != nil || len(r.Rows) != 1 || r.Rows[0]["data_type"] != "jsonb" {
				t.Fatalf("old journal changed: %v %v", r, e)
			}
			fresh, e := New(ctx, b, model, "", schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.HasPrefix(fresh.Namespace, "ponder_go_shadow_v2_") {
				t.Fatal("new default namespace lacks storage version", fresh.Namespace)
			}
		})
	}
}

func TestORMBytesPrimaryKey(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			model := schema.Schema{Tables: []schema.Table{{Name: "binary_key", Columns: []schema.Column{{Name: "key", Type: "bytes", Primary: true}, {Name: "value", Type: "text"}}}}}
			s, e := New(ctx, b, model, fmt.Sprintf("ponder_go_bytekey_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			key := Row{"key": []byte{0, 128, 255, 92, 78, 9, 10}}
			if e = s.Apply(ctx, event("insert", 1, 1), func(w *Writer) error {
				_, e := w.Indexing().InsertOne(ctx, "binary_key", Row{"key": key["key"], "value": "before"}, Conflict{})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			check := func(w *Writer, want string) error {
				row, e := w.Indexing().Find(ctx, "binary_key", key)
				if e != nil {
					return e
				}
				if row == nil || row["value"] != want || !reflect.DeepEqual(row["key"], key["key"]) {
					return fmt.Errorf("byte key lookup: %v want %s", row, want)
				}
				return nil
			}
			if e = s.Apply(ctx, event("update", 1, 2), func(w *Writer) error {
				if e := check(w, "before"); e != nil {
					return e
				}
				_, e := w.Indexing().Update(ctx, "binary_key", key, Row{"value": "after"})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if e = s.Revert(ctx, event("", 1, 1).Checkpoint); e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(ctx, event("delete", 1, 2), func(w *Writer) error {
				if e := check(w, "before"); e != nil {
					return e
				}
				deleted, e := w.Indexing().Delete(ctx, "binary_key", key)
				if e == nil && !deleted {
					return fmt.Errorf("byte key delete missed")
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if e = s.Revert(ctx, event("", 1, 1).Checkpoint); e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(ctx, event("read", 1, 2), func(w *Writer) error { return check(w, "before") }); e != nil {
				t.Fatal(e)
			}
		})
	}
}
