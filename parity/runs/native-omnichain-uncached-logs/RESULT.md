# Independent Omnichain static logs with per-chain RPC caching

A bounded, globally finalized deployment can turn off caching on any participating chain. The runner checks the actual pages returned by `eth_getLogs` for each uncached static filter over its own inclusive block range. The storage layer records the scanned interval only after the last required page succeeds. It checks empty gaps and the unprocessed tail against the acquired block headers and all matching event ABIs. The last committed source checkpoint can precede the scanned height.

## Verification

Tests with one-block RPC pages exercise two uncached chains, one cached and one uncached chain, a trailing range with ABI-skipped logs, a run with no decodable events, and cold recovery after a callback writes then fails. They compare the calls and persisted business rows to the source fixture, inspect the cache tables on both chains and reopen the completed deployment. Store tests cover a bad block hash, off-page, removed or wrong-address logs, an outage on the last page, and two matching ABI layouts where only the second decodes.

A second test mines two local Anvil 1.8.5 chains. The same event signature is emitted by the indexed contract and a decoy on each chain. After all eight expected transactions commit, the test injects an `eth_getLogs` error on one chain during the completion scan. It checks that readiness stays false, that the business store retains all eight indexed transaction hashes, and that two subsequent opens complete without running the handlers again.

```sh
selection='^Test(OmnichainUncachedArchiveRetry|OmnichainUncachedLogs|UncachedLogScanEvidence|RuntimeOmnichainPreflightBoundary|LogOnlyScanDecodedCoverage|LogOnlyEmptyTailPagination|SourceHistoricalMemoryBound|SourceHistoricalFactoryDrain|SourceHistoricalMemoryLive)$'
go test -v -count=1 -run "$selection" ./store ./runtime ./historical
go test -race -v -count=1 -run "$selection" ./store ./runtime ./historical
controls='^Test(OmnichainUncachedArchiveRetry|RuntimeOmnichainLogRanges)$/^(postgres|pglite)$/^(cached-outside-range)$'
go test -v -count=1 -run "$controls" ./historical
go test -race -v -count=1 -run "$controls" ./historical
```

The upstream selections use the pinned Ponder 0.18.2 source.

```sh
vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/historical.test.ts src/sync-historical/in-memory.test.ts -t 'getLocalInMemoryEventGenerator\(\)|getHistoricalEvents\(\) omnichain|syncBlockData\(\) with log filter|syncBlockData\(\) dedupes logs matched by many log filters'
```

Eight selected upstream tests passed and 29 were filtered out.

The focused Go tests passed in ordinary and race modes with PostgreSQL 14.24 and PGlite 0.2.13. The cached range control also passed on both engines. All packages compiled and `go vet ./...` passed. Repository tooling passed 503 Node checks, 40 Python discovery tests and three observer tests. Full-module ordinary and race regression results for this revision are pending. The upstream Go inventory remains at 883 ports, with 866 passed and 17 provider-specific failures.
