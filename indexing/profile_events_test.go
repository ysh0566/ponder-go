package indexing

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"sync"
	"testing"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

func TestServiceProfileEventsProjection(t *testing.T) {
	cb := Object{"name": "T:E", "fn": Handler(func(context.Context, *HandlerArgs) error { return nil })}
	body := Object{"args": Object{"z": big.NewInt(7), "a": big.NewInt(7)}}
	event := Object{"type": "log", "chain": Object{"id": 1}, "eventCallback": cb, "event": body}
	calls := 0
	service := &Service{options: Options{ColumnAccessPattern: NewColumnAccessPattern([]string{"T:E"}), EventCount: map[string]uint64{"T:E": 41}, FieldOrder: func(got Object) map[string][]string {
		calls++
		if objectID(got) != objectID(event) {
			t.Fatal("projection reconstructed event")
		}
		return map[string][]string{"args": {"z", "a"}}
	}}}
	got, err := service.ProfileEvents([]Object{event, event})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || calls != 2 || got[0].Count != 41 || got[1].Count != 41 || got[0].ChainID != 1 || got[0].Type != "log" || got[0].Callback != "T:E" || !reflect.DeepEqual(got[0].FieldOrder, map[string][]string{"args": {"z", "a"}}) || reflect.ValueOf(got[0].Data).Pointer() != reflect.ValueOf(body).Pointer() {
		t.Fatal("prefetch projection diverged", got, calls)
	}
	if service.EventCount()["T:E"] != 41 || service.ColumnAccessPattern()["T:E"].Count != 0 {
		t.Fatal("prefetch projected future counts")
	}
	service.EventCount()["T:E"] = 42
	next, err := service.ProfileEvents([]Object{event})
	if err != nil || next[0].Count != 42 {
		t.Fatal("current count not used", next, err)
	}
	event["chain"] = Object{"id": "invalid"}
	if _, err = service.ProfileEvents([]Object{event}); err == nil {
		t.Fatal("invalid chain accepted")
	}
	if _, err = service.ProfileEvents([]Object{{}}); err == nil {
		t.Fatal("missing real callback accepted")
	}
}

type profileUnusedRequester struct{}

func (profileUnusedRequester) Call(context.Context, string, any, any) error {
	return fmt.Errorf("unexpected RPC in pure service projection test")
}

// Real Service include reset/convergence with a non-writing adapter; this tests
// synchronization, not database behavior. No synthetic SQL results are claimed.
func TestServiceFilterLearningSession(t *testing.T) {
	session := &filter.Session{}
	state := &ErrorState{}
	chain := Object{"id": 1, "name": "one"}
	f := Object{"type": "log", "include": DefaultFilterInclude("log", false)}
	cb := Object{"name": "T:E", "filter": f, "fn": Handler(func(context.Context, *HandlerArgs) error { return nil })}
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{profileUnusedRequester{}})
	if err != nil {
		t.Fatal(err)
	}
	resolved := 0
	service, err := NewService(Options{Build: Build{FilterSession: session, Chains: []Object{chain}, Contracts: []Object{{}}, EventCallbacks: [][]Object{{cb}}, IndexingFunctions: []Object{{"name": "T:E"}}}, Database: NewDatabase(&store.IndexingStore{}, state), Clients: clients, Errors: state, OnResolved: func(map[string]int) { session.Update(func() { resolved++ }) }})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 1000; n++ {
			copy := session.Snapshot([]filter.Object{f})[0]
			for key := range copy {
				_ = copy[key]
			}
		}
	}()
	for n := 0; n < 1000; n++ {
		service.resetFilterInclude("T:E")
		service.ColumnAccessPattern()["T:E"].Count = 100
		service.ColumnAccessPattern()["T:E"].Block = []string{"number"}
		if err = service.ProcessHistoricalEvents(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if resolved != 1000 || reflect.ValueOf(service.filters["T:E"][0]).Pointer() != reflect.ValueOf(f).Pointer() {
		t.Fatal("canonical learned filter replaced", resolved)
	}
}
