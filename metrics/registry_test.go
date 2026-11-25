package metrics

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

type sample struct {
	labels map[string]string
	value  float64
}

func samples(t *testing.T, r *Registry, name string) []sample {
	t.Helper()
	all, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	results := []sample{}
	for _, family := range all {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string)
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			value := metric.GetCounter().GetValue()
			if metric.Gauge != nil {
				value = metric.Gauge.GetValue()
			}
			results = append(results, sample{labels, value})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].labels["type"] < results[j].labels["type"] })
	return results
}

func expectSamples(t *testing.T, r *Registry, name string, want ...sample) {
	t.Helper()
	got := samples(t, r, name)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %+v, want %+v", name, got, want)
	}
}

func TestSourceMetrics(t *testing.T) {
	event := map[string]string{"event": "Token:Transfer"}
	hit := map[string]string{"table": "account", "type": "hit"}
	prefetch := map[string]string{"table": "account", "type": "prefetch"}
	insert := map[string]string{"table": "account", "method": "insert"}
	const completed = "ponder_indexing_completed_events"
	const cache = "ponder_indexing_cache_requests_total"
	const queries = "ponder_indexing_store_queries_total"
	t.Run("batches indexing metrics until collection", func(t *testing.T) {
		r := New()
		r.IncrementCompleted("Token:Transfer", 1)
		r.IncrementCompleted("Token:Transfer", 2)
		r.IncrementCacheRequests("account", "hit", 1)
		r.IncrementCacheRequests("account", "hit", 2)
		r.IncrementCacheRequests("account", "prefetch", 0)
		r.IncrementStoreQueries("account", "insert", 1)
		r.IncrementStoreQueries("account", "insert", 2)
		expectSamples(t, r, completed, sample{event, 3})
		expectSamples(t, r, cache, sample{hit, 3}, sample{prefetch, 0})
		expectSamples(t, r, queries, sample{insert, 3})
		if _, err := r.Collect(t.Context()); err != nil {
			t.Fatal(err)
		}
		expectSamples(t, r, completed, sample{event, 3})
	})
	t.Run("flushes pending indexing metrics before reset", func(t *testing.T) {
		r := New()
		r.IncrementCompleted("Token:Transfer", 2)
		r.IncrementCacheRequests("account", "hit", 3)
		r.IncrementStoreQueries("account", "insert", 4)
		r.ResetIndexing()
		expectSamples(t, r, completed)
		expectSamples(t, r, cache, sample{hit, 3})
		expectSamples(t, r, queries, sample{insert, 4})
	})
	t.Run("does not replay flushed completed event metrics", func(t *testing.T) {
		r := New()
		r.IncrementCompleted("Token:Transfer", 2)
		r.FlushIndexing()
		r.SetCompleted("Token:Transfer", 1)
		expectSamples(t, r, completed, sample{event, 1})
	})
}

func TestConcurrentCollect(t *testing.T) {
	r := New()
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 50 {
				r.IncrementCompleted("a", 1)
				if _, err := r.Collect(t.Context()); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	group.Wait()
	out, err := r.Collect(t.Context())
	if err != nil || !strings.Contains(out, "ponder_indexing_completed_events{event=\"a\"} 400\n") {
		t.Fatal(out, err)
	}
}
