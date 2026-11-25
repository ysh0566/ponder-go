package abi_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ponder.local/ponder-go/abi"
)

type catalogCase struct {
	Name, Operation, Layout, Argument string
	Items, Expected                   []string
}

type catalogWorkspace struct {
	dir          string
	declarations map[string]json.RawMessage
	packages     map[string]string
}

func newCatalogWorkspace(t *testing.T, declarations map[string]json.RawMessage) *catalogWorkspace {
	t.Helper()
	dir := t.TempDir()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"go.mod":  "module catalog.test\n\ngo 1.25.0\n",
		"go.work": fmt.Sprintf("go 1.25.0\n\nuse (\n.\n%q\n)\n", root),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &catalogWorkspace{dir: dir, declarations: declarations, packages: make(map[string]string)}
}

func (w *catalogWorkspace) binding(t *testing.T, names []string) (string, string) {
	t.Helper()
	raw := make([]json.RawMessage, 0, len(names))
	for _, name := range names {
		v, ok := w.declarations[name]
		if !ok {
			t.Fatalf("unknown original ABI alias %s", name)
		}
		raw = append(raw, v)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	key := string(data)
	if pkg, ok := w.packages[key]; ok {
		return pkg, key
	}
	pkg := fmt.Sprintf("binding%d", len(w.packages))
	source, err := abi.GenerateCatalog(pkg, data)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(w.dir, pkg)
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "catalog.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	w.packages[key] = pkg
	return pkg, key
}

func (w *catalogWorkspace) caller(t *testing.T, binding, other, declarations, body string, diagnostics ...string) {
	t.Helper()
	var code strings.Builder
	code.WriteString("package main\nimport (\"encoding/json\";\"reflect\";")
	for _, p := range []struct{ alias, path string }{{"binding", "catalog.test/" + binding}, {"other", "catalog.test/" + other}, {"abi", "ponder.local/ponder-go/abi"}} {
		if strings.Contains(declarations+body, p.alias+".") {
			fmt.Fprintf(&code, "%s %q;", p.alias, p.path)
		}
	}
	code.WriteString(`)
func metadata[T any](got T, raw string) {
 var want T
 if err:=json.Unmarshal([]byte(raw),&want);err!=nil {panic(err)}
 if !reflect.DeepEqual(got,want){panic("wrong ABI metadata: "+raw)}
}
func originalABI(items any, original string){
 data,err:=json.Marshal(items);if err!=nil{panic(err)}
 var got,want any
 if err=json.Unmarshal(data,&got);err!=nil{panic(err)}
 if err=json.Unmarshal([]byte(original),&want);err!=nil{panic(err)}
 if !reflect.DeepEqual(got,want){panic("declarations changed: "+string(data))}
}
func check(ok bool){if !ok{panic("unexpected ABI value")}}
`)
	code.WriteString(declarations + "\nfunc main(){\n" + body + "\n}\n")
	caller := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(caller, []byte(code.String()), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "run", caller)
	cmd.Dir = w.dir
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(w.dir, "go.work"))
	output, err := cmd.CombinedOutput()
	if len(diagnostics) > 0 {
		if err == nil {
			t.Fatal("out-of-catalog declaration compiled")
		}
		for _, diagnostic := range diagnostics {
			if !strings.Contains(string(output), diagnostic) {
				t.Fatalf("expected %q: %v\n%s", diagnostic, err, output)
			}
		}
		return
	}
	if err != nil {
		t.Fatalf("ABI caller: %v\n%s\n%s", err, output, code.String())
	}
}

// The aliases are the six independent declarations in the TypeScript input.
// The expected type of each operation comes from its own two assertType calls.
var catalogItemTypes = map[string]string{
	"Event0": "EventItemEvent0", "Event1": "EventItemEvent1", "Event1Overloaded": "EventItemEvent1Bytes32Indexed",
	"Func0": "FunctionItemFunc0", "Func1": "FunctionItemFunc1", "Func1Overloaded": "FunctionItemFunc1Bytes32",
}

func equalCatalogSets(target string, members []string) string {
	return fmt.Sprintf(`type expected interface{%s}
func acceptsExpected[T expected](){}
func acceptsActual[T %s](){}
func actualToExpected[T %s](){acceptsExpected[T]()}
func expectedToActual[T expected](){acceptsActual[T]()}`, strings.Join(members, " | "), target, target)
}

func equalCatalogValue(expected string) string {
	return "var want " + expected + "; want=got; got=want; _=got\n"
}

