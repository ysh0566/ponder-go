package filter

import (
	"reflect"
	"sync"
	"testing"
)

func TestFilterSessionWholeGraphSnapshot(t *testing.T) {
	session := &Session{}
	shared := []any{"old", Object{"nested": 1}}
	factory := Object{"array": shared}
	f := Object{"include": shared, "factory": factory, "type": "log"}
	snapshot := session.Snapshot([]Object{f, f, factory})
	if reflect.ValueOf(snapshot[0]).Pointer() != reflect.ValueOf(snapshot[1]).Pointer() || reflect.ValueOf(snapshot[0]["factory"]).Pointer() != reflect.ValueOf(snapshot[2]).Pointer() {
		t.Fatal("snapshot aliases lost")
	}
	snapshot[0]["include"].([]any)[0] = "snapshot"
	if snapshot[2]["array"].([]any)[0] != "snapshot" || shared[0] != "old" {
		t.Fatal("array alias/copy broken")
	}
	session.Update(func() { shared[1].(Object)["nested"] = 2; f["new-key"] = "learned" })
	if snapshot[0]["include"].([]any)[1].(Object)["nested"] != 1 || snapshot[0]["new-key"] != nil {
		t.Fatal("snapshot leaked later map mutation")
	}
	chain := Object{"id": 1}
	cb := Object{"chain": chain, "filter": f, "fn": func() {}}
	wrapper := session.Callbacks([]Object{cb})[0]
	if reflect.ValueOf(wrapper["chain"]).Pointer() != reflect.ValueOf(chain).Pointer() || reflect.ValueOf(wrapper).Pointer() == reflect.ValueOf(cb).Pointer() {
		t.Fatal("callback identity policy changed")
	}
	var nilSession *Session
	if reflect.ValueOf(nilSession.Snapshot([]Object{f})[0]).Pointer() != reflect.ValueOf(f).Pointer() {
		t.Fatal("standalone ownership changed")
	}
}
func TestFilterSessionConcurrentLearning(t *testing.T) {
	s := &Session{}
	f := Object{"type": "log", "include": []any{"block.number"}, "nested": Object{"key": 0}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 2000; n++ {
			s.Update(func() { f["include"] = []any{n}; f["nested"].(Object)["key"] = n; f["extra"] = n; delete(f, "extra") })
		}
	}()
	for n := 0; n < 2000; n++ {
		copy := s.Snapshot([]Object{f})[0]
		for key := range copy {
			_ = copy[key]
		}
		_ = copy["nested"].(Object)["key"]
	}
	wg.Wait()
}
