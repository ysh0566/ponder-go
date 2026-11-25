package historical

import (
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/store"
	"slices"
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
			want := name != "mixed-preserved"
			if (err == nil) != want {
				t.Fatalf("physical budget %s err=%v", name, err)
			}
			if name == "mixed-preserved" && (err == nil || !strings.Contains(err.Error(), "mixed")) {
				t.Fatalf("mixed rejection changed %v", err)
			}
		})
	}
}

func TestRuntimeOmnichainSparseGroupHeights(t *testing.T) {
	const end = 9007199254740991
	events := map[uint64][]indexing.Object{12: {{}}, end - 3: {{}, {}}}
	for _, initial := range []bool{true, false} {
		want := []uint64{12, end - 3, end}
		if initial {
			want = append([]uint64{1}, want...)
		}
		got := slices.Collect(omnichainGroupHeights(1, end, initial, true, events))
		if !slices.Equal(got, want) {
			t.Fatalf("physical segments %v want %v", got, want)
		}
	}
}
