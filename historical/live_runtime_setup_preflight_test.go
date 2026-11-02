package historical

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	"testing"
	"time"
)

func TestRuntimeSetupPreflight(t *testing.T) {
	for _, mode := range []string{"name_slice", "foreign_chain", "invalid_block", "setup_only", "legacy_bounded"} {
		t.Run(mode, func(t *testing.T) {
			p := newFiniteRPC(t, 3)
			b := finiteBuild(t, p, 3, true, indexing.Handler(finiteIncrement), indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil }))
			x := b.Executable()
			o := RuntimeOptions{Historical: Options{Build: b}, BuildID: "setup-preflight", OpenID: "invalid", Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
			calls := 0
			o.Requesters = func(context.Context, uint64) ([]rpc.Requester, error) {
				calls++
				return nil, fmt.Errorf("preflight reached RPC")
			}
			switch mode {
			case "name_slice":
				x.Chains[0]["name"] = []string{"one"}
			case "foreign_chain":
				copy := indexing.Object{}
				for k, v := range x.SetupCallbacks[0][0]["chain"].(map[string]any) {
					copy[k] = v
				}
				copy["id"] = uint64(99)
				x.SetupCallbacks[0][0]["chain"] = copy
			case "invalid_block":
				x.SetupCallbacks[0][0]["block"] = -1
			case "setup_only":
				x.EventCallbacks[0] = nil
			case "legacy_bounded":
				o.Live = nil
			}
			var err error
			var pan any
			func() { defer func() { pan = recover() }(); _, err = OpenRuntimePostgres(context.Background(), o) }()
			if pan != nil {
				t.Fatalf("invalid setup metadata panicked instead of preflight error: %v", pan)
			}
			if err == nil || calls != 0 {
				t.Fatalf("preflight err=%v RPC calls=%d", err, calls)
			}
		})
	}
}
