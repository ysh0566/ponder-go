package metrics

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestDatabaseMeasurements(t *testing.T) {
	r := New()
	ctx := WithDatabase(t.Context(), r)
	failed := errors.New("read failed")
	ObserveDatabaseMethod(ctx, "read", 6500*time.Microsecond, nil)
	ObserveDatabaseMethod(ctx, "read", 10*time.Millisecond, failed)
	ObserveDatabaseMethod(ctx, "", time.Second, failed)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	ObserveDatabaseMethod(cancelled, "aborted", time.Second, cancelled.Err())
	SubmitPostgresQuery(ctx, "default")
	SubmitPostgresQuery(t.Context(), "dedicated")

	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	const duration = "ponder_database_method_duration"
	const failures = "ponder_database_method_error_total"
	const queries = "ponder_postgres_query_total"
	foundDuration := false
	for _, f := range families {
		if f.GetName() != duration {
			continue
		}
		foundDuration = true
		if len(f.Metric) != 1 {
			t.Fatalf("expected one duration series, got %d", len(f.Metric))
		}
		labels := f.Metric[0].Label
		if len(labels) != 1 || labels[0].GetName() != "method" || labels[0].GetValue() != "read" {
			t.Fatalf("unexpected duration labels: %v", labels)
		}
		h := f.Metric[0].GetHistogram()
		var bounds []float64
		for _, b := range h.GetBucket() {
			bounds = append(bounds, b.GetUpperBound())
		}
		if h.GetSampleCount() != 2 || h.GetSampleSum() != 16.5 || !slices.Equal(bounds, []float64{1, 5, 10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000, 500000}) {
			t.Fatal(h)
		}
	}
	if !foundDuration {
		t.Fatal("missing database duration histogram")
	}
	expectSamples(t, r, failures, sample{map[string]string{"method": "read"}, 1})
	expectSamples(t, r, queries, sample{map[string]string{"pool": "default"}, 1})
	r.ResetIndexing()
	for _, name := range []string{duration, failures, queries} {
		expectSamples(t, r, name)
	}
}
