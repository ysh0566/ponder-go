package historical

import (
	"context"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
)

func TestRuntimePreflightRejectsUnsupported(t *testing.T) {
	for _, kind := range []string{"experimental_isolated", "omnichain", "setup", "start_zero", "cache_disabled", "future_end"} {
		t.Run(kind, func(t *testing.T) {
			p := newFiniteRPC(t, 3)
			b := runtimeBuild(t, p, 3, 1, indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil }))
			x := b.Executable()
			reason := ""
			switch kind {
			case "experimental_isolated", "omnichain":
				x.Ordering = kind
				reason = "default multichain"
				if kind == "omnichain" {
					reason = "chain finality"
				}
			case "setup":
				x.SetupCallbacks[0] = []indexing.Object{{"name": "bad:setup"}}
				reason = "setup callbacks"
			case "start_zero":
				x.EventCallbacks[0][0]["filter"].(map[string]any)["fromBlock"] = float64(0)
				reason = "startBlock>=1"
			case "cache_disabled":
				x.Chains[0]["cacheRpcRequests"] = false
				reason = "cacheRpcRequests=true"
			case "future_end":
				x.EventCallbacks[0][0]["filter"].(map[string]any)["toBlock"] = float64(4)
				reason = "exceeds acquired latest"
			}
			epochs := []uint64{}
			_, err := OpenRuntimePostgres(context.Background(), RuntimeOptions{Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: store.IndependentStorageOptions{PostgresURL: "this must never reach the database", BusinessNamespace: "invalid_before_ddl", SyncNamespace: "invalid_before_ddl_sync"}}}, BuildID: "preflight-v1", OpenID: "preflight", Requesters: runtimeFactory(p, &epochs)})
			if err == nil || !strings.Contains(err.Error(), reason) {
				t.Fatalf("unsupported %s reached storage or silently defaulted: %v", kind, err)
			}
		})
	}
}
