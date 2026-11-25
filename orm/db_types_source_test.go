package orm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/schema"
)

// Compile a caller together with the actual generated model. Positive controls
// use concrete result types, including absent rows and positional batch results.
func compileDBCaller(t *testing.T, model []byte, body string, diagnostics ...string) {
	t.Helper()
	dir := t.TempDir()
	modelFile := filepath.Join(dir, "model.go")
	callerFile := filepath.Join(dir, "caller.go")
	source := `package dbwrites
import ("context"; "github.com/ysh0566/ponder-go/orm"; "github.com/ysh0566/ponder-go/store")
func result[T any](value T, err error) T { return value }
func check(ctx context.Context, db *store.IndexingStore) {
 table := Table.Bind(db)
 _ = table
 _ = orm.Null[int64]
 ` + body + "\n}"
	for name, data := range map[string][]byte{modelFile: model, callerFile: []byte(source)} {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "model.a"), modelFile, callerFile)
	output, err := cmd.CombinedOutput()
	if len(diagnostics) == 0 {
		if err != nil {
			t.Fatalf("valid database caller: %v\n%s", err, output)
		}
		return
	}
	if err == nil {
		t.Fatal("invalid database caller compiled")
	}
	for _, diagnostic := range diagnostics {
		if !strings.Contains(string(output), diagnostic) {
			t.Fatalf("missing %q\n%s", diagnostic, output)
		}
	}
}

func TestSourceDBTypes(t *testing.T) {
	data, err := os.ReadFile("testdata/dbwrites/model.json")
	if err != nil {
		t.Fatal(err)
	}
	var model schema.Schema
	if err = json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	generated, err := orm.GenerateModels("dbwrites", model)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/dbwrites/models.go")
	if err != nil || !bytes.Equal(fixture, generated) {
		t.Fatal("runtime model must match the generator", err)
	}

	t.Run("composite primary key", func(t *testing.T) {
		composite := schema.Schema{Tables: []schema.Table{{Name: "table", PrimaryKey: []string{"id", "other"}, Columns: []schema.Column{
			{Name: "id", Type: "text", NotNull: true}, {Name: "other", Type: "integer", NotNull: true}, {Name: "other_other", Property: "otherOther", Type: "boolean"},
		}}}}
		m, err := orm.GenerateModels("dbwrites", composite)
		if err != nil {
			t.Fatal(err)
		}
		compileDBCaller(t, m, `type Key = TableKey
var constructor func(string,int64) Key = NewTableKey
var _ *TableRow = result(table.Find(ctx,constructor("kevin",9)))`)
		compileDBCaller(t, m, `_ = NewTableKey("kevin")`, "not enough arguments in call to NewTableKey")
		compileDBCaller(t, m, `var unusedKey TableKey`, "declared and not used: unusedKey")
	})

	for _, c := range []struct {
		name, operation string
		calls, types    []string
	}{
		{"find", "Find", []string{`table.Find(ctx,NewTableKey("kevin"))`}, []string{"*TableRow"}},
		{"insert", "Insert", []string{
			`table.Insert(ctx,NewTableInsert("kevin"))`,
			`table.InsertOrIgnore(ctx,NewTableInsert("kevin"))`,
			`table.Upsert(ctx,NewTableInsert("kevin"),TableUpdate{Other:orm.Set(orm.Some(int64(9)))})`,
			`table.UpsertFunc(ctx,NewTableInsert("kevin"),func(row TableRow)(TableUpdate,error){
				other:=row.Other
				if !other.Valid(){other=orm.Some(int64(8))}
				return TableUpdate{Other:orm.Set(other)},nil
			})`,
			`table.InsertManyOrIgnore(ctx,[]TableInsert{NewTableInsert("kevin")})`,
		}, []string{"TableRow", "*TableRow", "TableRow", "TableRow", "[]*TableRow"}},
		{"update", "Update", []string{`table.Update(ctx,NewTableKey("kevin"),TableUpdate{Other:orm.Set(orm.Some(int64(52)))})`}, []string{"TableRow"}},
		{"delete", "Delete", []string{`table.Delete(ctx,NewTableKey("kevin"))`}, []string{"bool"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var positive, unused strings.Builder
			var diagnostics []string
			for i, call := range c.calls {
				name := "unused" + string(rune('A'+i))
				positive.WriteString("var _ " + c.types[i] + " = result(" + call + ")\n")
				unused.WriteString(name + " := result(" + call + ")\n")
				diagnostics = append(diagnostics, "declared and not used: "+name)
			}
			compileDBCaller(t, generated, positive.String())
			compileDBCaller(t, generated, "operation := table."+c.operation+"\noperation = func(){}\n_ = operation", "cannot use func()")
			compileDBCaller(t, generated, unused.String(), diagnostics...)
		})
	}
	t.Run("non-empty table name", func(t *testing.T) {
		cli := filepath.Join(t.TempDir(), "ponder")
		cmd := exec.CommandContext(t.Context(), "go", "build", "-o", cli, "../cmd/ponder")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build model generator: %v\n%s", err, output)
		}
		for _, name := range []string{"table", ""} {
			dir := t.TempDir()
			spec := model
			spec.Tables = append([]schema.Table(nil), model.Tables...)
			spec.Tables[0].Name = name
			raw, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			directive := fmt.Sprintf("//go:generate %q codegen --typed --schema model.json --out models.go --package dbwrites\npackage dbwrites\n", cli)
			for file, data := range map[string][]byte{"input.go": []byte(directive), "model.json": raw} {
				if err := os.WriteFile(filepath.Join(dir, file), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), "go", "generate", "input.go")
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			file := filepath.Join(dir, "models.go")
			if name == "" {
				if err == nil || !bytes.Contains(output, []byte("invalid table name")) {
					t.Fatalf("empty name: %v\n%s", err, output)
				}
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("unexpected generated model", err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("valid table: %v\n%s", err, output)
			}
			code, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(code, generated) {
				t.Fatal("unexpected generated definition", err)
			}
			compileDBCaller(t, code, `_ = Table.Name()`)
		}
	})
}
