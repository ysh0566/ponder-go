package historical

import (
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ysh0566/ponder-go/metrics"
)

func assertHistoricalGroups(t *testing.T, registry *metrics.Registry, present bool) {
	t.Helper()
	body, err := registry.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"extract", "transform"} {
		series := fmt.Sprintf(`ponder_historical_concurrency_group_duration{group=%q} `, group)
		if strings.Contains(body, series) != present {
			t.Fatalf("expected %s present=%t in %s", series, present, body)
		}
	}
}

func TestHistoricalGroupTiming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := metrics.New()
		ctx := metrics.WithHistorical(t.Context(), registry)
		check := func(extract, transform float64) {
			t.Helper()
			body, err := registry.Collect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for group, want := range map[string]float64{"extract": extract, "transform": transform} {
				line := fmt.Sprintf("ponder_historical_concurrency_group_duration{group=%q} %g\n", group, want)
				if !strings.Contains(body, line) {
					t.Fatalf("missing %q in %s", line, body)
				}
			}
		}
		assertHistoricalGroups(t, registry, false)
		source := func(yield func(int, error) bool) {
			for i := 1; i <= 3; i++ {
				time.Sleep(1250 * time.Microsecond)
				if !yield(i, nil) {
					return
				}
			}
		}
		for n := range timedHistoricalGroups(ctx, iter.Seq2[int, error](source)) {
			time.Sleep(3500 * time.Microsecond)
			if n == 3 {
				break
			}
		}
		check(2.5, 7)
		registry.ResetIndexing()
		check(2.5, 7)
		failed := func(yield func(int, error) bool) {
			time.Sleep(time.Second)
			yield(0, errors.New("acquisition failed"))
		}
		for _, err := range timedHistoricalGroups(ctx, iter.Seq2[int, error](failed)) {
			if err == nil {
				t.Fatal("missing acquisition error")
			}
		}
		for range timedHistoricalGroups(metrics.WithHistorical(ctx, nil), iter.Seq2[int, error](source)) {
		}
		check(2.5, 7)
	})
}
