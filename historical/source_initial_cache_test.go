package historical

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"sync"
	"testing"
	"time"
)

// Supplemental compatibility evidence, not source-test mapping credit. The
// pinned transport oracle uses a durable-map adapter; this test supplies real
// independent sync/business SQL domains and a failing setup transaction.
func TestRuntimeSourceFailedSetupForkCache(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			raw, e := os.ReadFile("testdata/source-cache-17c6d5ba.json")
			if e != nil {
				t.Fatal(e)
			}
			var golden struct {
				Compatibility struct {
					Retry string `json:"retry"`
				} `json:"compatibility"`
			}
			if e = json.Unmarshal(raw, &golden); e != nil {
				t.Fatal(e)
			}
			want, ok := new(big.Int).SetString(strings.TrimPrefix(golden.Compatibility.Retry, "0x"), 16)
			if !ok {
				t.Fatal("invalid actual transport oracle")
			}
			s := newContinuousRPC(t, 1)
			fail := true
			values := []string{}
			var mu sync.Mutex
			network := 0
			s.gate = func(_ context.Context, _ uint64, method string, _ []any) error {
				if method == "eth_getBalance" {
					mu.Lock()
					network++
					mu.Unlock()
				}
				return nil
			}
			setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				balance, e := a.Context.Client.GetBalance(ctx, "0x1111111111111111111111111111111111111111")
				if e != nil {
					return e
				}
				values = append(values, balance.String())
				_, e = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("setup-%d", finiteChain(a)), "balance": balance}, store.Conflict{})
				if e != nil {
					return e
				}
				if fail && finiteChain(a) == 2 {
					return fmt.Errorf("controlled failed setup after independent numeric RPC cache writes")
				}
				return nil
			})
			b := sourceMatrixBuild(t, s.p, 1, 2, 1, "both", false, indexing.Handler(finiteIncrement), setup)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "source-cache-compatibility", OpenID: "cache-before-failure", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
			r := runtimeOpen(t, backend, o)
			out, e := r.Run(context.Background())
			if e == nil || out.Ready || out.Completed {
				t.Fatalf("failed setup accepted %+v %v", out, e)
			}
			c, e := r.Control(context.Background())
			if e != nil || c.Batches != 0 {
				t.Fatalf("failed business leaked checkpoint %+v %v", c, e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
				if len(rows) != 0 {
					t.Fatalf("failed setup business rows persisted %v", rows)
				}
			})
			mu.Lock()
			firstNetwork := network
			mu.Unlock()
			if firstNetwork != 2 {
				t.Fatalf("first native transport requests=%d", firstNetwork)
			}
			// Same numeric request now refers to a forked block; fresh finality advances
			// from0 to1. Compatibility preserves cached block1, matching the real source
			// transport oracle. This is intentionally not claimed fork-safe hardening.
			s.fork(1, 1)
			s.setLatest(2)
			fail = false
			o.OpenID = "cache-after-fork-fresh-finality"
			r = runtimeOpen(t, backend, o)
			out, e = r.Run(context.Background())
			if e != nil || !out.Completed || !out.Ready {
				t.Fatalf("cache compatibility restart %+v %v", out, e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			if len(values) != 4 || values[2] != want.String() {
				t.Fatalf("source/native compatibility differs values=%v oracle=%s", values, want)
			}
			mu.Lock()
			allNetwork := network
			mu.Unlock()
			if allNetwork != firstNetwork {
				t.Fatalf("fresh client bypassed durable numeric cache: %d -> %d", firstNetwork, allNetwork)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='setup-1'")
				if len(rows) != 1 || fmt.Sprint(rows[0]["balance"]) != want.String() {
					t.Fatalf("actual committed setup value differs %v", rows)
				}
			})
		})
	}
}
