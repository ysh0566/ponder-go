package values_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/values"
)

type utilityCase struct {
	ID, File, Title string
	Input, Output   json.RawMessage
	ExportCount     int    `json:"export_count"`
	ExportsHash     string `json:"exports_sha256"`
	Assertions      []struct {
		Method   string
		Expected json.RawMessage
	}
}

func decode(t *testing.T, b json.RawMessage, target any) {
	t.Helper()
	if err := json.Unmarshal(b, target); err != nil {
		t.Fatal(err)
	}
}
func compare(t *testing.T, got any, want json.RawMessage) {
	t.Helper()
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	decode(t, data, &a)
	decode(t, want, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got %s, want %s", data, want)
	}
}
func TestSourceValueUtilities(t *testing.T) {
	var f struct{ Cases []utilityCase }
	data, err := os.ReadFile("../testdata/value-utilities.json")
	if err != nil {
		t.Fatal(err)
	}
	decode(t, data, &f)
	dir := t.TempDir()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	write := func(file string, data []byte) {
		t.Helper()
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", []byte("module utility.test\n\ngo 1.25.0\n"))
	write("go.work", fmt.Appendf(nil, "go 1.25.0\nuse (\n.\n%q\n)\n", root))
	caller := func(t *testing.T, pkg string, source []byte, body string) json.RawMessage {
		t.Helper()
		write(pkg+"/binding.go", source)
		program := `package main
import ("math/big";"encoding/json";"os"; binding "utility.test/` + pkg + `")
type hex struct{ text string }
func (h hex) MarshalText() ([]byte,error){return []byte(h.text),nil}
func numberToHex(n *big.Int) hex {return hex{"0x"+n.Text(16)}}
func check[T any](v T,err error)T {if err!=nil{panic(err)};return v}
func main(){` + body + `
if err:=json.NewEncoder(os.Stdout).Encode(out);err!=nil{panic(err)}
}
`
		write("caller/main.go", []byte(program))
		cmd := exec.Command("go", "run", "./caller")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("compile/run consumer: %v\n%s", err, out)
		}
		return json.RawMessage(out)
	}
	for _, c := range f.Cases {
		t.Run(c.ID, func(t *testing.T) {
			var got any
			switch filepath.Base(c.File) {
			case "mergeAbis.test.ts":
				var args [][]json.RawMessage
				decode(t, c.Input, &args)
				items, err := abi.MergeABIs(args[0]...)
				if err != nil {
					t.Fatal(err)
				}
				got = items
				binding, err := abi.GenerateMergedCatalog("binding", args[0]...)
				if err != nil {
					t.Fatal(err)
				}
				// Both assignment directions prove the ordered, concrete tuple fields.
				want := "struct{}"
				switch c.Title {
				case "mergeAbis() duplicate items", "mergeAbis() empty abi":
					want = "struct{Item0 binding.FunctionItemA}"
				case "mergeAbis() overloaded items":
					want = "struct{Item0 binding.FunctionItemA; Item1 binding.FunctionItemABytes32}"
				}
				body := "type expected = " + want + "\nvar result expected = binding.ContractABI{}.Items()\nvar _ binding.ContractItems = result\nout:=binding.ContractABI{}"
				compare(t, caller(t, "p"+c.ID, binding, body), c.Output)
			case "replaceBigInts.test.ts":
				var raw any
				decode(t, c.Input, &raw)
				got, err = values.ReplaceBigInts(unmark(raw), func(n *big.Int) string { return "0x" + n.Text(16) })
				if err != nil {
					t.Fatal(err)
				}
				shape, input, expected := "BigInt", "big.NewInt(5)", "hex"
				switch c.Title {
				case "array", "readonly array":
					shape, input, expected = "[1]BigInt", "[1]*big.Int{big.NewInt(5)}", "[1]hex"
				case "object":
					shape = "struct{Kevin struct{Kevin BigInt `json:\"kevin\"`} `json:\"kevin\"`}"
					input = "binding.ReplaceShape[*big.Int]{Kevin:struct{Kevin *big.Int `json:\"kevin\"`}{big.NewInt(5)}}"
					expected = "struct{Kevin struct{Kevin hex `json:\"kevin\"`} `json:\"kevin\"`}"
				}
				binding, err := values.GenerateBigIntReplacer("binding", "Replace", shape)
				if err != nil {
					t.Fatal(err)
				}
				body := "out:=check(binding.Replace(" + input + ",numberToHex))\nvar _ " + expected + " = out"
				compare(t, caller(t, "p"+c.ID, binding, body), c.Output)
			case "truncate.test.ts":
				var args []json.RawMessage
				decode(t, c.Input, &args)
				var text string
				decode(t, args[0], &text)
				var limit []int
				if len(args) == 2 {
					var n int
					decode(t, args[1], &n)
					limit = []int{n}
				}
				got = metrics.Truncate(text, limit...)
			case "chains.test.ts":
				exports, err := config.ChainExports()
				if err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(exports)
				if err != nil {
					t.Fatal(err)
				}
				if len(exports) != c.ExportCount || fmt.Sprintf("%x", sha256.Sum256(data)) != c.ExportsHash {
					t.Fatal("chain exports differ from the pinned ESM namespace")
				}
				return
			default:
				t.Fatalf("unrecognized source %s", c.File)
			}
			compare(t, got, c.Output)
			for _, a := range c.Assertions {
				if a.Method == "toBe" && strings.Contains(c.File, "mergeAbis") {
					compare(t, len(got.([]json.RawMessage)), a.Expected)
				} else {
					compare(t, got, a.Expected)
				}
			}
		})
	}
	t.Run("generated_maps_slices_and_failures", func(t *testing.T) {
		shape := "struct{ BigInt string; Groups map[string][]struct{Value BigInt}; Empty [3]struct{} }"
		binding, err := values.GenerateBigIntReplacer("binding", "Replace", shape)
		if err != nil {
			t.Fatal(err)
		}
		body := `n := big.NewInt(9)
		input := binding.ReplaceShape[*big.Int]{BigInt:"preserved",Groups:map[string][]struct{Value *big.Int}{"key":{{n}}}}
		out := check(binding.Replace(input,func(x *big.Int)hex{return numberToHex(x.Add(x,big.NewInt(1)))}))
		if n.Int64()!=9{panic("input bigint changed")}
		var _ map[string][]struct{Value hex} = out.Groups
		input.Groups["key"][0].Value=nil
		if _,err:=binding.Replace(input,numberToHex);err==nil{panic("accepted nil typed leaf")}
		if _,err:=binding.Replace[hex](input,nil);err==nil{panic("accepted nil callback")}`
		compare(t, caller(t, "nested", binding, body), json.RawMessage(`{"BigInt":"preserved","Groups":{"key":[{"Value":"0xa"}]},"Empty":[{},{},{}]}`))
	})
}

