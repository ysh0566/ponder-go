package metrics

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestHistoricalDurations(t *testing.T) {
	r := New()
	ctx := WithHistorical(t.Context(), r)
	live := WithHistorical(ctx, nil)
	start := time.Now()
	finish := Historical(ctx).StartExtract("decode")
	time.Sleep(3 * time.Millisecond)
	finish()
	before := time.Since(start)
	Historical(live).StartTransform("index")()
	read := func() float64 {
		t.Helper()
		list, err := r.Gather()
		if err != nil {
			t.Fatal(err)
		}
		var found float64
		for _, f := range list {
			switch f.GetName() {
			case "ponder_historical_extract_duration":
				for _, v := range f.GetMetric() {
					if len(v.Label) != 1 || v.Label[0].GetName() != "step" || v.Label[0].GetValue() != "decode" || v.Gauge == nil {
						t.Fatal(v)
					}
					found = v.Gauge.GetValue()
				}
			case "ponder_historical_transform_duration":
				t.Fatalf("unexpected live sample %v", f)
			}
		}
		return found
	}
	v := read()
	if v < 3 || v > float64(before.Milliseconds()+10) {
		t.Fatalf("sample is %v ms after %v", v, before)
	}
	r.ResetIndexing()
	if got := read(); got != v {
		t.Fatalf("reset changed duration from %v to %v", v, got)
	}
	finish = Historical(ctx).StartExtract("decode")
	time.Sleep(time.Millisecond)
	finish()
	if got := read(); got < v+1 {
		t.Fatalf("second observation did not accumulate: %v + %v", v, got-v)
	}
}

func TestBackfillWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const start = "ponder_historical_start_timestamp_seconds"
		const end = "ponder_historical_end_timestamp_seconds"
		r := New()
		names := []string{"a", "b"}
		w := r.NewBackfillWindow(names...)
		names[0] = "other"
		a, b := map[string]string{"chain": "a"}, map[string]string{"chain": "b"}
		initial := float64(time.Now().Unix())
		w.Finish()
		expectSamples(t, r, start)
		expectSamples(t, r, end)
		time.Sleep(499 * time.Millisecond)
		w.Start()
		time.Sleep(time.Millisecond)
		var callers sync.WaitGroup
		for range 4 {
			callers.Go(w.Start)
		}
		callers.Wait()
		expectSamples(t, r, start, sample{a, initial}, sample{b, initial})
		expectSamples(t, r, end)
		w.Finish()
		time.Sleep(2 * time.Second)
		for range 4 {
			callers.Go(w.Finish)
		}
		callers.Wait()
		expectSamples(t, r, end, sample{a, initial + 1}, sample{b, initial + 1})
		// A fresh run of one Isolated chain clears just its previous end.
		next := r.NewBackfillWindow("b")
		next.Start()
		expectSamples(t, r, start, sample{a, initial}, sample{b, initial + 3})
		expectSamples(t, r, end, sample{a, initial + 1})
		r.ResetIndexing()
		expectSamples(t, r, start)
		expectSamples(t, r, end)
		Historical(t.Context()).NewBackfillWindow("disabled").Start()
	})
}

func TestHistoricalBlockCounts(t *testing.T) {
	r := New()
	chain := map[string]string{"chain": "one"}
	r.PlanHistoricalBlocks("one", 4, 1, false)
	r.AddHistoricalBlocks("one", 2)
	r.PlanHistoricalBlocks("one", 6, 1, true)
	r.AddHistoricalBlocks("one", 3)
	expectSamples(t, r, "ponder_historical_total_blocks", sample{chain, 6})
	expectSamples(t, r, "ponder_historical_cached_blocks", sample{chain, 1})
	expectSamples(t, r, "ponder_historical_completed_blocks", sample{chain, 5})
	r.SetHistoricalBlocks("one", 4, 4, 0)
	expectSamples(t, r, "ponder_historical_cached_blocks", sample{chain, 4})
	expectSamples(t, r, "ponder_historical_completed_blocks", sample{chain, 0})
	r.ResetIndexing()
	for _, name := range []string{"total", "cached", "completed"} {
		expectSamples(t, r, "ponder_historical_"+name+"_blocks")
	}
}
