package historical

import (
	"context"
	"errors"
	"fmt"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
	"testing"
	"time"
)

func TestRuntimeSetupCallbackClose(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			p := newFiniteRPC(t, 3)
			var r *RuntimeRunner
			calls := 0
			setup := indexing.Handler(func(ctx context.Context, _ *indexing.HandlerArgs) error {
				calls++
				if err := r.Close(ctx); !errors.Is(err, store.ErrManagedLiveBusy) {
					return fmt.Errorf("setup callback Close bypassed busy gate: %v", err)
				}
				if err := r.Close(context.WithoutCancel(ctx)); !errors.Is(err, store.ErrManagedLiveBusy) {
					return fmt.Errorf("setup retained context Close bypassed gate: %v", err)
				}
				return nil
			})
			b := finiteBuild(t, p, 3, true, indexing.Handler(finiteIncrement), setup)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			r = runtimeOpen(t, backend, RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "setup-callback-close", OpenID: "callback-close", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}})
			out, err := r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || calls != 2 {
				t.Fatalf("setup callback close=%+v calls=%d err=%v", out, calls, err)
			}
		})
	}
}
