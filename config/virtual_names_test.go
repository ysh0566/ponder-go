package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
)

type virtualChain struct {
	Name string
	ID   uint64
}
type virtualCase struct {
	ID, Title, Event string
	Names            []string
	Chains           []virtualChain
	Configuration    json.RawMessage
	Dynamic          []string `json:"dynamic_abis"`
}

func writeVirtualPackage(t *testing.T, dir, pkg string, raw []byte, dynamic ...string) {
	t.Helper()
	cfg, err := config.ParseSourceConfiguration(raw)
	if err != nil {
		t.Fatal(err)
	}
	src, err := config.GenerateApplicationBindings(pkg, cfg, dynamic...)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, pkg), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, pkg, "generated.go"), src, 0600); err != nil {
		t.Fatal(err)
	}
}

// Build a downstream caller with both directions of each source assertion.
// Running the resulting binary also checks the labels and handler chain IDs.
func TestSourceVirtualNames(t *testing.T) {
	var fixture struct {
		Application json.RawMessage
		Cases       []virtualCase
	}
	readJSON(t, "testdata/virtual-names.json", &fixture)
	dir := t.TempDir()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	put := func(name, s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put("go.mod", "module virtual.test\n\ngo 1.25.0\n")
	put("go.work", fmt.Sprintf("go 1.25.0\nuse (\n.\n%q\n)\n", root))
	writeVirtualPackage(t, dir, "app", fixture.Application)
	var caller strings.Builder
	caller.WriteString("package virtual_test\nimport (\"testing\"; \"context\"; \"ponder.local/ponder-go/indexing\"; \"virtual.test/app\"\n")
	for i, c := range fixture.Cases {
		if c.Configuration != nil {
			pkg := fmt.Sprintf("names%d", i)
			writeVirtualPackage(t, dir, pkg, c.Configuration, c.Dynamic...)
			fmt.Fprintf(&caller, "%q\n", "virtual.test/"+pkg)
		}
	}
	caller.WriteString(")\n")
	for i, c := range fixture.Cases {
		if c.Configuration != nil {
			writeVirtualNameCaller(&caller, i, c)
		} else {
			writeVirtualChainCaller(&caller, i, c)
		}
	}
	put("caller_test.go", caller.String())
	registration, err := os.ReadFile("testdata/virtual-registration.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	put("registration_test.go", string(registration))
	bin := filepath.Join(dir, "caller")
	args := []string{"test", "-c", "-o", bin}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-race" && setting.Value == "true" {
				args = append(args, "-race")
			}
		}
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile external caller: %v\n%s", err, output)
	}
	for _, c := range fixture.Cases {
		t.Run(c.ID, func(t *testing.T) {
			output, err := exec.Command(bin, "-test.v", "-test.run", "^TestCase"+c.ID+"$").CombinedOutput()
			if err != nil || !strings.Contains(string(output), "--- PASS: TestCase"+c.ID) {
				t.Fatalf("%s: %v\n%s", c.Title, err, output)
			}
		})
	}
	t.Run("executable_registration", func(t *testing.T) {
		output, err := exec.Command(bin, "-test.v", "-test.run", "^TestRegisterGeneratedBlock$").CombinedOutput()
		if err != nil || !strings.Contains(string(output), "--- PASS: TestRegisterGeneratedBlock") {
			t.Fatalf("executable registration: %v\n%s", err, output)
		}
	})
}

func checkVirtualSets(out *strings.Builder, index int, want []string, got string) {
	fmt.Fprintf(out, "type expected%d interface{%s}\ntype actual%d interface{%s}\n", index, strings.Join(want, " | "), index, got)
	fmt.Fprintf(out, "func expectedValue%d[T expected%d](T){}\nfunc actualValue%d[T actual%d](T){}\n", index, index, index, index)
	fmt.Fprintf(out, "func both%d[T expected%d,U actual%d](x T,y U){actualValue%d(x);expectedValue%d(y)}\n", index, index, index, index, index)
}

