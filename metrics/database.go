package metrics

import (
	"context"
	"time"
)

type databaseKey struct{}

// WithDatabase routes database measurements in ctx to the application's registry.
func WithDatabase(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, databaseKey{}, r)
}

func databaseRegistry(ctx context.Context) *Registry {
	r, _ := ctx.Value(databaseKey{}).(*Registry)
	return r
}

// ObserveDatabaseMethod records one labeled attempt on success or failure.
func ObserveDatabaseMethod(ctx context.Context, method string, elapsed time.Duration, err error) {
	r := databaseRegistry(ctx)
	if r == nil || method == "" || (err != nil && ctx.Err() != nil) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dbDuration.WithLabelValues(method).Observe(float64(elapsed) / float64(time.Millisecond))
	if err != nil {
		r.dbErrors.WithLabelValues(method).Inc()
	}
}

// SubmitPostgresQuery counts explicit Query and Exec submissions to the driver.
func SubmitPostgresQuery(ctx context.Context, pool string) {
	if r := databaseRegistry(ctx); r != nil {
		r.postgresQueries.WithLabelValues(pool).Inc()
	}
}

// StartCacheQuery measures a cache operation that reaches the database.
// Call the returned function only after the find, load or flush succeeds.
func (r *Registry) StartCacheQuery(table, method string) func() {
	if r == nil {
		return noTiming
	}
	start := time.Now()
	return func() {
		r.cacheDuration.WithLabelValues(table, method).Observe(float64(time.Since(start)) / float64(time.Millisecond))
	}
}

// StartRawSQL measures a submitted callback statement, including savepoint recovery.
func (r *Registry) StartRawSQL() func() {
	if r == nil {
		return noTiming
	}
	start := time.Now()
	return func() {
		r.rawSQLDuration.Observe(float64(time.Since(start)) / float64(time.Millisecond))
	}
}