func TestSourceABIUtilityTypes(t *testing.T) {
	var fixture struct {
		Declarations map[string]json.RawMessage
		Cases        []catalogCase
	}
	data, err := os.ReadFile("testdata/catalog-utilities.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 34 || len(fixture.Declarations) != 6 {
		t.Fatal("original ABI type inventory changed")
	}
	w := newCatalogWorkspace(t, fixture.Declarations)
	all := []string{"Event0", "Event1", "Event1Overloaded", "Func0", "Func1", "Func1Overloaded"}
	other, allJSON := w.binding(t, all)
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			binding, raw := w.binding(t, c.Items)
			if c.Layout == "weak" {
				raw = allJSON
			}
			kind := "Event"
			if strings.Contains(c.Operation, "Function") {
				kind = "Function"
			}
			expected := c.Expected[0]
			nameType := func(name string) string { return "binding." + kind + "Name" + abi.GoFieldName(name) }
			itemType := func(alias string) string {
				typ, ok := catalogItemTypes[alias]
				if !ok {
					t.Fatalf("unknown declaration %q", alias)
				}
				return "binding." + typ
			}
			var declarations, body string
			var diagnostics []string
			switch {
			case strings.HasPrefix(c.Operation, "Extract"):
				var types []string
				for _, wanted := range c.Expected {
					if wanted == "Abi"+kind {
						types = append(types, "abi."+kind)
						continue
					}
					typ := itemType(wanted)
					types = append(types, typ)
					body += fmt.Sprintf("metadata(%s{}.Definition(),%q)\n", typ, fixture.Declarations[wanted])
				}
				declarations = equalCatalogSets("binding."+kind+"Items", types)
			case strings.HasPrefix(c.Operation, "Parse"):
				if expected == "Abi"+kind {
					body = fmt.Sprintf("dynamic,err:=abi.Parse(%q);if err!=nil{panic(err)}\ngot,ok:=dynamic.Find%sSafe(%q);check(ok==%t)\n", raw, kind, c.Argument, c.Layout == "weak")
					body += equalCatalogValue("abi." + kind)
					if c.Layout == "weak" {
						alias := "Event0"
						if kind == "Function" {
							alias = "Func0"
						}
						body += fmt.Sprintf("metadata(got,%q)\n", fixture.Declarations[alias])
					}
				} else {
					body = fmt.Sprintf("got:=binding.Parse%s(%s{})\n", kind, nameType(c.Argument)) + equalCatalogValue(itemType(expected))
					body += fmt.Sprintf("metadata(got.Definition(),%q)\n", fixture.Declarations[expected])
				}
			case strings.HasPrefix(c.Operation, "Format"):
				switch {
				case expected == "never":
					// A declaration outside this ABI has no formattable value.
					body = fmt.Sprintf("_=binding.Format%s(other.%s{})", kind, catalogItemTypes[c.Argument])
					diagnostics = []string{"Format" + kind, catalogItemTypes[c.Argument], "does not satisfy"}
				case c.Layout == "weak":
					base := strings.TrimSuffix(expected, "()")
					body = fmt.Sprintf("item:=other.%s{};got:=item.UnqualifiedName()\n", catalogItemTypes[c.Argument])
					body += equalCatalogValue("other.Bare" + kind + abi.GoFieldName(base))
					body += fmt.Sprintf("check(got.String()==%q)", expected)
				default:
					body = fmt.Sprintf("got:=binding.Format%s(%s{})\n", kind, itemType(c.Argument)) + equalCatalogValue(nameType(expected))
					body += fmt.Sprintf("check(got.String()==%q)", expected)
				}
			case strings.HasPrefix(c.Operation, "Safe"):
				if c.Layout == "weak" {
					switch expected {
					case "string":
						body = "got:=(abi.Event{Name:\"arbitrary\"}).UnqualifiedName()\n" + equalCatalogValue("string") + `check(got=="arbitrary")`
					case "`" + "${string}()" + "`":
						body = "got:=(abi.Function{Name:\"arbitrary\"}).UnqualifiedName()\n" + equalCatalogValue("abi.UnqualifiedFunctionName") + `check(got.String()=="arbitrary()")`
					default:
						t.Fatal("unknown original weak-name type", expected)
					}
				} else {
					var types []string
					for _, literal := range c.Expected {
						typ := nameType(literal)
						types = append(types, typ)
						body += fmt.Sprintf("check((%s{}).String()==%s)\n", typ, strconv.Quote(literal))
					}
					declarations = equalCatalogSets("binding."+kind+"Names", types)
				}
			default:
				t.Fatal("unknown source operation", c.Operation)
			}
			if c.Layout != "weak" {
				var values []string
				for _, alias := range c.Items {
					values = append(values, itemType(alias)+"{}")
				}
				literal := "[" + strconv.Itoa(len(values)) + "]binding.Declaration{" + strings.Join(values, ",") + "}"
				if c.Layout == "array" {
					literal = "[]binding.Declaration{" + strings.Join(values, ",") + "}"
				}
				input := fmt.Sprintf("items:=%s\noriginalABI(items,%q)\n", literal, raw)
				if len(diagnostics) > 0 {
					w.caller(t, binding, other, "", input)
				}
				body = input + body
			}
			w.caller(t, binding, other, declarations, body, diagnostics...)
		})
	}
}
