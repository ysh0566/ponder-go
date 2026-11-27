# Callback SQL and cache query duration

The Go writer publishes two cumulative millisecond histograms. Callback SQL is timed after scope validation and cache preparation, from the savepoint attempt through query completion or recovery. A database error produces a sample. A failed prior flush does not. Atomic callbacks and managed setup and block transactions bind the writer registry at transaction creation. Explicit cache metrics can select another registry.

Cache timing uses the table and method labels. A successful disk lookup records a find. A populated insert or update buffer records a flush after its write succeeds. A table's successful predictive load records one sample, including when Go splits the SQL to stay within the parameter limit. Cache hits, complete-cache misses and empty flushes do not record database latency. An indexing reset keeps the cumulative distributions, matching the pinned registry.

## Verification

The backend selection uses fresh PostgreSQL namespaces and PGlite stores. It covers a failing SQL statement followed by another successful operation in the same transaction, cache hits and misses, insert and update flushes, a duplicate-key flush that prevents a SQL attempt, explicit registry selection, positive and negative prefetched rows and a managed two-chain writer. The sample counts are checked through the Prometheus exposition.

```sh
go test -count=1 ./metrics
go test -count=1 -run '^(TestIndexingStoreDurations|TestNativeProfilePrefetch)$' ./store
go test -count=1 -run '^TestRuntimeBoundedLifecycle$' ./historical
```

The focused tests also run with the Go race detector. Original comparison files are the pinned Ponder store and cache tests. [Metrics declaration](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L239-L247) · [Go SQL and failure tests](../../../store/sql_test.go) · [Cache load tests](../../../store/prefetch_upstream_test.go)

Ordinary and race runs each passed the same 8 backend cases and 16 metrics cases. All 51 Go packages compiled and `go vet -p 1 ./...` passed. The verification tools passed 503 Node and 43 Python checks. Some preliminary checks were interrupted under shared memory pressure and passed on serial retry. Full-module ordinary and race test execution is tracked separately.

Eight unmodified tests from the pinned Ponder indexing store and cache suites passed on a local PostgreSQL instance. Five supplemental checks inspected actual histogram counts for successful and failed SQL, disk finds, buffered writes, and prefetching. The original complete-cache prefetch fixture had no load sample. The extra disk-load fixture set the callback count to its first sampling point, recorded a lookup using the event's address, and cleared and invalidated the cache before prefetching. The lookup produced one load sample. Supplemental observations do not increase the upstream mapping count.
