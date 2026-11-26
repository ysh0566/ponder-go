# Factory-discovered logs in a finalized Omnichain range

A Go indexer using `historical.OpenRuntimePostgres` or `historical.OpenRuntimePGlite` can register only contract log callbacks, including callbacks on factory-discovered addresses. Every participating chain has finite source ranges below the acquired global finalized watermark. Set both `SourceCheckpoints` and `FactorySources` and leave `Live` unset.

The store validates interval coverage for the child's configured log range and for each parent factory fragment from the start of discovery. A parent can finish registering addresses before the child stops emitting. Cached chains read the registered children and coverage in the same sync-store transaction. An uncached chain reads creation logs from its node, checks their block hashes against finalized ancestry and uses the earliest birth height to match subsequent logs. On a cold restart its ancestry check can extend below the resume cursor to cover those births.

## Native verification

The integration case starts two local Anvil chains with different child addresses and staggered block timestamps. One chain caches RPC data and the other reads directly from its node. The parent discovery ends at block 2 and the child filter ends at block 5. An unrelated emitter sends the same event, the first chain's future child emits before discovery, and the fixture has both an interior gap and an empty tail. The test checks eligible transactions and their cross-chain order. After an injected handler error, a cold open completes the business row from the committed prefix. A second cold open leaves the row and callback count intact. Only the cached chain has a persisted factory birth. Both PostgreSQL 14.24 and PGlite 0.2.13 are exercised with Anvil 1.8.5 and Go 1.25.11.

Storage tests reject a missing parent interval, a creation log from another block hash, a discovery RPC failure and a decodable child in a supposedly empty interval. A cached log from an unrelated emitter can still belong to an overlapping static callback.

```sh
feature='^Test(RuntimeOmnichainLogFactory|RuntimeOmnichainFactoryPreflight|LogFactoryScanEvidence|CachedLogFactoryEmitter|UncachedLogScanEvidence|LogOnlyRangeCoverage|LogOnlyRangeScanPages|LogOnlyScanDecodedCoverage|LogOnlyScanReceiptBinding)$'
go test -p 2 -v -count=1 -timeout=45m -run "$feature" ./store ./historical
controls='^Test(RuntimeOmnichainFactory|RuntimeOmnichainFactoryPreflight|RuntimeOmnichainStaticLogsCapabilities|OmnichainUncachedLogs|OmnichainUncachedArchiveRetry|RuntimeOmnichainLogRanges)$'
go test -v -count=1 -timeout=45m -run "$controls" ./historical
race='^Test(RuntimeOmnichainLogFactory|RuntimeOmnichainFactory|RuntimeOmnichainFactoryPreflight|RuntimeOmnichainStaticLogsCapabilities|OmnichainUncachedLogs|OmnichainUncachedArchiveRetry|RuntimeOmnichainLogRanges|LogFactoryScanEvidence|CachedLogFactoryEmitter|UncachedLogScanEvidence|LogOnlyRangeCoverage|LogOnlyRangeScanPages|LogOnlyScanDecodedCoverage|LogOnlyScanReceiptBinding)$'
go test -race -p 2 -v -count=1 -timeout=45m -run "$race" ./store ./historical
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

The ordinary feature and control selections and the combined race selection passed on both backends. The closing-block and log-only factory integration cases also passed together after adjusting the shared fixture. The full module compiled and passed vet. An initial race run exhausted the workspace disk. The successful rerun used a dedicated PostgreSQL data volume in memory and a separate temporary directory for PGlite.

## Pinned upstream checks

The factory builder and runtime event suites passed 17 tests. The original end-to-end factory test passed on both PostgreSQL and PGlite. The first PGlite invocation exceeded its 15-second deadline. The rerun passed with the same source, assertions and timeout. PostgreSQL used a dedicated local instance and PGlite used the upstream in-memory configuration.

```sh
# packages/core, Ponder 0.18.2 at 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334
vitest run --maxWorkers=1 --hookTimeout=45000 src/build/factory.test.ts src/runtime/events.test.ts
vitest run --maxWorkers=1 --hookTimeout=45000 src/_test/e2e/factory/factory.test.ts
```

The verification tools passed 503 Node tests, 40 Python discovery tests and three observer tests. The new native tests provide additional coverage of the Go runtime. The pinned inventory remains at 883 ported Go cases with 866 passing and 17 provider RPC failures. The full-module ordinary and race regressions for this revision are pending.
