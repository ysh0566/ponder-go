package indexing

import (
	"context"
	"strings"
	"testing"

	"ponder.local/ponder-go/metrics"
	"ponder.local/ponder-go/store"
)

// One registry is shared by the event service, buffered ORM, and HTTP collector.
func TestServiceHTTPMetrics(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			st := adapterStore(t, dialect)
			registry := metrics.New()
			err := st.Apply(t.Context(), adapterEvent("metrics", 1), func(w *store.Writer) error {
				state := &ErrorState{}
				orm := w.Indexing(store.CacheOptions{Metrics: registry})
				db := NewDatabase(orm, state)
				handler := func(ctx context.Context, args *HandlerArgs) error {
					key := store.Row{"id": "unseen"}
					if _, err := args.Context.DB.Find(ctx, "account", key); err != nil {
						return err
					}
					_, err := args.Context.DB.Find(ctx, "account", key)
					return err
				}
				opts, cb, _ := semanticOptions(t, db, state, handler)
				opts.Metrics = &Metrics{Registry: registry}
				s, err := NewService(opts)
				if err != nil {
					return err
				}
				if err := orm.Cache.Prefetch(t.Context(), nil); err != nil {
					return err
				}
				return s.ProcessHistoricalEvents(t.Context(), []Object{semanticEvent(opts.Build.Chains[0], cb, 2)}, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
			text, err := registry.Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range []string{
				`ponder_indexing_completed_events{event="Token:Transfer"} 1`,
				`ponder_indexing_store_queries_total{method="find",table="account"} 2`,
				`ponder_indexing_cache_requests_total{table="account",type="prefetch"} 0`,
				`ponder_indexing_cache_requests_total{table="account",type="miss"} 1`,
				`ponder_indexing_cache_requests_total{table="account",type="hit"} 1`,
			} {
				if !strings.Contains(text, line+"\n") {
					t.Fatalf("missing %s in %s", line, text)
				}
			}
		})
	}
}