func unmark(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x["$bigint"].(string); ok {
			n, ok := new(big.Int).SetString(s, 10)
			if !ok {
				panic("invalid fixture bigint")
			}
			return n
		}
		r := map[string]any{}
		for k, v := range x {
			r[k] = unmark(v)
		}
		return r
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = unmark(v)
		}
		return r
	default:
		return v
	}
}

func TestValueUtilityBoundaries(t *testing.T) {
	from := json.RawMessage(`[{"type":"error","name":"X","inputs":[{"type":"tuple[]","components":[{"type":"uint"}]}],"extra":true}]`)
	other := json.RawMessage(`[{"type":"function","name":"X","inputs":[{"type":"tuple[]","components":[{"name":"y","type":"uint"}]}]},{"type":"error","name":"X","inputs":[{"type":"tuple[]","components":[{"type":"uint256"}]}]}]`)
	items, err := abi.MergeABIs(from, other)
	if err != nil || len(items) != 2 || !bytes.Contains(items[0], []byte(`"extra":true`)) {
		t.Fatalf("merge returned %s %v", items, err)
	}
	for _, bad := range []string{"null", "{}", `[{"type":"bad"}]`} {
		if _, err := abi.MergeABIs(json.RawMessage(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	n := big.NewInt(5)
	in := map[string]any{"a": n, "b": []any{n, nil, "keep"}}
	out, err := values.ReplaceBigInts(in, func(x *big.Int) string { return x.Add(x, big.NewInt(1)).String() })
	if err != nil || n.Int64() != 5 {
		t.Fatal("mutated source bigint", err)
	}
	compare(t, out, json.RawMessage(`{"a":"6","b":["6",null,"keep"]}`))
	in["cycle"] = in
	if _, err := values.ReplaceBigInts(in, func(x *big.Int) string { return x.String() }); err == nil {
		t.Fatal("accepted cyclic input")
	}
	for _, s := range []string{"[]Unknown", "map[int]BigInt", "struct{ X BigInt; X string }", "struct{ unexported BigInt }"} {
		if _, err := values.GenerateBigIntReplacer("out", "Convert", s); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	units := []uint16{'a', 0xd83d, 0xde00, 'b', 'c', 'd', 'e', 'f', 'g', 'h'}
	want := []uint16{'a', 0xd83d, '.', '.', '.', 'g', 'h'}
	if v := metrics.TruncateUTF16(units, 8); !reflect.DeepEqual(v, want) {
		t.Fatal(v)
	}
	if v := metrics.Truncate("abcdef", 4); v != "...abcdef" {
		t.Fatal(v)
	}
	a, err := config.ChainExports()
	if err != nil {
		t.Fatal(err)
	}
	a[0].Name = "changed"
	b, err := config.ChainExports()
	if err != nil || b[0].Name == a[0].Name {
		t.Fatal("shared mutable exports", err)
	}
}
