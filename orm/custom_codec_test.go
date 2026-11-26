package orm_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/orm/testdata/custom"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestGeneratedCustomColumnModels(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			db := modelStore(t, backend(t, dialect), "custom")
			key := custom.NewHoldingKey(7)
			payload := custom.Payload{1, 2}
			history := []orm.Nullable[custom.Units]{orm.Some(custom.Units(5)), orm.Null[custom.Units]()}
			base := custom.NewHoldingInsert(7, payload).WithHistory(orm.Some(history))
			invalid := base.WithLimit(orm.Some(custom.Units(-1)))
			input := invalid.WithLimit(orm.Some(custom.Units(0)))
			payload[0], history[0] = 9, orm.Some(custom.Units(9))
			if err := db.Apply(ctx, indexed("insert", 1), func(w *store.Writer) error {
				table := custom.Holding.Bind(w.Indexing())
				if _, err := table.InsertMany(ctx, []custom.HoldingInsert{input, invalid}); !errors.Is(err, custom.ErrUnits) {
					return fmt.Errorf("expected column encoder error: %v", err)
				}
				if row, err := table.Find(ctx, key); err != nil || row != nil {
					return fmt.Errorf("partially executed batch: %v %v", row, err)
				}
				row, err := table.Insert(ctx, input)
				if err != nil {
					return err
				}
				data, err := json.Marshal(row)
				if err != nil || string(data) != `{"history":[5,null],"id":7,"limit":0,"payload":"AQI="}` {
					return fmt.Errorf("domain JSON or builder snapshot: %s %v", data, err)
				}
				var decoded custom.HoldingRow
				if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, row) {
					return fmt.Errorf("JSON round trip: %+v %v", decoded, err)
				}
				raw, err := w.Indexing().Find(ctx, "holding", store.Row{"id": "u/7"})
				if err != nil || raw == nil || raw["limit"] != "u/0" {
					return fmt.Errorf("native storage value: %v %v", raw, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Apply(ctx, indexed("update", 2), func(w *store.Writer) error {
				table := custom.Holding.Bind(w.Indexing())
				if _, err := table.Find(ctx, custom.NewHoldingKey(-1)); !errors.Is(err, custom.ErrUnits) {
					return fmt.Errorf("invalid key: %v", err)
				}
				bad := []orm.Nullable[custom.Units]{orm.Null[custom.Units](), orm.Some(custom.Units(-1))}
				_, err := table.Update(ctx, key, custom.HoldingUpdate{History: orm.Set(orm.Some(bad))})
				if !errors.Is(err, custom.ErrUnits) || !strings.Contains(err.Error(), "holding.history: element 1") {
					return fmt.Errorf("array encoder error: %v", err)
				}
				row, err := table.UpdateFunc(ctx, key, func(old custom.HoldingRow) (custom.HoldingUpdate, error) {
					if !old.Limit.Valid() || old.Limit.Value() != 0 || old.History.Value()[0].Value() != 5 {
						return custom.HoldingUpdate{}, fmt.Errorf("invalid typed update input: %+v", old)
					}
					empty := []orm.Nullable[custom.Units]{}
					return custom.HoldingUpdate{Limit: orm.Set(orm.Null[custom.Units]()), History: orm.Set(orm.Some(empty))}, nil
				})
				if err != nil || row.Limit.Valid() || !row.History.Valid() || len(row.History.Value()) != 0 {
					return fmt.Errorf("NULL or empty array update: %+v %v", row, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Revert(ctx, indexed("", 1).Checkpoint); err != nil {
				t.Fatal(err)
			}
			if err := db.Apply(ctx, indexed("read", 2), func(w *store.Writer) error {
				table := custom.Holding.Bind(w.Indexing())
				row, err := table.Find(ctx, key)
				if err != nil || row == nil || !row.Limit.Valid() || row.History.Value()[0].Value() != 5 {
					return fmt.Errorf("reverted domain values: %+v %v", row, err)
				}
				_, err = w.Indexing().Update(ctx, "holding", store.Row{"id": "u/7"}, store.Row{"limit": "invalid"})
				if err != nil {
					return err
				}
				if _, err = table.Find(ctx, key); !errors.Is(err, custom.ErrUnits) || !strings.Contains(err.Error(), "holding.limit") {
					return fmt.Errorf("stored value decode error: %v", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCustomColumnCodegen(t *testing.T) {
	raw, err := os.ReadFile("testdata/custom/model.json")
	if err != nil {
		t.Fatal(err)
	}
	var model schema.Schema
	if err := json.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	generated, err := orm.GenerateCallbackDatabase("custom", model)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := os.ReadFile("testdata/custom/models.go")
	if err != nil || !bytes.Equal(generated, checked) {
		t.Fatalf("checked-in callback database differs: %v", err)
	}
	for _, spec := range []schema.GoColumnCodec{
		{Type: "Price", Name: "Price"}, {Type: "HoldingRow", Name: "PriceCodec"}, {Type: "Units", Name: "bad()"},
	} {
		model.Tables[0].Columns[0].GoCodec = &spec
		if _, err := orm.GenerateModels("custom", model); err == nil || !strings.Contains(err.Error(), "holding.id") {
			t.Fatalf("accepted invalid binding %+v: %v", spec, err)
		}
	}
}

func TestCustomColumnCLIModes(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "ponder")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", cli, "../cmd/ponder").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	domain, err := os.ReadFile("testdata/custom/codec.go")
	if err != nil {
		t.Fatal(err)
	}
	domainPath := filepath.Join(dir, "codec.go")
	if err = os.WriteFile(domainPath, domain, 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct {
		name  string
		flags []string
		usage string
	}{
		{"plain", nil, "var _ = Holding{Id: Units(3), History: []*Units{nil}}"},
		{"typed", []string{"--typed"}, "var _ = NewHoldingKey(Units(3))"},
		{"callback", []string{"--typed", "--callback-db"}, "var _ = BindCallbackDatabase"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			output := filepath.Join(dir, "model.go")
			args := append([]string{"codegen", "--schema", "testdata/custom/model.json", "--package", "custom", "--out", output}, mode.flags...)
			if out, err := exec.CommandContext(t.Context(), cli, args...).CombinedOutput(); err != nil {
				t.Fatalf("codegen: %v: %s", err, out)
			}
			caller := filepath.Join(dir, "caller.go")
			if err := os.WriteFile(caller, []byte("package custom\n"+mode.usage+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "caller.a"), output, domainPath, caller).CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, out)
			}
		})
	}
}
