package store_test

import (
	"bytes"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/orm"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

func equalCopy(t *testing.T, actual, want any) {
	t.Helper()
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %#v, want %#v", actual, want)
	}
}

func TestSourceRowCopies(t *testing.T) {
	t.Run("copyOnWrite", func(t *testing.T) {
		obj := store.Row{"a": 1, "b": 2}
		v := store.CopyOnWrite(obj)
		for _, a := range []int{1, 3} {
			if a == 3 {
				v.Set("a", a)
			}
			equalCopy(t, obj, store.Row{"a": 1, "b": 2})
			x, ok := v.Get("a")
			if !ok {
				t.Fatal("missing a")
			}
			equalCopy(t, x, a)
			x, ok = v.Get("b")
			if !ok {
				t.Fatal("missing b")
			}
			equalCopy(t, x, 2)
		}
		v.Set("c", 10)
		if _, ok := obj["c"]; ok {
			t.Fatal("source gained c")
		}
		x, ok := v.Get("c")
		if !ok {
			t.Fatal("missing c")
		}
		equalCopy(t, x, 10)
		checkCopyTypes(t, false)
	})
	t.Run("copyOnWrite nested", func(t *testing.T) {
		obj := store.Row{"a": map[string]any{"c": 1}, "b": 2}
		v := store.CopyOnWrite(obj)
		nested, _ := v.Get("a")
		b, _ := v.Get("b")
		equalCopy(t, obj, store.Row{"a": map[string]any{"c": 1}, "b": 2})
		equalCopy(t, nested.(map[string]any)["c"], 1)
		equalCopy(t, b, 2)
		nested.(map[string]any)["c"] = 2
		equalCopy(t, obj["a"].(map[string]any)["c"], 1)
		nested, _ = v.Get("a")
		equalCopy(t, nested.(map[string]any)["c"], 2)
	})
	t.Run("copyOnWrite nested array", func(t *testing.T) {
		obj := store.Row{"a": []int{}}
		v := store.CopyOnWrite(obj)
		a, _ := v.Get("a")
		v.Set("a", append(a.([]int), 1))
		equalCopy(t, obj["a"], []int{})
		a, _ = v.Get("a")
		equalCopy(t, a, []int{1})
	})
	t.Run("copy", func(t *testing.T) {
		obj := store.Row{"a": 1, "b": 2}
		v := store.CopyOnWrite(obj)
		other := store.CopyValue(v, false)
		equalCopy(t, v.Snapshot(), obj)
		equalCopy(t, other, obj)
		v.Set("a", 3)
		equalCopy(t, obj, store.Row{"a": 1, "b": 2})
		equalCopy(t, other, obj)
		equalCopy(t, v.Snapshot(), store.Row{"a": 3, "b": 2})
		equalCopy(t, store.CopyValue([]any{v}, false), []any{store.Row{"a": 3, "b": 2}})
	})
	t.Run("copy shallow", func(t *testing.T) {
		obj := store.Row{"a": 1, "b": "value", "c": big.NewInt(2), "d": true}
		out := store.CopyValue(obj, true).(store.Row)
		equalCopy(t, out, obj)
		out["a"] = 9
		equalCopy(t, obj["a"], 1)
	})
	t.Run("copy bytes", func(t *testing.T) {
		obj := store.Row{"address": "0x" + strings.Repeat("0", 40), "calldata": make([]byte, 20)}
		v := store.CopyOnWrite(obj)
		out := store.CopyValue(v, false).(store.Row)
		b, _ := v.Get("calldata")
		for _, x := range []any{b, out["calldata"]} {
			data, ok := x.([]byte)
			if !ok || !bytes.Equal(data, make([]byte, 20)) {
				t.Fatalf("unexpected bytes %#v", x)
			}
		}
		b.([]byte)[0] = 255
		equalCopy(t, out["calldata"], make([]byte, 20))
		equalCopy(t, obj["calldata"], make([]byte, 20))
	})
	t.Run("copy timestamp", func(t *testing.T) {
		stamp := time.UnixMilli(1742925862000).UTC()
		obj := store.Row{"address": "0x" + strings.Repeat("0", 40), "timestamp": stamp}
		out := store.CopyValue(obj, false).(store.Row)
		equalCopy(t, out["timestamp"], stamp)
	})
	t.Run("copy keeps writes made through copyOnWrite", func(t *testing.T) {
		obj := store.Row{"a": 1, "b": 2}
		v := store.CopyOnWrite(obj)
		v.Set("a", 3)
		want := store.Row{"a": 3, "b": 2}
		for _, fast := range []bool{true, false} {
			equalCopy(t, store.CopyValue(v, fast), want)
		}
		equalCopy(t, store.CopyValue([]any{v}, false), []any{want})
		equalCopy(t, store.CopyValue(map[string]any{"nested": v}, false), map[string]any{"nested": want})
		equalCopy(t, obj, store.Row{"a": 1, "b": 2})
	})
	t.Run("copy does not alias the copyOnWrite target", func(t *testing.T) {
		obj := store.Row{"a": 1, "b": 2}
		v := store.CopyOnWrite(obj)
		out := store.CopyValue(v, true).(store.Row)
		out["a"] = 5
		equalCopy(t, obj["a"], 1)
		a, _ := v.Get("a")
		equalCopy(t, a, 1)
	})
	t.Run("copyOnWrite behaves like a plain object", func(t *testing.T) {
		obj := store.Row{"a": big.NewInt(1), "b": "0x1"}
		v := store.CopyOnWrite(obj)
		out := v.Snapshot()
		equalCopy(t, reflect.TypeOf(out), reflect.TypeOf(obj))
		equalCopy(t, v.Keys(), []string{"a", "b"})
		equalCopy(t, out, obj)
		if !v.Has("a") {
			t.Fatal("missing a")
		}
		equalCopy(t, fmt.Sprint(v), fmt.Sprint(obj))
		v.Set("a", big.NewInt(2))
		equalCopy(t, fmt.Sprint(v), fmt.Sprint(store.Row{"a": big.NewInt(2), "b": "0x1"}))
		equalCopy(t, obj, store.Row{"a": big.NewInt(1), "b": "0x1"})
	})
	t.Run("copyOnWrite rejects arrays at the type level", func(t *testing.T) { checkCopyTypes(t, true) })
}

