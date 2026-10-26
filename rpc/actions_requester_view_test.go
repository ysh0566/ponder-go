package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// Uses real action pagination and receipt fallback; no direct metadata seeding.
func TestActionsRequesterViewsRetainLearning(t *testing.T) {
	ctx := context.Background()
	query := map[string]any{"fromBlock": "0x0", "toBlock": "0x2c"}
	once := true
	ownerRequests := &actionMock{answer: func(r ActionRequest) (any, error) {
		switch r.Method {
		case "eth_getLogs":
			if once {
				once = false
				return nil, &Error{Message: "Max range: 20"}
			}
			return []map[string]any{}, nil
		case "eth_getBlockReceipts":
			return nil, errors.New("unsupported")
		default:
			return actionReceipt(), nil
		}
	}}
	owner := NewActions(ownerRequests)
	if _, err := drainPages(owner.GetLogsWithPagination(ctx, query, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.GetTransactionReceipts(ctx, actionHash, []string{actionHash}, nil); err != nil {
		t.Fatal(err)
	}
	scope := &actionMock{answer: func(r ActionRequest) (any, error) {
		if r.Method == "eth_getLogs" {
			return []map[string]any{}, nil
		}
		return actionReceipt(), nil
	}}
	view := owner.WithRequester(scope)
	before := len(ownerRequests.calls)
	if _, err := drainPages(view.GetLogsWithPagination(ctx, query, nil)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actionRanges(scope.calls), [][2]uint64{{0, 19}, {20, 39}, {40, 44}}) {
		t.Fatal("scope lost learned limit", scope.calls)
	}
	scope.calls = nil
	if _, err := view.GetTransactionReceipts(ctx, actionHash, []string{actionHash}, nil); err != nil {
		t.Fatal(err)
	}
	if len(scope.calls) != 1 || scope.calls[0].Method != "eth_getTransactionReceipt" || len(ownerRequests.calls) != before {
		t.Fatal("scoped action bypassed requester or lost receipt fallback", scope.calls)
	}
	// A replacement logsMetadata learned by the view must be visible to the owner.
	smaller := true
	scope.answer = func(r ActionRequest) (any, error) {
		if smaller {
			smaller = false
			return nil, &Error{Message: "Max range: 5"}
		}
		return []map[string]any{}, nil
	}
	if _, err := drainPages(view.GetLogsWithPagination(ctx, query, nil)); err != nil {
		t.Fatal(err)
	}
	ownerRequests.calls = nil
	if _, err := drainPages(owner.GetLogsWithPagination(ctx, map[string]any{"fromBlock": "0x0", "toBlock": "0x9"}, nil)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actionRanges(ownerRequests.calls), [][2]uint64{{0, 4}, {5, 9}}) {
		t.Fatal("metadata replacement not shared", ownerRequests.calls)
	}
	next := &actionMock{answer: func(r ActionRequest) (any, error) {
		if r.Method == "eth_getLogs" {
			return []map[string]any{}, nil
		}
		return actionReceipt(), nil
	}}
	if _, err := drainPages(owner.WithRequester(next).GetLogsWithPagination(ctx, query, nil)); err != nil {
		t.Fatal(err)
	}
	if actionRanges(next.calls)[0] != [2]uint64{0, 4} {
		t.Fatal("new scope forgot learning")
	}
	other := &actionMock{answer: func(r ActionRequest) (any, error) {
		if r.Method == "eth_getBlockReceipts" {
			return []map[string]any{actionReceipt()}, nil
		}
		return []map[string]any{}, nil
	}}
	independent := NewActions(other)
	if _, err := drainPages(independent.GetLogsWithPagination(ctx, query, nil)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actionRanges(other.calls), [][2]uint64{{0, 44}}) {
		t.Fatal("different owner shared learned range")
	}
	other.calls = nil
	if _, err := independent.GetTransactionReceipts(ctx, actionHash, []string{actionHash}, nil); err != nil {
		t.Fatal(err)
	}
	if len(other.calls) != 1 || other.calls[0].Method != "eth_getBlockReceipts" {
		t.Fatal("different owner shared fallback")
	}
}

type concurrentViewRequester struct {
	mu    sync.Mutex
	calls int
}

func (r *concurrentViewRequester) Call(_ context.Context, method string, _ any, out any) error {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if method == "eth_getBlockReceipts" {
		return errors.New("no capability")
	}
	var v any = []map[string]any{}
	if method == "eth_getTransactionReceipt" {
		v = actionReceipt()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func TestActionsRequesterViewsConcurrentMetadata(t *testing.T) {
	client := New()
	owner := client.Actions()
	if client.Actions() != owner {
		t.Fatal("owner Actions identity changed")
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &concurrentViewRequester{}
			view := owner.WithRequester(r)
			for j := 0; j < 40; j++ {
				if _, err := drainPages(view.GetLogsWithPagination(context.Background(), map[string]any{"fromBlock": "0x0", "toBlock": "0x10"}, nil)); err != nil {
					t.Error(err)
				}
				if _, err := view.GetTransactionReceipts(context.Background(), actionHash, []string{actionHash}, nil); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if client.Actions() != owner || owner.WithRequester(nil) != nil || (*Actions)(nil).WithRequester(&concurrentViewRequester{}) != nil {
		t.Fatal("owner identity/nil contract changed")
	}
}

type actionsViewAsync struct {
	admitted  int
	syncCalls int
	done      chan error
}

func (r *actionsViewAsync) Call(context.Context, string, any, any) error {
	r.syncCalls++
	return fmt.Errorf("sync requester must not be used")
}
func (r *actionsViewAsync) BeginCall(context.Context, string, any, any) <-chan error {
	r.admitted++
	return r.done
}
func TestActionsRequesterViewsPreserveAsyncAdmission(t *testing.T) {
	base := &concurrentViewRequester{}
	owner := NewActions(base)
	scope := &actionsViewAsync{done: make(chan error, 1)}
	continuation, async := owner.WithRequester(scope).BeginSourceBlockByNumber(context.Background(), "0x1", false)
	if !async || continuation == nil || scope.admitted != 1 || scope.syncCalls != 0 || base.calls != 0 {
		t.Fatal("actual scoped Begin admission not preserved")
	}
	marker := errors.New("future settled")
	scope.done <- marker
	if _, err := continuation(); !errors.Is(err, marker) {
		t.Fatal("future result not retained", err)
	}
	if continuation, async := owner.WithRequester(base).BeginSourceBlockByNumber(context.Background(), "0x1", false); async || continuation != nil || base.calls != 0 {
		t.Fatal("sync view manufactured async admission")
	}
}
