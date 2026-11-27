package historical

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/store"
)

func assertSyncMetrics(t *testing.T, registry *metrics.Registry, chain string, head store.ManagedLiveHead, realtime, complete int) {
	t.Helper()
	cp, err := checkpoint.Decode(head.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"ponder_sync_block": float64(cp.BlockNumber), "ponder_sync_block_timestamp": float64(cp.BlockTimestamp),
		"ponder_sync_is_realtime": float64(realtime), "ponder_sync_is_complete": float64(complete),
	}
	all, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range all {
		value, ok := want[family.GetName()]
		if !ok {
			continue
		}
		for _, sample := range family.Metric {
			if len(sample.Label) == 1 && sample.Label[0].GetName() == "chain" && sample.Label[0].GetValue() == chain {
				if sample.Gauge == nil || sample.GetGauge().GetValue() != value {
					t.Fatalf("chain %s %s = %v, want %v", chain, family.GetName(), sample.GetGauge().GetValue(), value)
				}
				delete(want, family.GetName())
			}
		}
	}
	for name, value := range want {
		if value >= 0 {
			t.Fatalf("chain %s missing %s = %v", chain, name, value)
		}
	}
}

func assertBackfillTimes(t *testing.T, registry *metrics.Registry, chain string, started, ended bool) {
	t.Helper()
	body, err := registry.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for phase, want := range map[string]bool{"start": started, "end": ended} {
		sample := fmt.Sprintf("ponder_historical_%s_timestamp_seconds{chain=%q} ", phase, chain)
		if strings.Contains(body, sample) != want {
			t.Fatalf("expected %s present=%t in metrics:\n%s", sample, want, body)
		}
	}
}
