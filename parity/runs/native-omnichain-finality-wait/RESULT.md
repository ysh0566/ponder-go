# Opening a bounded dual-store Omnichain job before finality

A Go job may set `RuntimeOptions.OmnichainFinalityPollInterval` to a positive duration on the PostgreSQL or PGlite constructor. Each poll refreshes all participating chains. It applies each chain’s reorg window and uses the earliest finalized checkpoint across chains. Every configured end block and any later setup height must fit. The context passed to the constructor bounds the wait, and storage opens after the check succeeds. A zero interval keeps the single preflight check. [Go usage](../../../docs/USAGE.md#等待双库-omnichain-的区间最终确认)

Compilation can occur while both configured ends are still in the future. The two-chain opening fixture advances through three snapshots. At height one the configured ends are still in the future. At height three both ends exist, but one chain’s block-three timestamp exceeds its peer’s finalized checkpoint. At height four the entire range is eligible. On PostgreSQL and PGlite, 38 ordered callbacks agree with the pinned Ponder fixture. The test compares business rows, undo state and chain positions, then reopens the same storage and observes zero further callbacks. Admission tests cover a setup read beyond the event range, an expired context, RPC failure, wrong-height headers and invalid option combinations. [Test source](../../../historical/omnichain_finality_wait_test.go)

Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` supplies the historical ordering reference. All 14 original tests in this file passed on PostgreSQL 14.24.

```sh
cd packages/core
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/historical.test.ts
```

## Go checks

The following selection passed 31 leaf cases with zero skips in both modes using Go 1.25.11, PostgreSQL 14.24 and PGlite. All 50 Go packages compiled and passed vet. Repository checks passed 503 Node.js and 43 Python tests.

```sh
selection='^(TestRuntimeOmnichainFinalityWait|TestRuntimeOmnichainFinalityWaitAdmission|TestRuntimeOmnichainPreflightBoundary|TestRuntimeOmnichainSetupFinality|TestRuntimeOmnichainSetupLineage|TestRuntimeIsolatedPreflight|TestRuntimeRejectsHistoricalOptions)$'
go test -p 1 -count=1 -timeout=35m -v -run "$selection" ./historical
go test -p 1 -count=1 -timeout=35m -v -race -run "$selection" ./historical
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The fixture initially included configuration-time RPC reads in its poll counts. The test now counts opening-time reads separately. An additional run with compilation at height one passed on both backends in ordinary mode. During a repeat under the race detector, a PGlite process was killed under host memory pressure. The full focused race selection above passed with early compilation on both engines. After moving the retained fixture to disk, a fresh `go test -race -p 1 -v -count=1 -timeout=7m -run '^TestRuntimeOmnichainFinalityWait$/^pglite$' ./historical` also passed.

The module-wide ordinary and race runs have a separate queue. An earlier revision’s full run reported PostgreSQL disk exhaustion on a 384 MiB temporary data volume, local server startup failures and a stale compiler-fixture fingerprint. Those reports cannot establish module-wide success for this change.
