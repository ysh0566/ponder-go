# Historical extraction and transformation groups

The Go writer exposes the gauge `ponder_historical_concurrency_group_duration` with two series. The extract series measures elapsed time obtaining a historical batch. With a prefetching producer this includes the consumer's wait for the next batch. The transform series measures the time the consumer spends processing it, including callback execution and business writes. Each successfully consumed batch adds fractional milliseconds to both series. Parallel workers contribute to the same totals. Live blocks use a separate path. [Usage and queries](../../../docs/USAGE.md#查看历史回填耗时)

The atomic Go indexer times a block's header read and source extraction, then its dispatch through completion. Ordered workers retain the duration of acquiring a prefetched header while processing a peer. The dual-store runner measures the existing producer/consumer boundary for each historical batch. Finite multichain jobs include the final successfully handled batch before stopping at the configured ends. [Atomic writer](../../../runtime/engine.go) · [Ordered scheduler](../../../runtime/ordered_indexer.go) · [Dual-store queue](../../../historical/live_runtime_completion.go) · [Omnichain iterator](../../../historical/live_runtime_omnichain_history.go) · [Finite runner](../../../historical/runner.go)

## Verification

The deterministic Go test consumes three batches and interrupts the iterator during the third. Each acquisition takes 1.25 ms and each consumer operation takes 3.5 ms on the virtual clock. The first two pairs publish exactly 2.5 ms and 7 ms. An acquisition error, a disabled registry and an indexing reset preserve those values. A fresh registry has no samples. The pinned 0.18.2 generator and registry produce the same values and labels. [Go test](../../../historical/historical_group_metrics_test.go) · [Pinned registry and generator probe](oracle.cjs) · [Upstream consumer](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/runtime/multichain.ts#L320-L345)

The native HTTP test exercises all three orderings on PostgreSQL and PGlite. It scrapes the two group labels after initial catch-up and compares all historical samples after new live blocks. The independent-store tests exercise bounded default indexing, Isolated cold restart, Omnichain callback interruption and resume, and a range completed entirely by live polling. The finite-runner test checks final progress and the original RPC request budget with telemetry enabled. [HTTP test](../../../runtime/historical_metrics_test.go) · [Isolated restart](../../../historical/isolated_finalized_test.go) · [Omnichain resume](../../../historical/runtime_commit_observer_test.go) · [Live-only range](../../../historical/live_runtime_continuous_test.go) · [Finite boundary](../../../historical/runner_test.go)

Run the following with the repository's PostgreSQL, PGlite and Foundry test prerequisites. Each backend and ordering may be selected separately with the usual Go subtest filters.

```sh
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 ./metrics
  go test $mode -p 1 -count=1 -run '^TestApplicationHistoricalTimings$' ./runtime
  go test $mode -p 1 -count=1 -run '^Test(HistoricalGroupTiming|FiniteHistoricalRunnerFiniteBoundary|RuntimeBoundedLifecycle|RuntimeIsolatedFinalized|RuntimeOmnichainCommitObserver|RuntimeContinuousFutureEnd)$' ./historical
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

From the pinned upstream `packages/core` directory, run `node_modules/.bin/tsx --tsconfig tsconfig.json <go-checkout>/parity/runs/native-historical-group-duration/oracle.cjs`. The original registry and generator test files were run with Vitest using the core source alias and a single worker. Both suites run independently of the upstream network fixture.

All 30 selected Go test cases passed in ordinary mode and all 30 passed under the race detector. An initial PGlite race run lost its database subprocess during a cgroup out-of-memory kill. The same case passed with its database files on disk in the isolated rerun. The pinned upstream suites passed eight tests, and the repository checks passed 503 Node tests and 43 Python tests. Comparing the explicitly registered names finds 27 matches among 39 upstream metric families. The [two main-branch regression failures](../../../docs/RUNTIME_SUPPORT.md#native-regression-checks) were reproduced separately from the selected group-timing tests.

All 51 Go packages compiled and `go vet -p 1 ./...` passed. Full-module ordinary and race tests are tracked separately.
