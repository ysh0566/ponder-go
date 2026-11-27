# Historical block counts for Go writers

Baseline Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. This change adds three per-chain Prometheus gauges to the native writer. The metric-name census now covers 31 of 39 upstream families. It adds no original declaration mappings. [Go usage](../../../docs/USAGE.md#查看历史回填的区块数)

The upstream gauges use inclusive block ranges. The persistent generator calculates initial cached coverage from the union of required heights across filters. Its completion increment uses the entire adaptive window, which can overlap an already covered gap. The Go persistent generator counts only the missing heights inside a successful window. For two sources that request heights 1 and 3, the Go counts on a fresh run are 3 total, 1 already covered and 2 acquired. On a fully cached replay the counts are 3, 3 and 0. The memory generator advances over empty tails and counts the part before a recovery cursor.

Atomic workers publish the historical prefix confirmed by the first successful poll. They use the durable gaps seen during that poll and its retries. A height acquired before a transient failure still counts as new work in that run. The Omnichain watermark can limit the eligible prefix. Managed default and Isolated workers publish the verified durable prefix at startup and count successful historical groups. An authenticated chain with zero historical work publishes three zeroes. A finite deployment whose stored end is already reached reports the verified range as reusable. Live blocks leave these measurements stable.

## Verification

The focused Go commands use separate, test-owned PostgreSQL and PGlite databases. Both backends cover all three atomic orderings, cached and uncached finite scans, mixed historical/live operation, a live-only range and a failed callback followed by a warm restart. Assertions read the public Prometheus output.

```bash
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 ./metrics
  go test $mode -p 1 -count=1 -run '^(TestAtomicHistoricalRetryCoverage|TestApplicationHistoricalTimings)$' ./runtime
  go test $mode -p 1 -count=1 -run '^(TestFiniteHistoricalRunnerFiniteBoundary|TestFiniteHistoricalRunnerCacheDisabled|TestRuntimeBoundedLifecycle|TestRuntimeContinuousFutureEnd|TestRuntimeOmnichainLogRanges)$' ./historical
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

From the pinned checkout’s `packages/core` directory, `node_modules/.bin/tsx --tsconfig tsconfig.json <go-checkout>/parity/runs/native-historical-block-counts/oracle.cjs` verified gauge types, emitted labels, combined progress, the empty range and reset. Vitest passed all three original internal metrics cases and the 11 selected local historical generator cases using PostgreSQL and Anvil 1.8.5. The historical name filter excluded the other three cases in that file. [Upstream registry probe](oracle.cjs)

The selection passed 34 unique Go leaf cases in ordinary mode and the same 34 with the race detector. A PGlite child exited under shared-worker memory pressure during the live-only reopen case. Its isolated rerun and both subsequent sparse-range race cases passed. All 51 Go packages compiled and passed vet. The repository tool checks passed 503 Node tests and 43 Python tests. These counts cover this milestone’s selected verification. The whole-module ordinary and race regression is tracked separately.
