package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
)

type applicationCase struct {
	ID, Name string
	Line     int
	Input    json.RawMessage
}

func TestSourceApplicationDeclarations(t *testing.T) {
	var fixture struct{ Cases []applicationCase }
	readJSON(t, "testdata/app-declarations.json", &fixture)
	dir := t.TempDir()
	write := func(path string, data []byte) {
		t.Helper()
		out := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	write("go.mod", []byte("module appfixture.test\n\ngo 1.25.0\n"))
	write("go.work", fmt.Appendf(nil, "go 1.25.0\n\nuse (\n.\n%q\n)\n", root))
	for _, c := range fixture.Cases {
		switch c.Name {
		case "createConfig strict return type", "createConfig weak Abi":
			var input struct {
				Contracts struct{ C2 struct{ ABI json.RawMessage } }
			}
			if err := json.Unmarshal(c.Input, &input); err != nil {
				t.Fatal(err)
			}
			pkg := "events"
			if c.Name == "createConfig weak Abi" {
				pkg = "weak"
			}
			src, err := abi.GenerateCatalog(pkg, input.Contracts.C2.ABI)
			if err != nil {
				t.Fatal(err)
			}
			write(pkg+"/binding.go", src)
		case "factory":
			if c.Line != 43 {
				continue
			}
			var input struct{ Event abi.Event }
			if err := json.Unmarshal(c.Input, &input); err != nil {
				t.Fatal(err)
			}
			src, err := abi.GenerateEventCatalog("market", input.Event)
			if err != nil {
				t.Fatal(err)
			}
			write("market/binding.go", src)
		}
	}
	build := func(args ...string) ([]byte, error) {
		cmd := exec.Command("go", append([]string{"build"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"))
		return cmd.CombinedOutput()
	}
	caller, err := os.ReadFile("testdata/application-declarations/main.go")
	if err != nil {
		t.Fatal(err)
	}
	write("main.go", caller)
	bin := filepath.Join(dir, "caller")
	if out, err := build("-o", bin, "."); err != nil {
		t.Fatalf("build caller: %v\n%s", err, out)
	}
	const imports = `package controls
import ("ponder.local/ponder-go/config"; "appfixture.test/events"; "appfixture.test/weak"; "appfixture.test/market")
var _ = weak.FunctionItemFunc{}
var _ = market.EventItemCreateMarket{}
var event = events.EventItemEvent0{}
`
	checkCompile := func(t *testing.T, declaration string, want ...string) {
		t.Helper()
		write("controls/control.go", []byte(imports+declaration+"\n"))
		out, err := build("./controls")
		if len(want) == 0 {
			if err != nil {
				t.Fatalf("valid caller rejected: %v\n%s", err, out)
			}
			return
		}
		if err == nil {
			t.Fatalf("invalid caller compiled: %s", declaration)
		}
		for _, message := range want {
			if !strings.Contains(string(out), message) {
				t.Fatalf("expected %q in: %s", message, out)
			}
		}
	}
	for _, c := range fixture.Cases {
		t.Run(c.ID, func(t *testing.T) {
			name := c.Name
			switch name {
			case "createConfig no extra properties":
				for _, typ := range []string{"config.ChainDeclaration[int,string]", "config.ContractDeclaration[[1]events.EventItemEvent0,string,struct{}]"} {
					checkCompile(t, "var _ = "+typ+"{}")
					checkCompile(t, "var _ = "+typ+"{A:0}", "unknown field A")
				}
			case "factory with invalid event":
				checkCompile(t, "var _,_ = config.FactoryByParameter(event,event.Parameters().Arg,config.FactoryOptions{Address:\"0x\"})")
				checkCompile(t, "var _,_ = config.FactoryByParameter[weak.FunctionItemFunc](weak.FunctionItemFunc{},event.Parameters().Arg,config.FactoryOptions{Address:\"0x\"})", "wrong type for method Definition", `have Definition() "ponder.local/ponder-go/abi".Function`, `want Definition() "ponder.local/ponder-go/abi".Event`)
				return
			case "factory":
				name = fmt.Sprintf("factory at line %d", c.Line)
			}
			out, err := exec.Command(bin, name).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", name, err, out)
			}
			var got, want any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(c.Input, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s\ngot %s\nwant %s", name, out, c.Input)
			}
		})
	}
	t.Run("different_event_parameter", func(t *testing.T) {
		checkCompile(t, "var _,_ = config.FactoryByParameter[events.EventItemEvent0](event,market.EventItemCreateMarket{}.Parameters().ArgsArg2,config.FactoryOptions{})", "wrong type for method ParameterOf")
	})
}

func readJSON(t *testing.T, path string, dst any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatal(err)
	}
}

func TestSourceFactoryDeclarationControls(t *testing.T) {
	e := abi.Event{Name: "Born", Type: "event", Inputs: []abi.Parameter{{Type: "address", Name: "child", Indexed: true}}}
	for _, key := range []config.DynamicFactoryParameter{"", "unknown"} {
		if _, err := config.FactoryByParameter(e, key, config.FactoryOptions{}); err == nil {
			t.Fatalf("accepted parameter %q", key)
		}
	}
	bad := e
	bad.Type = "function"
	if _, err := config.FactoryByLocation(bad, "topic1", config.FactoryOptions{}); err == nil {
		t.Fatal("accepted a function decoded into event metadata")
	}
	param, err := config.FactoryByParameter(e, config.DynamicFactoryParameter("child"), config.FactoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	param.Event.Inputs = nil
	if _, err := json.Marshal(param); err == nil {
		t.Fatal("serialized a stale factory parameter")
	}
	var missing *config.DynamicFactoryParameter
	if _, err := config.FactoryByParameter(e, missing, config.FactoryOptions{}); err == nil {
		t.Fatal("accepted nil parameter")
	}
	if _, err := config.FactoryByLocation((*abi.Event)(nil), "topic1", config.FactoryOptions{}); err == nil {
		t.Fatal("accepted nil event")
	}
	if _, err := config.FactoryByLocation(e, "topic4", config.FactoryOptions{}); err == nil {
		t.Fatal("accepted topic4")
	}
	if _, err := json.Marshal(config.FactoryDeclaration[abi.Event]{}); err == nil {
		t.Fatal("serialized an incomplete factory")
	}
	f, err := config.FactoryByLocation(e, "topic1", config.FactoryOptions{StartBlock: 0})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(wire, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["location"] != "topic1" || obj["startBlock"] != float64(0) || len(obj) != 3 {
		t.Fatalf("invalid location declaration: %s", wire)
	}
}
