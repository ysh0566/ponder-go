# Wall-clock timestamps for historical indexing

Go writers expose `ponder_historical_start_timestamp_seconds` and `ponder_historical_end_timestamp_seconds` as per-chain gauges. Values use the host clock rounded to the nearest Unix second. Ordered writers share a timestamp across their group. Isolated chains finish independently. A successful empty backfill can start and finish in the same second.

On the atomic indexer the first successful catch-up poll ends the measurement. On the dual-store writer the committed historical cuts or the verified Omnichain scan determine completion. A cold Isolated peer that rejoins directly in realtime has no historical window on that run. Retrying startup preserves an existing start. Live polling and reorg replay preserve the completed window. Resetting the indexing registry clears both gauges. [Pinned metric definitions](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L180-L193) · [Go usage](../../../docs/USAGE.md#查看历史回填的起止时间)

## Verification

The registry test uses Go's virtual test clock to cross the half-second boundary. The HTTP integration uses two Anvil chains under default, Omnichain and Isolated ordering with both storage backends. Separate dual-store tests cover ordered catch-up, one Isolated chain finishing before its peer fails, an interrupted Omnichain run, a cold restart with a fully processed range, and a peer that is still offline at reopen. [Registry](../../../metrics/historical_test.go) · [Writer HTTP tests](../../../runtime/historical_metrics_test.go) · [Isolated](../../../historical/isolated_finalized_test.go) · [Omnichain](../../../historical/runtime_commit_observer_test.go)

With Go 1.25.11 and the repository's task-owned PostgreSQL, PGlite and Anvil fixtures, run the following commands for each mode.

```sh
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 ./metrics
  go test $mode -p 1 -count=1 -run '^TestApplicationHistoricalTimings$' ./runtime
  go test $mode -p 1 -count=1 -run '^TestRuntime(IsolatedFinalized|OmnichainCommitObserver|ContinuousFutureEnd)$' ./historical
  go test $mode -p 1 -count=1 -run '^TestRuntimeIsolatedOfflineReopen$' ./historical
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The pinned registry uses the `chain` label, minimum and maximum aggregation for start and end respectively, and removes both samples on indexing reset. From the pinned upstream `packages/core` directory, run `node_modules/.bin/tsx <go-checkout>/parity/runs/native-backfill-wall-clock/oracle.cjs`. The three original registry tests also ran with Vitest. Its default global setup could not claim port 8545, so the registry-only run used the core source alias and omitted the network fixture. [Pinned registry probe](oracle.cjs) · [Original tests](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.test.ts)

All 29 selected Go cases passed in ordinary mode and all 29 passed under the race detector. They comprise 13 registry cases, six native writer HTTP cases and ten dual-store cases. All 51 packages compiled and `go vet` passed. The repository tooling passed 503 Node tests and 43 Python tests. Comparing literal registered names with the pinned metrics source finds 26 matching families out of 39. Full-module ordinary and race regressions are scheduled separately.
