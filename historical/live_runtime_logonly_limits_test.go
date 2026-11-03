package historical

import (
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
)

func TestRuntimeOmnichainLogOnlyPhysicalRange(t *testing.T) {
	limit := uint64(store.ManagedLiveOmnichainBlockLimit(2))
	for _, name := range []string{"boundary", "over", "sum-over", "overlap-counts-once", "huge", "mixed-preserved"} {
		t.Run(name, func(t *testing.T) {
			ends := []uint64{limit / 2, limit - limit/2}
			repeat := 1
			switch name {
			case "over":
				ends[1]++
			case "sum-over":
				ends = []uint64{7000, 7000}
			case "overlap-counts-once":
				repeat = 2
			case "huge":
				ends[0] = 9007199254740991
			}
			groups := [][]indexing.Object{}
			for i, end := range ends {
				callbacks := []indexing.Object{}
				for j := 0; j < repeat; j++ {
					callbacks = append(callbacks, indexing.Object{"type": "contract", "filter": map[string]any{"type": "log", "chainId": i + 1, "fromBlock": 1, "toBlock": end}})
				}
				groups = append(groups, callbacks)
			}
			if name == "mixed-preserved" {
				groups[1] = []indexing.Object{{"type": "block", "filter": map[string]any{"type": "block"}}}
			}
			err := validateOmnichainCallbackModes(groups)
			want := name == "boundary" || name == "overlap-counts-once"
			if (err == nil) != want {
				t.Fatalf("physical budget %s err=%v", name, err)
			}
			if name == "mixed-preserved" && (err == nil || !strings.Contains(err.Error(), "mixed")) {
				t.Fatalf("mixed rejection changed %v", err)
			}
		})
	}
}
