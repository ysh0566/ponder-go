package historical

import (
	"context"
	"errors"
	"testing"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeOmnichainStaticLogsCapabilities(t *testing.T) {
	g := loadOmniLogsGolden(t)
	type scenario struct {
		name    string
		query   int
		change  func(*config.ExecutableSourceIndexingBuild)
		factory bool
		valid   bool
	}
	blockChange := func(key string, value any) func(*config.ExecutableSourceIndexingBuild) {
		return func(b *config.ExecutableSourceIndexingBuild) {
			for _, cb := range b.EventCallbacks[0] {
				f := cb["filter"].(map[string]any)
				if f["type"] == "block" {
					f[key] = value
				}
			}
		}
	}
	logChange := func(key string, value any) func(*config.ExecutableSourceIndexingBuild) {
		return func(b *config.ExecutableSourceIndexingBuild) {
			for _, cb := range b.EventCallbacks[0] {
				f := cb["filter"].(map[string]any)
				if f["type"] == "log" {
					f[key] = value
					return
				}
			}
		}
	}
	cases := []scenario{
		{name: "default", valid: true},
		{name: "query_1", query: 1, valid: true},
		{name: "query_6", query: 6, valid: true},
		{name: "query_12000", query: 12000, valid: true},
		{name: "negative_query", query: -1},
		{name: "oversized_query", query: 12001},
		{name: "multichain_query", query: 6, change: func(b *config.ExecutableSourceIndexingBuild) { b.Ordering = "multichain" }},
		{name: "factory_option", factory: true},
		{name: "block_interval", valid: true, change: blockChange("interval", uint64(2))},
		{name: "block_offset", change: blockChange("offset", uint64(1))},
		{name: "short_closing_range", valid: true, change: blockChange("toBlock", uint64(2))},
		{name: "late_closing_start", valid: true, change: blockChange("fromBlock", uint64(2))},
		{name: "log_before_closing_start", valid: true, change: logChange("fromBlock", uint64(0))},
		{name: "log_after_closing_end", valid: true, change: logChange("toBlock", uint64(4))},
		{name: "trace_without_call_type", change: logChange("type", "trace")},
		{name: "transaction_without_account", change: logChange("type", "transaction")},
		{name: "transfer_without_account", change: logChange("type", "transfer")},
		{name: "log_only", valid: true, change: func(b *config.ExecutableSourceIndexingBuild) {
			kept := []indexing.Object{}
			for _, cb := range b.EventCallbacks[0] {
				if cb["type"] != "block" {
					kept = append(kept, cb)
				}
			}
			b.EventCallbacks[0] = kept
		}},
		{name: "duplicate_block_source", change: func(b *config.ExecutableSourceIndexingBuild) {
			for _, cb := range b.EventCallbacks[0] {
				if cb["type"] == "block" {
					b.EventCallbacks[0] = append(b.EventCallbacks[0], cb)
					return
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := omniLogsRPC(t, g)
			noop := indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil })
			b := omniLogsBuild(t, g, p, map[string]indexing.Handler{"block": noop, "A": noop, "B": noop})
			if tc.change != nil {
				tc.change(b.Executable())
			}
			epochs := []uint64{}
			factoryCalls := 0
			requesters := runtimeFactory(p, &epochs)
			o := RuntimeOptions{Historical: Options{Build: b, QuerySize: tc.query}, BuildID: "omni-logs-capability-v1", OpenID: "capability", FactorySources: tc.factory}
			o.Requesters = func(ctx context.Context, n uint64) ([]rpc.Requester, error) {
				factoryCalls++
				return requesters(ctx, n)
			}
			opened := false
			sentinel := errors.New("capability reached owned DDL boundary")
			_, err := openRuntime(context.Background(), o, func(context.Context, store.IndependentStorageOptions, store.ManagedLiveRuntimeOptions) (*store.ManagedLiveDomains, error) {
				opened = true
				return nil, sentinel
			})
			if tc.valid {
				if !opened || factoryCalls != 1 || !errors.Is(err, sentinel) {
					t.Fatalf("valid capability rejected before owned opener opened%v requests%d err%v", opened, factoryCalls, err)
				}
			} else {
				if opened || factoryCalls != 0 || err == nil {
					t.Fatalf("unsupported capability reached RPC or DDL opened%v requests%d err%v", opened, factoryCalls, err)
				}
			}
		})
	}
}
