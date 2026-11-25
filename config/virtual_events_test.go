package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
)

type eventFieldFixture struct{ Name, Type string }
type eventFixture struct {
	ID, Title, Event, Receipt string
	Fields                    []eventFieldFixture
}

func TestSourceVirtualEvents(t *testing.T) {
	var fixtures struct {
		Cases      []json.RawMessage
		Properties map[string][]string
	}
	readJSON(t, "testdata/virtual-events.json", &fixtures)
	var app struct{ Application json.RawMessage }
	readJSON(t, "testdata/virtual-names.json", &app)
	dir := t.TempDir()
	put := func(p string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, p), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	put("go.mod", []byte("module typed.test\n\ngo 1.25.0\n"))
	put("go.work", fmt.Appendf(nil, "go 1.25.0\nuse (\n.\n%q\n)\n", root))
	cfg, err := config.ParseSourceConfiguration(app.Application)
	if err != nil {
		t.Fatal(err)
	}
	code, err := config.GenerateApplicationEventBindings("app", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "app"), 0700); err != nil {
		t.Fatal(err)
	}
	put("app/bindings.go", code)
	common, err := os.ReadFile("testdata/virtual-event-caller.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	put("caller_test.go", common)
	var caller strings.Builder
	caller.WriteString("package virtual_test\nimport(\"testing\";\"strings\";\"math/big\";\"github.com/ysh0566/ponder-go/abi\";\"github.com/ysh0566/ponder-go/indexing\";\"typed.test/app\")\n")
	var cases []eventFixture
	for _, raw := range fixtures.Cases {
		var c eventFixture
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		writeEventTypeCaller(t, &caller, c, string(raw))
		cases = append(cases, c)
	}
	put("events_test.go", []byte(caller.String()))
	args := []string{"test", "-c", "-o", filepath.Join(dir, "test")}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "-race" && s.Value == "true" {
				args = append(args, "-race")
			}
		}
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile both assignment directions: %v\n%s", err, out)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			out, err := exec.Command(filepath.Join(dir, "test"), "-test.run", "^TestCase"+c.ID+"$", "-test.v").CombinedOutput()
			if err != nil || !strings.Contains(string(out), "--- PASS: TestCase"+c.ID) {
				t.Fatalf("%s: %v\n%s", c.Title, err, out)
			}
		})
	}
	t.Run("rejected_callbacks", func(t *testing.T) {
		if err := os.Mkdir(filepath.Join(dir, "invalid"), 0700); err != nil {
			t.Fatal(err)
		}
		put("invalid/caller.go", []byte(`package invalid
import("context";"typed.test/app";"github.com/ysh0566/ponder-go/indexing")
var _ = app.EventC1Event0{}.TransactionReceipt
var _ = app.EventB1Block{}.Transaction
var _ = app.NameC1Event0{}.On(func(context.Context,app.EventB1Block,*indexing.Context)error{return nil})
`))
		cmd := exec.Command("go", "build", "./invalid")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"))
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("invalid callback compiled")
		}
		for _, part := range []string{"TransactionReceipt undefined", "Transaction undefined", "as func(context.Context, app.EventC1Event0, *indexing.Context) error"} {
			if !strings.Contains(string(out), part) {
				t.Fatalf("missing %q: %s", part, out)
			}
		}
	})
	t.Run("component_fields", func(t *testing.T) {
		views := map[string]any{"TransactionReceipt": indexing.EventReceipt{}, "Trace": indexing.EventTrace{}, "Block": indexing.EventBlock{}, "Transaction": indexing.EventTransaction{}, "Log": indexing.EventLog{}, "Transfer": indexing.EventTransfer{}}
		for name, expected := range fixtures.Properties {
			typ := reflect.TypeOf(views[name])
			var names []string
			for i := 0; i < typ.NumMethod(); i++ {
				names = append(names, typ.Method(i).Name)
			}
			if name == "Transfer" {
				for i := 0; i < typ.NumField(); i++ {
					names = append(names, typ.Field(i).Name)
				}
			}
			want := make([]string, len(expected))
			for i, k := range expected {
				want[i] = abi.GoFieldName(k)
			}
			sort.Strings(names)
			sort.Strings(want)
			if !reflect.DeepEqual(names, want) {
				t.Fatalf("%s: %v != %v", name, names, want)
			}
		}
	})
}

