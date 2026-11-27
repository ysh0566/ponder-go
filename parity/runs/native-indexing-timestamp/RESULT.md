# Committed indexing time

The Go writer exposes the Unix timestamp of committed indexing progress through `ponder_indexing_timestamp{chain}`. Default multichain and Isolated retain independent positions. Omnichain projects a single committed prefix onto every participating chain. Physical block heights and per-chain sync timestamps remain available. [Usage](../../../docs/USAGE.md#查看每条链的同步位置) · [Gauge](../../../metrics/indexing_time.go)

The pinned 0.18.2 runtime uses per-chain timestamps for default multichain and Isolated, and a shared checkpoint for live Omnichain. During historical callbacks the upstream Omnichain code can report the range end before its transaction commits. Go publishes after the group commits, then advances to the verified scan end after successful activation of a bounded Omnichain job. A stored start-of-block checkpoint alone produces no sample. [Pinned registry](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L224-L230) · [Historical callback](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/runtime/omnichain.ts#L411-L418) · [Live callback](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/runtime/omnichain.ts#L746-L752)

The HTTP integration test scrapes two chains whose tips have the same timestamp. The Omnichain writer completes the first tip while the other chain remains at the preceding height. Both indexing labels publish the first tip’s timestamp. Another test verifies both labels immediately after a global fork rollback, after peer replay and on reopening the database without running a callback. Staged single-chain tests cover an initial handler failure and restoring an event checkpoint from a committed block. Dual-store tests cover live commits, failure before commit, Isolated peer outages and sparse finalized ranges with different scan ends. [HTTP scrape](../../../runtime/historical_metrics_test.go) · [Ordered reorg and restart](../../../runtime/ordered_indexer_test.go) · [Staged checkpoints](../../../runtime/staged_boundaries_test.go) · [Independent peer](../../../historical/isolated_live_outage_test.go) · [Sparse ranges](../../../historical/omnichain_log_ranges_test.go)

## Verification

Run with the repository’s PostgreSQL, PGlite and Foundry test prerequisites. Backend subtest filters allow separate runs.

```sh
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 ./metrics
  go test $mode -p 1 -count=1 -run '^(TestOrderedIndexer|TestStagedHandlerFailureAndReconnect|TestApplicationHistoricalTimings)$' ./runtime
  go test $mode -p 1 -count=1 -run '^(TestFiniteHistoricalRunnerFiniteBoundary|TestRuntimeIsolatedLiveOutage|TestRuntimeContinuousFutureEnd|TestRuntimeFailedHeadLatency|TestRuntimeOmnichainLogRanges)$' ./historical
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

From the pinned checkout’s `packages/core` directory run `node_modules/.bin/tsx --tsconfig tsconfig.json <go-checkout>/parity/runs/native-indexing-timestamp/oracle.cjs`. The probe exercises the upstream registry and its application progress calculation in all three orderings, including zero, a rewind and reset. Vitest also passed all three original cases in `src/internal/metrics.test.ts`. [Probe](oracle.cjs)

All 36 selected Go cases passed in ordinary mode and all 36 passed under the race detector. Shared-worker memory pressure killed PGlite subprocesses during initial runs. The affected cases passed when run serially with fresh directories and reduced V8 compilation concurrency. All 51 packages compiled and passed vet. The repository verification tools passed 503 Node cases and 43 Python cases. The full-module ordinary and race jobs are tracked separately.

The writer now registers 28 of 39 named upstream metric families. The 11 remaining names and their measurements are listed in the [support matrix](../../../docs/RUNTIME_SUPPORT.md#prometheus-coverage).
