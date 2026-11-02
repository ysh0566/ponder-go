package historical

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
	"time"
)

func TestRuntimeCachePolicySnapshot(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, mode := range []string{"bounded_v5", "continuous_v6"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				continuousRoot(t)
				p := newFiniteRPC(t, 3)
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					if _, err := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40)); err != nil {
						return err
					}
					return finiteIncrement(ctx, a)
				})
				b := runtimeBuild(t, p, 3, 1, h)
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "cache-policy-frozen", OpenID: "cache-policy-first", Requesters: runtimeFactory(p, &epochs)}
				if mode == "continuous_v6" {
					o.Live = &RuntimeLiveOptions{PollingInterval: time.Millisecond}
				}
				r := runtimeOpen(t, backend, o)
				for _, chain := range b.Executable().Chains {
					chain["cacheRpcRequests"] = false
				}
				for _, chain := range r.build.Chains {
					if !runtimeRPCCacheEnabled(chain) {
						t.Fatal("caller changed opened runtime cache policy without a new identity")
					}
				}
				out, err := r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready {
					t.Fatalf("frozen cached runtime=%+v %v", out, err)
				}
				c, err := r.Control(context.Background())
				want := "managed-live-runtime-v5"
				if mode == "continuous_v6" {
					want = "managed-live-continuous-v6"
				}
				if err != nil || c.Version != want {
					t.Fatalf("legacy format silently changed: %+v %v", c, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				syncf := f
				syncf.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
				managedInspect(t, syncf, func(db store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.SyncNamespace + "_rpc_1")
					for _, table := range []string{"blocks", "rpc_request_results"} {
						rows := managedRows(t, db, "SELECT count(*)::text AS n FROM "+ns+"."+table)
						if len(rows) != 1 || fmt.Sprint(rows[0]["n"]) == "0" {
							t.Fatalf("opened cached strategy stopped persisting %s: %v", table, rows)
						}
					}
				})
			})
		}
	}
}