func writeEventTypeCaller(t *testing.T, out *strings.Builder, c eventFixture, raw string) {
	t.Helper()
	typ := "app.Event" + abi.GoFieldName(c.Event)
	name := "app.Name" + abi.GoFieldName(c.Event)
	got := map[string]string{}
	for _, f := range c.Fields {
		switch f.Name {
		case "id":
			got["ID"] = "string"
		case "block", "log", "trace", "transaction":
			got[abi.GoFieldName(f.Name)] = "indexing.Event" + f.Type
		case "transactionReceipt":
			got["TransactionReceipt"] = "indexing.EventReceipt"
		case "transfer":
			got["Transfer"] = "indexing.EventTransfer"
		case "args", "result":
			dest := abi.GoFieldName(f.Name)
			switch strings.Join(strings.Fields(f.Type), " ") {
			case "{ arg: Hex; arg1: Hex; }":
				got[dest] = "struct {Arg abi.Hex `abi:\"arg\"`;Arg1 abi.Hex `abi:\"arg1\"`}"
			case "readonly []":
				got[dest] = "[0]struct{}"
			case "readonly [Hex]", "readonly [Address]":
				got[dest] = "[1]abi.Hex"
			case "never":
				got[dest] = "abi.NoValue"
			case "bigint":
				got[dest] = "*big.Int"
			default:
				t.Fatal(f.Type)
			}
		default:
			t.Fatal(f)
		}
	}
	// TypeScript permits a comparable shape to omit optional properties. The
	// concrete Go representation adds the optional receipt only when present in the inferred type.
	if c.Receipt == "optional" {
		got["TransactionReceipt"] = "*indexing.EventReceipt"
	}
	fmt.Fprintf(out, "type Expected%s = struct{", c.ID)
	for _, field := range strings.Fields("ID Block Transaction Log Trace TransactionReceipt Transfer Args Result") {
		if value := got[field]; value != "" {
			fmt.Fprintf(out, "%s %s\n", field, value)
		}
	}
	fmt.Fprintf(out, "}\nvar _ Expected%s = %s{}\nvar _ %s = Expected%s{}\n", c.ID, typ, typ, c.ID)
	fmt.Fprintf(out, "func TestCase%s(t *testing.T){exercise(t,%q,%s{}.On,func(e %s,c specimen,has bool){if e.ID!=\"checkpoint-1\"{t.Fatal(e.ID)};checkBlock(t,e.Block)\n", c.ID, raw, name, typ)
	if got["Transaction"] != "" {
		out.WriteString("checkTransaction(t,e.Transaction)\n")
	}
	if got["Trace"] != "" {
		out.WriteString("if v,err:=e.Trace.Type();err!=nil||v!=\"CALL\"{t.Fatal(v,err)}\n")
	}
	if got["Log"] != "" {
		out.WriteString("if v,err:=e.Log.LogIndex();err!=nil||v!=2{t.Fatal(v,err)}\n")
	}
	if got["Transfer"] != "" {
		out.WriteString("if e.Transfer.From!=abi.Hex(\"0x\"+strings.Repeat(\"12\",20)) || e.Transfer.To!=abi.Hex(\"0x\"+strings.Repeat(\"34\",20)) || e.Transfer.Value.Int64()!=19{t.Fatal(e.Transfer)}\n")
	}
	if c.Receipt == "required" {
		out.WriteString("checkReceipt(t,e.TransactionReceipt)\n")
	}
	if c.Receipt == "optional" {
		out.WriteString("if has!=(e.TransactionReceipt!=nil){t.Fatal(e.TransactionReceipt)};if has{checkReceipt(t,*e.TransactionReceipt)}\n")
	}
	for _, k := range []string{"Args", "Result"} {
		if got[k] != "" {
			fmt.Fprintf(out, "checkValue(t,e.%s,c.%s)\n", k, k)
		}
	}
	out.WriteString("})}\n")
}

func TestOverloadedContractTupleCallbacks(t *testing.T) {
	const contractABI = `[
		{"type":"function","name":"read","inputs":[{"type":"uint256","name":"key"}],"outputs":[{"type":"uint256"}]},
		{"type":"function","name":"read","inputs":[{"type":"tuple[]","name":"rows","components":[{"type":"bool","name":"a_b"},{"type":"bool","name":"aB"}]}],"outputs":[{"type":"tuple[]","components":[{"type":"bool","name":"a_b"},{"type":"bool","name":"aB"}]}]}
	]`
	parsed, err := abi.Parse(contractABI)
	if err != nil {
		t.Fatal(err)
	}
	_, functions, err := parsed.DeclarationItems()
	if err != nil || len(functions) != 2 {
		t.Fatal(functions, err)
	}
	contract := map[string]any{"chain": "mainnet", "includeCallTraces": true, "abi": json.RawMessage(contractABI)}
	raw, err := json.Marshal(map[string]any{"chains": map[string]any{"mainnet": map[string]any{"id": 1}}, "contracts": map[string]any{"First": contract, "Second": contract}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseSourceConfiguration(raw)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := config.GenerateApplicationEventBindings("app", cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "app"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	write("go.mod", []byte("module calls.test\n\ngo 1.25.0\n"))
	write("go.work", fmt.Appendf(nil, "go 1.25.0\nuse (\n.\n%q\n)\n", root))
	write("app/bindings.go", bindings)
	common, err := os.ReadFile("testdata/virtual-event-caller.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	write("caller_test.go", common)
	var script strings.Builder
	script.WriteString("package virtual_test\nimport (\"testing\"; \"calls.test/app\")\nfunc TestCalls(t *testing.T) {\n")
	word := func(n int) string { return fmt.Sprintf("%064x", n) }
	for _, source := range []string{"First", "Second"} {
		for _, f := range functions {
			name := source + "." + f.Name
			field := abi.GoFieldName(name)
			payload := word(7)
			check := "if e.Args[0].Int64()!=7 || e.Result.Int64()!=7 { t.Fatal(e) }"
			if f.Item.Inputs[0].Type == "tuple[]" {
				payload = word(32) + word(1) + word(0) + word(1)
				check = "if len(e.Args[0])!=1 || len(e.Result)!=1 || e.Args[0][0].AB_615f62 || !e.Args[0][0].AB_6142 || e.Result[0].AB_615f62 || !e.Result[0].AB_6142 { t.Fatal(e) }"
			}
			wire := map[string]any{"input": abi.FunctionSelector(f.Item) + payload, "output": "0x" + payload}
			rawCase, err := json.Marshal(map[string]any{"Event": name, "Item": f.Item, "Wire": wire})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&script, "exercise(t,%q,app.Name%s{}.On,func(e app.Event%s,_ specimen,_ bool){%s})\n", rawCase, field, field, check)
		}
	}
	script.WriteString("}\n")
	write("calls_test.go", []byte(script.String()))
	cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile and dispatch Go callbacks: %v\n%s", err, output)
	}
}
