package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"testing"

	"ponder.local/ponder-go/schema"
)

func TestCachedRowCopyOwnership(t *testing.T) {
	model := schema.Schema{Tables: []schema.Table{{Name: "entry", Columns: []schema.Column{
		{Name: "id", Type: "text", Primary: true},
		{Name: "payload", Type: "bytes"},
		{Name: "label", Type: "text"},
	}}}}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
				key := Row{"id": "one"}
				if _, err := db.InsertOne(ctx, "entry", Row{"id": "one", "payload": []byte{1, 2}, "label": "initial"}, Conflict{}); err != nil {
					return err
				}
				first, err := db.FindCopy(ctx, "entry", key)
				if err != nil || first == nil {
					return fmt.Errorf("find initial row: %v", err)
				}
				label, ok := first.Get("label")
				if !ok || label != "initial" || first.owned {
					return fmt.Errorf("scalar read copied row: %v", label)
				}
				if _, err = db.Update(ctx, "entry", key, Row{"label": "updated"}); err != nil {
					return err
				}
				payload, _ := first.Get("payload")
				switch b := payload.(type) {
				case []byte:
					b[0] = 99
				default:
					return fmt.Errorf("unexpected bytes %T", payload)
				}
				first.Set("local", true)
				first.Delete("id")
				next, err := db.Find(ctx, "entry", key)
				if err != nil {
					return err
				}
				if next["label"] != "updated" || !reflect.DeepEqual(next["payload"], []byte{1, 2}) || first.Has("id") || next["local"] != nil {
					return fmt.Errorf("cache ownership: view %v cached %v", first, next)
				}
				if label, _ = first.Get("label"); label != "initial" {
					return fmt.Errorf("earlier view changed: %v", label)
				}
				absent, err := db.FindCopy(ctx, "entry", Row{"id": "missing"})
				if err != nil || absent != nil {
					return fmt.Errorf("missing view %v: %v", absent, err)
				}
				return nil
			})
		})
	}
}

func TestRowCopyTypedValues(t *testing.T) {
	n := new(big.Int).Lsh(big.NewInt(1), 130)
	expected := new(big.Int).Set(n)
	obj := Row{"n": *n, "groups": map[string][]int{"a": {1, 2}}, "flags": [1][]byte{{5}}}
	v := CopyOnWrite(obj)
	fast := CopyValue(v, true).(Row)
	fast["groups"].(map[string][]int)["a"][0] = 41
	fast["flags"].([1][]byte)[0][0] = 9
	value := fast["n"].(big.Int)
	value.Add(&value, big.NewInt(1))
	orig := obj["n"].(big.Int)
	if orig.Cmp(expected) != 0 || value.Cmp(expected) == 0 || obj["groups"].(map[string][]int)["a"][0] != 1 || obj["flags"].([1][]byte)[0][0] != 5 {
		t.Fatalf("borrowed values changed: %v", obj)
	}
	linked := Row{}
	linked["self"] = linked
	owned := CopyOnWrite(linked).Snapshot()
	owned["self"].(Row)["added"] = true
	if owned["added"] != true || linked["added"] != nil {
		t.Fatal("row cycle points outside the copy")
	}
	child := CopyOnWrite(Row{"a": 1})
	nested := CopyValue(map[string]*RowCopy{"child": child}, false).(map[string]*RowCopy)
	nested["child"].Set("a", 2)
	a, _ := child.Get("a")
	if a != 1 {
		t.Fatal(a)
	}
	child.Set("local", true)
	bytes, err := json.Marshal(child)
	if err != nil || string(bytes) != `{"a":1,"local":true}` {
		t.Fatal(string(bytes), err)
	}
}
