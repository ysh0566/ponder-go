package historical

// Supplemental managed-plan identity tests, zero original-source credit.
import (
	"reflect"
	"testing"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
)

func managedIdentityFixture() *config.ExecutableSourceIndexingBuild {
	chain := filter.Object{"id": float64(1), "name": "one", "rpc": []any{"http://127.0.0.1:1"}}
	f := filter.Object{"type": "block", "fromBlock": float64(1), "toBlock": float64(3), "interval": float64(1), "include": []any{"block.number"}, "metadata": map[string]any{"include": "preserve"}}
	return &config.ExecutableSourceIndexingBuild{Ordering: "multichain", Chains: []filter.Object{chain}, Contracts: []filter.Object{{"C": map[string]any{"abi": []any{}, "address": filter.Undefined}}}, EventCallbacks: [][]filter.Object{{{"name": "Blocks:block", "fn": func() {}, "chain": chain, "filter": f}}}, SetupCallbacks: [][]filter.Object{{}}, IndexingFunctions: []filter.Object{{"name": "Blocks:block", "fn": func() {}}}, FinalizedBlocks: []filter.Object{{"number": float64(3)}}}
}
func TestManagedPlanStableIdentity(t *testing.T) {
	b := managedIdentityFixture()
	initial, err := managedPlan(b)
	if err != nil {
		t.Fatal(err)
	}
	b.FinalizedBlocks[0]["number"] = float64(999)
	b.Chains[0]["rpc"] = []any{"http://127.0.0.1:2"}
	b.EventCallbacks[0][0]["filter"].(map[string]any)["include"] = []any{"block.number", "block.timestamp"}
	b.IndexingFunctions[0]["fn"] = func() { panic("not executed") }
	got, err := managedPlan(b)
	if err != nil || got.PlanID != initial.PlanID {
		t.Fatalf("nonsemantic changes changed identity: %v", err)
	}
	if got.EndBlocks[1] != 3 || len(got.Chains) != 1 || got.Chains[0].Name != "one" {
		t.Fatal(got)
	}
}
func TestManagedPlanRejectsSemanticDrift(t *testing.T) {
	for _, kind := range []string{"end", "start", "interval", "nested-include", "callback-name", "contract", "chain-window", "chain-unknown"} {
		t.Run(kind, func(t *testing.T) {
			b := managedIdentityFixture()
			before, err := managedPlan(b)
			if err != nil {
				t.Fatal(err)
			}
			f := b.EventCallbacks[0][0]["filter"].(map[string]any)
			switch kind {
			case "end":
				f["toBlock"] = float64(4)
			case "start":
				f["fromBlock"] = float64(2)
			case "interval":
				f["interval"] = float64(2)
			case "nested-include":
				f["metadata"].(map[string]any)["include"] = "changed"
			case "callback-name":
				b.EventCallbacks[0][0]["name"] = "Other:block"
			case "chain-window":
				b.Chains[0]["reorgWindow"] = float64(120)
			case "chain-unknown":
				b.Chains[0]["futureSemantic"] = "bound"
			case "contract":
				b.Contracts[0]["C"].(map[string]any)["address"] = nil
			}
			after, err := managedPlan(b)
			if err != nil {
				t.Fatal(err)
			}
			if after.PlanID == before.PlanID {
				t.Fatal("semantic change not bound")
			}
		})
	}
}
func TestManagedPlanFailClosed(t *testing.T) {
	for _, kind := range []string{"nil", "shape", "cycle", "hidden-function", "wrong-chain"} {
		t.Run(kind, func(t *testing.T) {
			b := managedIdentityFixture()
			switch kind {
			case "nil":
				b = nil
			case "shape":
				b.SetupCallbacks = nil
			case "cycle":
				f := b.EventCallbacks[0][0]["filter"].(map[string]any)
				f["self"] = f
			case "hidden-function":
				b.Contracts[0]["fn"] = func() {}
			case "wrong-chain":
				b.EventCallbacks[0][0]["chain"] = map[string]any{"id": float64(2), "name": "two"}
			}
			if _, err := managedPlan(b); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}
func TestManagedStableUndefinedIsNotNullOrUserMarker(t *testing.T) {
	values := []any{nil, filter.Undefined, map[string]any{"undefined": true}, []any{"undefined"}}
	got := make([]any, len(values))
	for i, v := range values {
		x, err := managedStableValue(reflect.ValueOf(v), map[uintptr]bool{})
		if err != nil {
			t.Fatal(err)
		}
		got[i] = x
	}
	for i := range got {
		for j := 0; j < i; j++ {
			if reflect.DeepEqual(got[i], got[j]) {
				t.Fatalf("collided %d %d", i, j)
			}
		}
	}
}
