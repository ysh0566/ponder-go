package historical

import (
	"context"
	"testing"

	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/runtime"
)

func TestManagedApplicationIndexer(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			server := newFiniteRPC(t, 3)
			calls := 0
			build := runtimeBuild(t, server, 3, 1, func(ctx context.Context, args *indexing.HandlerArgs) error {
				calls++
				return finiteIncrement(ctx, args)
			})
			fixture := newManagedFiniteFixture(t, backend, build)
			var epochs []uint64
			runner := runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(fixture), BuildID: "managed-app-test", OpenID: "test-open", Requesters: runtimeFactory(server, &epochs)})
			var application runtime.ApplicationIndexer = runner
			var states []bool
			if err := application.RunIndexing(t.Context(), func(ready bool) { states = append(states, ready) }); err != nil {
				t.Fatal(err)
			}
			if calls != 6 || len(states) < 2 || states[0] || !states[len(states)-1] {
				t.Fatalf("callbacks=%d readiness=%v", calls, states)
			}
		})
	}
}
