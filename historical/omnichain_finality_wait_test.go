package historical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeOmnichainFinalityWait(t *testing.T) {
	g := loadOmniLogsGolden(t)
	tc := omniLogsNamedCase(t, g, "small")
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			var polling atomic.Bool
			var polls [3]atomic.Int32
			p, _ := omniLogsRPCWithSelectors(t, g, nil, func(id uint64, selector string) string {
				if selector == "latest" {
					if !polling.Load() {
						return "0x1" // Compile while the range is still in the future.
					}
					switch polls[id].Add(1) {
					case 1:
						return "0x1" // Neither end exists yet.
					default:
						return "0x3" // Both ends exist, but chain 2 has the earlier timestamp.
					}
				}
				return selector
			})
			var calls []string
			var snapshot omniLogsSnapshot
			h := omniLogsHandlers(func() []omniLogsEvent { return snapshot.Calls }, &calls, nil)
			f := newManagedFiniteFixture(t, backend, omniLogsBuild(t, g, p, h))
			omnichainGoldenStorage(&f)
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "finality-wait", Requesters: runtimeFactory(p, &epochs), OmnichainFinalityPollInterval: time.Millisecond}
			o.Historical.QuerySize = tc.QuerySize
			// Compilation also reads the node. Advance the simulated tips only during Open.
			polling.Store(true)
			for i, want := range tc.Snapshots {
				snapshot, calls, epochs = want, nil, nil
				o.OpenID = fmt.Sprintf("wait-%d", i)
				r := runtimeOpen(t, backend, o)
				if !slices.Equal(epochs, []uint64{0}) || (i == 0 && (polls[1].Load() != 2 || polls[2].Load() != 2)) {
					t.Fatalf("unexpected acquisition: epochs %v, head reads %d and %d", epochs, polls[1].Load(), polls[2].Load())
				}
				out, err := r.Run(t.Context())
				if err != nil || !out.Completed || !out.Ready || out.Recovered != (i > 0) || len(calls) != len(want.Calls) {
					t.Fatalf("run %+v, err %v, callbacks %d, expected %d", out, err, len(calls), len(want.Calls))
				}
				control, err := r.Control(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if err := r.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				assertOmniLogsSnapshot(t, f, control, want)
			}
		})
	}
}

func TestRuntimeOmnichainFinalityWaitAdmission(t *testing.T) {
	for _, name := range []string{"cancel", "setup-wait", "wrong-end-height", "wrong-finality-height", "rpc-error", "negative-interval", "other-ordering"} {
		t.Run(name, func(t *testing.T) {
			var polling atomic.Bool
			var reads atomic.Int32
			g := loadOmniLogsGolden(t)
			p, _ := omniLogsRPCWithSelectors(t, g, nil, func(id uint64, selector string) string {
				if !polling.Load() {
					return selector
				}
				if selector == "latest" {
					reads.Add(1)
					if name == "cancel" || (name == "setup-wait" && reads.Load() <= 2 && id == 1) {
						return "0x2"
					}
				}
				if selector == "0x3" {
					switch name {
					case "wrong-end-height", "wrong-finality-height":
						return "0x2"
					case "rpc-error":
						return "0xff"
					}
				}
				return selector
			})
			noop := indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return errors.New("unexpected callback") })
			h := map[string]indexing.Handler{"block": noop, "A": noop, "B": noop, "setup": noop}
			b := omniLogsBuild(t, g, p, h)
			var epochs []uint64
			o := RuntimeOptions{Historical: Options{Build: b}, BuildID: "wait-admission", OpenID: "one", Requesters: runtimeFactory(p, &epochs), OmnichainFinalityPollInterval: time.Hour}
			expect := ""
			switch name {
			case "wrong-end-height":
				expect = "invalid omnichain end header"
			case "wrong-finality-height":
				for _, c := range b.Executable().Chains {
					c["reorgWindow"] = uint64(1)
				}
				expect = "invalid finality header"
			case "negative-interval":
				o.OmnichainFinalityPollInterval = -time.Second
				expect = "OmnichainFinalityPollInterval"
			case "other-ordering":
				b.Executable().Ordering = "multichain"
				expect = "OmnichainFinalityPollInterval"
			case "setup-wait":
				o.Historical.Build = omniLogsBuildWithSetup(t, g, p, h, 1, 1, true, map[string]any{"two": map[string]any{"startBlock": 3}})
				o.OmnichainFinalityPollInterval = time.Millisecond
			}
			// An hour-long poll must wake on cancellation. The same deadline
			// makes a hard error mistakenly classified as pending observable.
			polling.Store(true)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			storageReached := errors.New("reached storage")
			opened := false
			_, err := openRuntime(ctx, o, func(context.Context, store.IndependentStorageOptions, store.ManagedLiveRuntimeOptions) (*store.ManagedLiveDomains, error) {
				opened = true
				return nil, storageReached
			})
			if opened != (name == "setup-wait") || err == nil {
				t.Fatalf("opened %t, err %v", opened, err)
			}
			switch name {
			case "cancel":
				if !errors.Is(err, context.DeadlineExceeded) || reads.Load() != 2 {
					t.Fatalf("cancelled wait: %v, %d head reads", err, reads.Load())
				}
			case "setup-wait":
				if !errors.Is(err, storageReached) || reads.Load() != 4 {
					t.Fatalf("setup end was not observed on two snapshots: %v, %d reads", err, reads.Load())
				}
			case "rpc-error":
				if errors.Is(err, context.DeadlineExceeded) || reads.Load() != 2 || !strings.Contains(err.Error(), "400") {
					t.Fatalf("unexpected RPC result %v, reads %d", err, reads.Load())
				}
			default:
				if !strings.Contains(err.Error(), expect) {
					t.Fatalf("expected %q, got %v", expect, err)
				}
			}
			if (name == "negative-interval" || name == "other-ordering") && len(epochs) != 0 {
				t.Fatalf("unexpected RPC epochs %v", epochs)
			}
			// Every failed opening must release the compiled build session.
			_, release, err := o.Historical.Build.Executable().AcquireSession()
			if err != nil {
				t.Fatal(err)
			}
			release()
		})
	}
}
