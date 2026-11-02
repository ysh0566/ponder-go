package historical

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
)

func TestRuntimeLiveHeadersBounded(t *testing.T) {
	h := newRuntimeLiveHeaders(2)
	raw := finiteBlock(1, 1)
	number := big.NewInt(1)
	raw["number"] = number
	h.put(raw)
	number.SetInt64(99)
	copy := h.get(raw["hash"].(string))
	if copy["number"].(*big.Int).Int64() != 1 {
		t.Fatal("mutable quantity entered immutable header cache")
	}
	copy["number"].(*big.Int).SetInt64(200)
	if h.get(raw["hash"].(string))["number"].(*big.Int).Int64() != 1 {
		t.Fatal("header cache exposed mutable quantity")
	}
	s := newContinuousRPC(t, 6)
	for n := uint64(0); n <= 6; n++ {
		h.put(finiteBlock(1, n))
	}
	if len(h.values) != 2 || len(h.order) != 2 {
		t.Fatalf("header cache grew with full history: %d/%d", len(h.values), len(h.order))
	}
	final, err := runtimeHead(1, finiteBlock(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := runtimeHead(1, finiteBlock(1, 6))
	if err != nil {
		t.Fatal(err)
	}
	r := &RuntimeRunner{finalized: map[uint64]store.ManagedLiveHead{1: final}}
	requester := rpc.New(s.p.server.URL + "/1")
	requester.Retries = 0
	requester.RequestsPerSecond = 0
	floor, retained, err := r.liveBranch(context.Background(), requester, 1, store.ManagedLiveControl{Cursors: map[uint64]store.ManagedLiveHead{1: cursor}}, h)
	if err != nil || len(retained) != 5 {
		t.Fatalf("evicted branch fallback failed: retained=%d err=%v", len(retained), err)
	}
	actual, err := runtimeHead(1, floor)
	if err != nil || actual != final {
		t.Fatalf("fallback changed authenticated floor: %+v %v", actual, err)
	}
	previous := final
	for _, raw := range retained {
		head, err := runtimeHead(1, raw)
		if err != nil || head.ParentHash != previous.Hash {
			t.Fatalf("fallback skipped full predecessor identity: %+v %v", head, err)
		}
		previous = head
	}
	if previous != cursor || len(h.values) > 2 || len(h.order) > 2 {
		t.Fatalf("fallback changed cursor or cache bound: %+v %d/%d", previous, len(h.values), len(h.order))
	}
}

func TestRuntimeLiveHeaderCapacityPreflight(t *testing.T) {
	for _, capacity := range []int{-1, 4097} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			p := newFiniteRPC(t, 1)
			b := runtimeBuild(t, p, 1, 1, indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil }))
			called := false
			_, err := OpenRuntimePostgres(context.Background(), RuntimeOptions{Historical: Options{Build: b}, BuildID: "header-budget", OpenID: "header-budget", Live: &RuntimeLiveOptions{HeaderCacheCapacity: capacity}, Requesters: func(context.Context, uint64) ([]rpc.Requester, error) { called = true; return nil, nil }})
			if err == nil || !strings.Contains(err.Error(), "header cache capacity") || called {
				t.Fatalf("bad header budget reached acquisition/storage: cap=%d called=%v err=%v", capacity, called, err)
			}
		})
	}
}
