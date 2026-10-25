package indexing

import (
	"errors"
	"reflect"
	"testing"

	"ponder.local/ponder-go/filter"
)

type accessErrorState struct{ err error }

func (h *accessErrorState) GetRetryableError() error    { return h.err }
func (h *accessErrorState) SetRetryableError(err error) { h.err = err }
func (h *accessErrorState) ClearRetryableError()        { h.err = nil }

func newAccessTestView(t *testing.T, component string, body Object) (*PropertyView, ColumnAccessPattern, *accessErrorState, *int) {
	t.Helper()
	pattern := NewColumnAccessPattern([]string{"Token:Transfer"})
	handler := &accessErrorState{}
	resets := new(int)
	view, err := NewPropertyView(component, pattern, handler, func(name string) {
		if name != "Token:Transfer" {
			t.Fatalf("reset wrong event: %s", name)
		}
		if pattern[name].Resolved {
			t.Fatal("resolved must clear before reset")
		}
		pattern[name].Count = 0
		*resets++
	})
	if err != nil {
		t.Fatal(err)
	}
	view.Bind("Token:Transfer", body)
	return view, pattern, handler, resets
}

func TestPropertyViewReadOrderAndPresence(t *testing.T) {
	obj := Object{"gas": filter.Undefined, "maxFeePerGas": nil, "extra": "kept"}
	v, pattern, handler, resets := newAccessTestView(t, "transaction", obj)
	if value, err := v.Get("gas"); err != nil || !filter.IsUndefined(value) {
		t.Fatalf("present undefined %v %v", value, err)
	}
	if value, err := v.Get("maxFeePerGas"); err != nil || value != nil {
		t.Fatalf("present null %v %v", value, err)
	}
	if _, err := v.Get("gas"); err != nil {
		t.Fatal(err)
	}
	if value, err := v.Get("extra"); err != nil || value != "kept" {
		t.Fatalf("extra %v %v", value, err)
	}
	if value, err := v.Get("unknown"); err != nil || !filter.IsUndefined(value) {
		t.Fatalf("unknown %v %v", value, err)
	}
	if !reflect.DeepEqual(pattern["Token:Transfer"].Transaction, []string{"gas", "maxFeePerGas"}) {
		t.Fatal(pattern)
	}
	if handler.err != nil || *resets != 0 {
		t.Fatalf("unexpected invalidation %v %d", handler.err, *resets)
	}
}

func TestPropertyViewInvalidAccessStickyAndMutation(t *testing.T) {
	for _, operation := range []string{"get", "set", "delete"} {
		t.Run(operation, func(t *testing.T) {
			body := Object{"gas": 7}
			view, pattern, handler, resets := newAccessTestView(t, "transaction", body)
			profile := pattern["Token:Transfer"]
			profile.Resolved, profile.Count = true, 1001
			var err error
			switch operation {
			case "get":
				_, err = view.Get("maxPriorityFeePerGas")
			case "set":
				err = view.Set("maxPriorityFeePerGas", 99)
			case "delete":
				err = view.Delete("maxPriorityFeePerGas")
			}
			var invalid *InvalidEventAccessError
			if !errors.As(err, &invalid) || invalid.Key != "transaction.maxPriorityFeePerGas" || invalid.Error() != "" || !invalid.Retryable() || invalid.ErrorName() != "InvalidEventAccessError" {
				t.Fatalf("wrong invalid error %#v", err)
			}
			if handler.err != err {
				t.Fatal("sticky error identity lost")
			}
			if profile.Resolved || profile.Count != 0 || *resets != 1 {
				t.Fatalf("reset state %#v %d", profile, *resets)
			}
			if !reflect.DeepEqual(profile.Transaction, []string{"maxPriorityFeePerGas"}) {
				t.Fatal(profile.Transaction)
			}
			if _, present := body["maxPriorityFeePerGas"]; present {
				t.Fatal("failed mutation changed missing property")
			}
			if _, next := view.Get("gas"); next != nil || handler.err != err {
				t.Fatal("successful read must not clear caught retryable error")
			}
		})
	}
}

func TestPropertyViewWritesDeleteAndBindingIdentity(t *testing.T) {
	first := Object{"number": 1, "custom": 8}
	view, pattern, _, _ := newAccessTestView(t, "block", first)
	if err := view.Set("number", 2); err != nil || first["number"] != 2 {
		t.Fatalf("write %v", err)
	}
	if err := view.Set("added", 9); err != nil || first["added"] != 9 {
		t.Fatalf("unknown write %v", err)
	}
	if err := view.Delete("number"); err != nil {
		t.Fatal(err)
	}
	if _, ok := first["number"]; ok {
		t.Fatal("delete didn't mutate original")
	}
	if err := view.Delete("absentUnknown"); err != nil {
		t.Fatal(err)
	}
	retained := view
	second := Object{"number": 5}
	view.Bind("Token:Transfer", second)
	if v, err := retained.Get("number"); err != nil || v != 5 || retained != view {
		t.Fatalf("binding identity %v %v", v, err)
	}
	if !reflect.DeepEqual(pattern["Token:Transfer"].Block, []string{"number"}) {
		t.Fatal(pattern)
	}
}

