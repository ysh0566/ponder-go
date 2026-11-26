package metrics

import (
	"testing"
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