func writeVirtualNameCaller(out *strings.Builder, index int, c virtualCase) {
	pkg := fmt.Sprintf("names%d", index)
	fields := abi.GoFieldNames(c.Names)
	var types []string
	for _, name := range c.Names {
		types = append(types, pkg+".Name"+fields[name])
	}
	checkVirtualSets(out, index, types, pkg+".EventNames")
	fmt.Fprintf(out, "func TestCase%s(t *testing.T){", c.ID)
	out.WriteString("called:=0; fn:=indexing.Handler(func(context.Context,*indexing.HandlerArgs)error{called++;return nil})\n")
	for i, label := range c.Names {
		fmt.Fprintf(out, "{n:=%s{}; r:=n.IndexingFunction(fn); if r.Name!=%q || n.String()!=%q{t.Fatal(r.Name)}; if err:=r.Fn.(indexing.Handler)(t.Context(),nil);err!=nil{t.Fatal(err)}}\n", types[i], label, label)
	}
	fmt.Fprintf(out, "if called!=%d{t.Fatal(called)}}\n", len(types))
}

func writeVirtualChainCaller(out *strings.Builder, index int, c virtualCase) {
	var types []string
	for _, chain := range c.Chains {
		typ := "app.Chain" + abi.GoFieldName(chain.Name)
		if !slices.Contains(types, typ) {
			types = append(types, typ)
		}
	}
	result := "ChoiceOfChains1"
	if len(types) == 1 {
		result = strings.TrimPrefix(types[0], "app.")
	}
	checkVirtualSets(out, index, types, "app.MembersOf"+result)
	resolve := "app.ResolveChain"
	if c.Event != "" {
		resolve = "app.Name" + abi.GoFieldName(c.Event) + "{}.Chain"
	}
	fmt.Fprintf(out, "func TestCase%s(t *testing.T){var resolve func(*indexing.Context)(app.%s,error)=%s\n", c.ID, result, resolve)
	for _, chain := range c.Chains {
		fmt.Fprintf(out, "{v,err:=resolve(&indexing.Context{Chain:indexing.Object{\"name\":%q,\"id\":uint64(%d)}}); if err!=nil{t.Fatal(err)}; if v.Name()!=%q || v.ID()!=%d{t.Fatal(v)}; if _,ok:=any(v).(app.Chain%s);!ok{t.Fatalf(\"unexpected chain %%T\",v)}}\n", chain.Name, chain.ID, chain.Name, chain.ID, abi.GoFieldName(chain.Name))
	}
	out.WriteString("for _,bad:=range []*indexing.Context{nil,{Chain:indexing.Object{\"name\":\"mainnet\",\"id\":10}},{Chain:indexing.Object{\"name\":\"other\",\"id\":1}}")
	if len(types) == 1 {
		out.WriteString(",{Chain:indexing.Object{\"name\":\"optimism\",\"id\":10}}")
	}
	out.WriteString("}{if _,err:=resolve(bad);err==nil{t.Fatal(\"accepted invalid chain\",bad)}}}\n")
}

func TestVirtualNamesControls(t *testing.T) {
	cfg, err := config.ParseSourceConfiguration([]byte(`{"contracts":{"token":{"abi":[{"type":"event","name":"Hidden","inputs":[]}],"chain":""}},"blocks":{"a-b":{},"a_b":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	code, err := config.GenerateApplicationBindings("example", cfg, "token")
	if err != nil {
		t.Fatal(err)
	}
	text := string(code)
	if strings.Contains(text, "Hidden") || !strings.Contains(text, "NameTokenSetup struct") || !strings.Contains(text, "NameABBlock_612d623a626c6f636b struct") || !strings.Contains(text, "NameABBlock_615f623a626c6f636b struct") {
		t.Fatal(text)
	}
	if _, err := config.GenerateApplicationBindings("example", cfg, "unknown"); err == nil {
		t.Fatal("unknown dynamic ABI accepted")
	}
	for _, raw := range []string{`{"chains":{"a":{"id":1}},"blocks":{"b":{"chain":"missing"}}}`, `{"chains":{"a":{"id":1.5}}}`, `{"contracts":{"a":{"abi":[{"type":"event","name":"setup","inputs":[]}]}}}`} {
		cfg, err := config.ParseSourceConfiguration([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := config.GenerateApplicationBindings("example", cfg); err == nil {
			t.Fatal(raw)
		}
	}
}
