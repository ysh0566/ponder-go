# Historical durations in the Go writer

Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` defines two Prometheus gauges with a single `step` label. The clock returns elapsed milliseconds and each completed step increments its gauge. Ponder's indexing reset preserves these accumulated durations. The Go registry uses the same metric names, labels and unit. Separate processes have separate registries. [Pinned metrics](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts) · [Pinned timer](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/utils/timer.ts)

The two-chain HTTP test launches a Go application with default, Isolated and Omnichain ordering on PostgreSQL and PGlite. Fresh Anvil chains use identical timestamps and a 12-second reorg window. The test checks the chain-specific historical cutoff, including the Omnichain tie at the global checkpoint, scrapes the bound HTTP listener, mines more blocks on both chains and compares the historical samples. The dual-store finalized Omnichain test reads timings from a real cached scan and checks that reopening an already completed deployment runs no new business transform steps. [HTTP test](../../../runtime/historical_metrics_test.go) · [Dual-store test](../../../historical/runtime_commit_observer_test.go) · [Registry test](../../../metrics/historical_test.go)

The continuous dual-store fixture processes eight unfinalized blocks, reopens the deployment and replays all eight. The registry is shared across those two runs to catch any historical sample from live indexing, cache maintenance or replay. [Live replay test](../../../historical/live_runtime_continuous_test.go)

## Commands

```sh
selection='^(TestApplicationHistoricalTimings|TestRuntimeOmnichainCommitObserver|TestHistoricalDurations|TestContinuousFinality)$'
go test -p 1 -v -count=1 -timeout=20m -run "$selection" ./metrics ./runtime ./historical
go test -p 1 -race -v -count=1 -timeout=20m -run "$selection" ./metrics ./runtime ./historical
go test -p 1 -v -count=1 -timeout=20m -run '^TestRuntimeContinuousFutureEnd$' ./historical
go test -p 1 -race -v -count=1 -timeout=20m -run '^TestRuntimeContinuousFutureEnd$' ./historical
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

On Go 1.25.11 the main selection passed 11 leaf cases in ordinary mode and 11 with the race detector. The continuous dual-store fixture added two passes per mode. Both runs used PostgreSQL 14.24 and PGlite with zero skips. All 50 packages compiled and passed vet. The complete metrics package separately passed all five leaf cases in both modes. Verification tooling passed 499 Node cases, four loopback cases and 43 Python cases. Full module ordinary and race results for this revision are pending.

From the pinned `packages/core` directory, both original files passed all 17 tests.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/internal/metrics.test.ts src/runtime/historical.test.ts
```
