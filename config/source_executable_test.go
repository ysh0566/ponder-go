package config

import (
	"reflect"
	"sync"
	"testing"
)

// Supplemental reconstruction tests: no upstream original-case credit.
func TestExecutableSourceProjectionGraph(t *testing.T) {
	shared := []any{"block.number", "block.timestamp"}
	factory := sourceRecord("type", "log", "addresses", shared)
	f := sourceRecord("type", "log", "address", factory, "include", shared)
	chain := sourceRecord("id", 1, "name", "one")
	calls := 0
	fn := func() { calls++ }
	cb := sourceRecord("filter", f, "chain", chain, "fn", fn)
	b := &SourceIndexingBuild{chains: []*sourceObject{chain}, callbacks: [][]*sourceObject{{cb, cb}}, setup: [][]*sourceObject{{cb}}, contracts: []*sourceObject{sourceRecord("factory", factory)}, functions: []SourceIndexingFunction{{Name: "second", Fn: fn}, {Name: "first", Fn: fn}}, ordering: "multichain"}
	first := b.Executable()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.Executable() != first {
				t.Error("projection rebuilt")
			}
		}()
	}
	wg.Wait()
	same := func(a, b any) bool { return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() }
	if !same(first.EventCallbacks[0][0], first.EventCallbacks[0][1]) || !same(first.EventCallbacks[0][0], first.SetupCallbacks[0][0]) || !same(first.Chains[0], first.EventCallbacks[0][0]["chain"]) {
		t.Fatal("canonical map aliases lost")
	}
	filter := first.EventCallbacks[0][0]["filter"].(map[string]any)
	address := filter["address"].(map[string]any)
	if !same(address, first.Contracts[0]["factory"]) {
		t.Fatal("shared nested graph lost")
	}
	filter["include"].([]any)[0] = "changed"
	if address["addresses"].([]any)[0] != "changed" {
		t.Fatal("actual shared array mutation not observed")
	}
	if first.IndexingFunctions[0]["name"] != "second" || first.IndexingFunctions[1]["name"] != "first" {
		t.Fatal("registration order changed")
	}
	first.EventCallbacks[0][0]["fn"].(func())()
	first.IndexingFunctions[1]["fn"].(func())()
	if calls != 2 {
		t.Fatal("function was converted to marker")
	}
}
func TestExecutableSourceExclusiveSession(t *testing.T) {
	b := (&SourceIndexingBuild{}).Executable()
	session, release, err := b.AcquireSession()
	if err != nil || session == nil {
		t.Fatal(err)
	}
	if _, _, err = b.AcquireSession(); err == nil {
		t.Fatal("concurrent graph owner accepted")
	}
	release()
	release()
	next, end, err := b.AcquireSession()
	if err != nil || next == session {
		t.Fatal("new lease unavailable", err)
	}
	release()
	if _, _, err = b.AcquireSession(); err == nil {
		t.Fatal("stale release revoked new lease")
	}
	end()
}