// Compile the public API and the table-specific type produced by the real ORM
// generator. Each source-language error has a corresponding native diagnostic.
func checkCopyTypes(t *testing.T, array bool) {
	t.Helper()
	dir := t.TempDir()
	model := schema.Schema{Tables: []schema.Table{{Name: "ab", Columns: []schema.Column{{Name: "a", Type: "integer", Primary: true}, {Name: "b", Type: "integer", NotNull: true}}}}}
	generated, err := orm.GenerateModels("rowfixture", model)
	if err != nil {
		t.Fatal(err)
	}
	modelFile := filepath.Join(dir, "models.go")
	if err := os.WriteFile(modelFile, generated, 0600); err != nil {
		t.Fatal(err)
	}
	source := `package rowfixture
import "ponder.local/ponder-go/store"
func check(){
    obj:=store.Row{"a":1,"b":2}
    v:=store.CopyOnWrite(obj)
    original,_:=Ab.DecodeCopy(store.CopyOnWrite(obj))
    copied,_:=Ab.DecodeCopy(v)
    _=original.A
    copied.A=3
    _=copied.B
    // INPUT
}`
	file := filepath.Join(dir, "types.go")
	build := func(input string) (string, error) {
		t.Helper()
		err := os.WriteFile(file, []byte(strings.Replace(source, "// INPUT", input, 1)), 0600)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "output.a"), modelFile, file)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	if out, err := build(""); err != nil {
		t.Fatalf("valid row input: %v\n%s", err, out)
	}
	if array {
		out, err := build("store.CopyOnWrite([]int{1,2})")
		if err == nil || !strings.Contains(out, "as store.Row value in argument to store.CopyOnWrite") {
			t.Fatalf("array diagnostic: %v\n%s", err, out)
		}
		return
	}
	out, err := build("copied.C=10\n_=original.C\n_=copied.C")
	if err == nil || strings.Count(out, "copied.C undefined") != 2 || strings.Count(out, "original.C undefined") != 1 || strings.Count(out, "has no field or method C") != 3 {
		t.Fatalf("field diagnostics: %v\n%s", err, out)
	}
}
