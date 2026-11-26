package orm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/orm/testdata/enums"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestGeneratedEnumModels(t *testing.T) {
	data, err := os.ReadFile("testdata/enums/model.json")
	if err != nil {
		t.Fatal(err)
	}
	var model schema.Schema
	if err := json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	src, err := orm.GenerateModels("enums", model)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := os.ReadFile("testdata/enums/models.go")
	if err != nil || !bytes.Equal(src, checked) {
		t.Fatal("fixture differs from generated model", err)
	}

	choices := []enums.TradeType{enums.TradeTypeBuy(), enums.TradeTypeSell(), enums.TradeTypeValue(), enums.TradeTypePreSale(), enums.TradeTypePreSale2(), enums.TradeType二期()}
	for i, label := range model.Enums[0].Values {
		value, err := enums.ParseTradeType(label)
		if err != nil || value != choices[i] || value.String() != label {
			t.Fatalf("parse %q: %v %v", label, value, err)
		}
	}
	value := enums.TradeTypeSell()
	if err := json.Unmarshal([]byte(`"sell"`), &value); err == nil || value != enums.TradeTypeSell() {
		t.Fatalf("unknown JSON label changed value: %v %v", value, err)
	}
	var zero enums.TradeType
	if zero != enums.TradeTypeBuy() {
		t.Fatal("zero value must represent the first declared label")
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			db := modelStore(t, backend(t, dialect), "enums")
			history := []orm.Nullable[enums.TradeType]{orm.Some(choices[0]), orm.Null[enums.TradeType](), orm.Some(choices[2]), orm.Some(choices[5])}
			input := enums.NewTradeInsert(choices[1], enums.StatusBuy()).WithHistory(orm.Some(history))
			err := db.Apply(ctx, indexed("insert", 1), func(w *store.Writer) error {
				row, err := enums.Trade.Bind(w.Indexing()).Insert(ctx, input)
				if err != nil {
					return err
				}
				data, err := json.Marshal(row)
				if err != nil {
					return err
				}
				if string(data) != `{"history":["BUY",null,"","二期"],"kind":"SELL","previous":null,"state":"BUY"}` {
					return fmt.Errorf("encoded insert result: %s", data)
				}
				var decoded enums.TradeRow
				if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(row, decoded) {
					return fmt.Errorf("JSON round trip: %+v %v", decoded, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			err = db.Apply(ctx, indexed("update", 2), func(w *store.Writer) error {
				table := enums.Trade.Bind(w.Indexing())
				key := enums.NewTradeKey(enums.TradeTypeSell())
				row, err := table.Find(ctx, key)
				if err != nil || row == nil || row.Kind != choices[1] || row.Previous.Valid() || !reflect.DeepEqual(row.History.Value(), history) {
					return fmt.Errorf("find: %+v %v", row, err)
				}
				updated, err := table.Update(ctx, key, enums.TradeUpdate{
					Previous: orm.Set(orm.Some(choices[4])),
					History:  orm.Set(orm.Null[[]orm.Nullable[enums.TradeType]]()),
				})
				if err != nil || updated.Previous.Value() != choices[4] || updated.History.Valid() {
					return fmt.Errorf("update: %+v %v", updated, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGeneratedEnumCallers(t *testing.T) {
	dir := t.TempDir()
	caller := filepath.Join(dir, "caller.go")
	check := func(body string, diagnostics ...string) {
		t.Helper()
		code := "package caller\nimport m \"github.com/ysh0566/ponder-go/orm/testdata/enums\"\n" + body
		if err := os.WriteFile(caller, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "caller.a"), caller).CombinedOutput()
		if (len(diagnostics) == 0) != (err == nil) {
			t.Fatalf("unexpected build result: %v\n%s", err, out)
		}
		for _, diagnostic := range diagnostics {
			if !strings.Contains(string(out), diagnostic) {
				t.Fatalf("missing diagnostic %q in %s", diagnostic, out)
			}
		}
	}
	check(`var _ = m.NewTradeInsert(m.TradeTypeBuy(), m.StatusBuy())`)
	check(`var _ = m.NewTradeKey("SELL")
var _ = m.NewTradeInsert(m.StatusBuy(), m.StatusBuy())
var _ = m.TradeRow{Kind: "typo"}
var _ = m.TradeType(m.StatusBuy())
var _ = m.TradeType{1}`, `cannot use "SELL"`, `cannot use "typo"`, "cannot use m.StatusBuy()", "cannot convert", "unexported field")
}

func TestGeneratedEnumCLIModes(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "ponder")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", cli, "../cmd/ponder").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	// The table and enum intentionally produce overlapping Go identifiers.
	const spec = `{"enums":[{"name":"trade","values":["Row","BUY"]},{"name":"trade_2_buy","values":["", "!", "2", "二期"]}],"tables":[{"name":"trade","columns":[{"name":"id","type":"trade","primary":true},{"name":"history","type":"trade_2_buy[]"}]}]}`
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(spec), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct {
		name  string
		flags []string
		use   string
	}{
		{"plain", nil, `row := Trade{Id:Trade2Buy2(),History:[]*Trade2Buy{nil}}; data,err := json.Marshal(row); if err!=nil || string(data)!="{\"id\":\"BUY\",\"history\":[null]}" { panic(string(data)) }`},
		{"typed", []string{"--typed"}, `_ = NewTradeKey(Trade2Row()); _ = NewTradeInsert(Trade2Buy2()); _ = Trade2BuyValue2()`},
		{"callback", []string{"--typed", "--callback-db"}, `_ = BindCallbackDatabase; _ = NewTradeInsert(Trade2Buy2()); _ = Trade2Buy二期()`},
	} {
		t.Run(mode.name, func(t *testing.T) {
			path := filepath.Join(dir, mode.name+".go")
			args := append([]string{"codegen", "--schema", schemaPath, "--package", "main", "--out", path}, mode.flags...)
			if out, err := exec.CommandContext(t.Context(), cli, args...).CombinedOutput(); err != nil {
				t.Fatalf("codegen: %v\n%s", err, out)
			}
			main := filepath.Join(dir, "main.go")
			source := "package main\nimport \"encoding/json\"\nfunc main(){ _ = json.Marshal; " + mode.use + " }\n"
			if err := os.WriteFile(main, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.CommandContext(t.Context(), "go", "run", path, main).CombinedOutput(); err != nil {
				t.Fatalf("generated caller: %v\n%s", err, out)
			}
		})
	}
}
