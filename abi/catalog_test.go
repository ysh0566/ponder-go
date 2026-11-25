package abi_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ysh0566/ponder-go/abi"
)

func TestABICatalogLargeAndDuplicateDeclarations(t *testing.T) {
	decls := make(map[string]json.RawMessage)
	var order []string
	for i := 0; i < 101; i++ {
		name := fmt.Sprintf("Event%d", i)
		decls[name] = json.RawMessage(fmt.Sprintf(`{"type":"event","name":%q,"inputs":[]}`, name))
		order = append(order, name)
	}
	order = append(order, "Event17", "Event17")
	w := newCatalogWorkspace(t, decls)
	pkg, _ := w.binding(t, order)
	w.caller(t, pkg, "", "", "item:=binding.EventItemEvent17{}\nname:=binding.FormatEvent(item)\ncheck(name.String()==\"Event17\")\nvar _ binding.EventItemEvent17=binding.ParseEvent(name)")
}

func TestABICatalogGoNameCollisions(t *testing.T) {
	w := newCatalogWorkspace(t, map[string]json.RawMessage{
		"underscore": json.RawMessage(`{"type":"event","name":"a_b","inputs":[]}`),
		"camel":      json.RawMessage(`{"type":"event","name":"aB","inputs":[]}`),
	})
	pkg, _ := w.binding(t, []string{"underscore", "camel"})
	w.caller(t, pkg, "", "", `left:=binding.EventItemAB_615f62{}
right:=binding.EventItemAB_6142{}
check(binding.FormatEvent(left).String()=="a_b")
check(binding.FormatEvent(right).String()=="aB")
check(left.UnqualifiedName().String()=="a_b")
check(right.UnqualifiedName().String()=="aB")`)
}

func TestABICatalogValidation(t *testing.T) {
	for _, tc := range []struct{ pkg, input string }{
		{"_", "[]"}, {"for", "[]"}, {"catalog", "null"}, {"catalog", "{"},
		{"catalog", `[{"type":"event","name":"E","inputs":[]},{"type":"event","name":"E","anonymous":true,"inputs":[]}]`},
	} {
		code, err := abi.GenerateCatalog(tc.pkg, []byte(tc.input))
		if err == nil || code != nil {
			t.Fatal("invalid catalog returned source", tc)
		}
	}
	for _, base := range []string{"", "f", "g()", "a b"} {
		if value := abi.NewUnqualifiedFunctionName(base).String(); value != base+"()" {
			t.Fatal(value)
		}
	}
}
