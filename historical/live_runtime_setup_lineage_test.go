package historical

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"testing"
	"time"
)

func TestRuntimeSetupBaselineLineage(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 3)
			calls := 0
			b := finiteBuild(t, s.p, 3, true, indexing.Handler(finiteIncrement), setupCrashHandler(&calls))
			for _, c := range b.Executable().Chains {
				c["reorgWindow"] = uint64(1)
			}
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "setup-baseline-lineage-v8", OpenID: "setup-lineage", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
			r := runtimeOpen(t, backend, o)
			// Open captured finalized H2. Return a competing lower H0 while H1/H2
			// keep their original parent identities. The setup RPC block remains H1;
			// it is the permanently retained baseline that must authenticate first.
			s.mu.Lock()
			original := s.canonical[1][0]
			bad := map[string]any{}
			for k, v := range original {
				bad[k] = v
			}
			bad["hash"] = fmt.Sprintf("0x%064x", 999999)
			s.canonical[1][0] = bad
			s.mu.Unlock()
			out, err := r.Run(context.Background())
			if err == nil || out.Ready || out.Completed {
				t.Fatalf("competing baseline admitted %+v %v", out, err)
			}
			c, e := r.Control(context.Background())
			if e != nil || c.Batches != 0 || c.Bootstraps != 0 || calls != 0 {
				t.Fatalf("setup committed before complete finalized lineage: calls=%d control=%+v err=%v", calls, c, e)
			}
			if e = r.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
				if len(rows) != 0 {
					t.Fatalf("unauthenticated setup effects persisted %v", rows)
				}
			})
		})
	}
}