func TestPropertyViewHasKeysAndDisabledInspection(t *testing.T) {
	body := Object{"custom": 1}
	view, pattern, handler, resets := newAccessTestView(t, "block", body)
	if !view.Has("number") || view.Has("custom") {
		t.Fatal("has uses default set, not actual keys")
	}
	keys := view.Keys()
	if len(keys) != 19 || keys[0] != "baseFeePerGas" || keys[len(keys)-1] != "transactionsRoot" {
		t.Fatal(keys)
	}
	keys[0] = "mutated"
	if view.Keys()[0] != "baseFeePerGas" {
		t.Fatal("keys escaped mutable default set")
	}
	if len(pattern["Token:Transfer"].Block) != 0 {
		t.Fatal("has/keys recorded reads")
	}
	view.SetDisabled(true)
	if value, err := view.Get("number"); err != nil || !filter.IsUndefined(value) {
		t.Fatalf("disabled read %v %v", value, err)
	}
	if err := view.Set("number", 10); err != nil || body["number"] != 10 {
		t.Fatalf("disabled missing set %v", err)
	}
	if err := view.Delete("timestamp"); err != nil {
		t.Fatal(err)
	}
	if !view.Has("timestamp") || view.Has("custom") {
		t.Fatal("disable must not change has trap")
	}
	if _, err := view.Inspect(); err != nil {
		t.Fatalf("disabled inspect %v", err)
	}
	if handler.err != nil || *resets != 0 || len(pattern["Token:Transfer"].Block) != 0 {
		t.Fatal("diagnostic access recorded")
	}
	view.SetDisabled(false)
	if _, err := view.Inspect(); err == nil {
		t.Fatal("inspect must actually read each default field")
	}
	if !reflect.DeepEqual(pattern["Token:Transfer"].Block, []string{"baseFeePerGas"}) {
		t.Fatal(pattern)
	}
}

func TestPropertyViewComponentsAndValidation(t *testing.T) {
	for _, component := range []string{"block", "transaction", "transactionReceipt", "trace"} {
		t.Run(component, func(t *testing.T) {
			body := Object{}
			for _, key := range accessDefaultColumns[component] {
				body[key] = key
			}
			view, pattern, _, _ := newAccessTestView(t, component, body)
			got, err := view.Inspect()
			if err != nil || !reflect.DeepEqual(body, got) {
				t.Fatalf("inspect %v %v", got, err)
			}
			if !reflect.DeepEqual(*pattern["Token:Transfer"].columns(component), view.Keys()) {
				t.Fatal("inspect read order mismatch")
			}
		})
	}
	pattern := NewColumnAccessPattern([]string{"x"})
	var nilHandler *accessErrorState
	if _, err := NewPropertyView("block", pattern, nilHandler, func(string) {}); err == nil {
		t.Fatal("typed nil handler accepted")
	}
	if _, err := NewPropertyView("log", pattern, &accessErrorState{}, func(string) {}); err == nil {
		t.Fatal("log proxy accepted")
	}
	view, err := NewPropertyView("block", pattern, &accessErrorState{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = view.Get("number"); err == nil {
		t.Fatal("unbound access accepted")
	}
	view.Bind("missing", Object{"number": 1})
	if _, err = view.Get("number"); err == nil {
		t.Fatal("uninitialized profile accepted")
	}
}

func TestColumnIncludeSourceOrder(t *testing.T) {
	profile := &ColumnAccessProfile{Transaction: []string{"gas", "maxFeePerGas"}}
	expected := []string{
		"transaction.gas", "transaction.maxFeePerGas", "log.address", "log.data", "log.logIndex", "log.removed", "log.topics",
		"transaction.transactionIndex", "transaction.from", "transaction.to", "transaction.hash", "transaction.type", "block.timestamp", "block.number", "block.hash",
	}
	if got := ResolveProfileInclude(profile, "log", false); !reflect.DeepEqual(got, expected) {
		t.Fatalf("original snapshot\ngot %v\nwant %v", got, expected)
	}
	profile.Block = []string{"hash", "number"}
	profile.Transaction = []string{"from", "gas", "gas"}
	profile.TransactionReceipt = []string{"status"}
	profile.Trace = []string{"to"}
	got := ResolveProfileInclude(profile, "trace", true)
	prefix := []string{"block.hash", "block.number", "transaction.from", "transaction.gas", "transactionReceipt.status", "trace.to"}
	if !reflect.DeepEqual(got[:len(prefix)], prefix) {
		t.Fatal(got)
	}
	seen := map[string]bool{}
	for _, field := range got {
		if seen[field] {
			t.Fatalf("duplicate %s", field)
		}
		seen[field] = true
	}
	for _, kind := range []string{"block", "log", "transaction", "trace", "transfer"} {
		for _, receipts := range []bool{false, true} {
			full := DefaultFilterInclude(kind, receipts)
			required := RequiredFilterInclude(kind, receipts)
			if len(full) == 0 || len(required) == 0 {
				t.Fatal(kind)
			}
			all := map[string]bool{}
			for _, key := range full {
				all[key] = true
			}
			for _, key := range required {
				if !all[key] {
					t.Fatalf("required outside default: %s %s", kind, key)
				}
			}
			full[0] = "changed"
			if DefaultFilterInclude(kind, receipts)[0] == "changed" {
				t.Fatal("escaped include array")
			}
		}
	}
	if got := RequiredFilterInclude("transaction", true); !reflect.DeepEqual(got, RequiredFilterInclude("transaction", false)) {
		t.Fatal("transaction receipt duplicated")
	}
}
