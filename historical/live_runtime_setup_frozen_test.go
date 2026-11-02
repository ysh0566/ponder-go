package historical

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/indexing"
	"testing"
	"time"
)

func TestRuntimeSetupFrozenInput(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			p := newFiniteRPC(t, 2)
			calls := 0
			evil := false
			setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				calls++
				c, ok := a.Context.Contracts["Setup"].(map[string]any)
				if !ok || fmt.Sprint(c["startBlock"]) != "1" {
					return fmt.Errorf("caller changed frozen setup contract %v", a.Context.Contracts)
				}
				_, err := a.Context.Client.GetBalance(ctx, "0x1111111111111111111111111111111111111111")
				return err
			})
			b := finiteBuild(t, p, 2, true, indexing.Handler(finiteIncrement), setup)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			r := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "setup-frozen", OpenID: "setup-frozen", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}})
			x := b.Executable()
			for _, g := range x.SetupCallbacks {
				for _, cb := range g {
					cb["fn"] = indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { evil = true; return fmt.Errorf("mutated callback") })
					cb["block"] = uint64(9)
					cb["chain"] = indexing.Object{"id": uint64(99), "name": "foreign"}
				}
			}
			x.SetupCallbacks[0] = nil
			for _, contracts := range x.Contracts {
				contracts["Setup"].(map[string]any)["startBlock"] = uint64(9)
			}
			out, err := r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || calls != 2 || evil {
				t.Fatalf("setup immutable snapshot=%+v calls=%d evil=%v err=%v", out, calls, evil, err)
			}
			balanceCalls := 0
			for _, q := range p.snapshot() {
				if q.Method == "eth_getBalance" {
					balanceCalls++
					if q.Params[1] != "0x1" {
						t.Fatalf("caller changed frozen setup RPC block %v", q)
					}
				}
			}
			if balanceCalls != 2 {
				t.Fatalf("setup callback/RPC count=%d", balanceCalls)
			}
		})
	}
}
