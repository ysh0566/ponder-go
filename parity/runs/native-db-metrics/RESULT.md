# Database metrics for Go indexers

The Go writer and HTTP query service observe database method attempts on PostgreSQL and PGlite. PostgreSQL counts explicit SQL submissions separately by connection pool. See the [usage guide](../../../docs/USAGE.md#查看数据库操作) for the series and PromQL.

## Upstream comparison

The reference is Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Its query builder records milliseconds and failures for each labeled attempt, including a failed attempt followed by a successful retry. The registry declares a service label, and the query builder supplies only the method label. The Go histogram uses the same millisecond buckets and effective label. The pinned source declares and resets the PostgreSQL query counter. Its query paths have no increment call for that counter. The Go adapter increments it when it calls `QueryContext` or `ExecContext` through the backend wrappers. [Upstream registry](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L374-L445) · [Upstream attempt handling](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/database/queryBuilder.ts#L177-L229)

In the pinned core workspace, against a task-owned PostgreSQL instance:

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/database/queryBuilder.test.ts src/internal/metrics.test.ts
node_modules/.bin/tsx /path/to/ponder-go/parity/runs/native-db-metrics/oracle.cjs
```

The nine upstream cases passed. The separate [PGlite probe](oracle.cjs) executed a failing query and a successful retry, checked two duration observations and one error, issued an unlabeled query and verified indexing reset.

## Go verification

Use Go 1.25.11, Anvil 1.8.5, a private PostgreSQL 14.24 database and the PGlite 0.2.13 bridge. Configure the task-owned PostgreSQL URL and runtime fixture directory. Run the following from the Go module root.

```sh
unset PONDER_UPSTREAM_ROOT
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 ./metrics
  go test $flags -p 1 -count=1 -run '^Test(DatabaseQueryAttempts|SourceQueryBuilder|SourceQueryBuilderBoundaries)$' ./store
  go test $flags -p 1 -count=1 -run '^TestSQLValidationHTTP$' ./query
  go test $flags -p 1 -count=1 -run '^TestApplicationHistoricalTimings$' ./runtime
  go test $flags -p 1 -count=1 -run '^TestRuntimeIsolatedLiveOutage$' ./historical
  go test $flags -p 1 -count=1 -run '^Test(SourceSyncBudgetSplitsLogs|SourceEventDataSingleFilterSnapshot|SourceRPCAsyncBackends|SourceIntervalRewind)$' ./store
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

- The registry test checks the effective labels, millisecond buckets, sample sum, failure count, cancellation and reset. Both database backends exercise a real division-by-zero error followed by a successful retry. PostgreSQL additionally checks exact submission counts inside and outside transactions on the ordinary pool and a dedicated connection.
- The public SQL handler records one successful and one failed database operation. Requests rejected by SQL validation leave the method counts unchanged.
- The application's HTTP endpoint exposes writer measurements after indexing two Anvil chains in all three single-store orderings, on both backends. A dual-store Isolated outage, catch-up and fork replay exercises separate business and sync pools.
- A name comparison of the pinned registry and the production Go metrics files gives 24 of 39 shared names. The 15 remaining families measure historical sync (9) and indexing (6). This feature adds native coverage without changing the formal Go mappings. The ledger retains 883 executed cases, 866 passes and 17 provider-specific failures. [Remaining metric families](../../../docs/RUNTIME_SUPPORT.md#prometheus-coverage)

The ordinary and race selections each passed all 54 leaf cases. All 51 Go module packages compiled and `go vet -p 1 ./...` passed. The repository tooling passed 503 Node and 43 Python checks. The module-wide ordinary and race suites have a separate release queue.
