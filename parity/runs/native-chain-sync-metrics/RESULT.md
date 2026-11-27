# Chain sync metrics for Go indexers

The writer exports four upstream gauge names with a single `chain` label. `ponder_sync_block` and `ponder_sync_block_timestamp` use the processed canonical header and its Unix timestamp in seconds. A rollback can move both values backwards. A chain entering live indexing publishes `ponder_sync_is_realtime = 1`. When a live chain finishes its configured interval it publishes realtime 0 and `ponder_sync_is_complete = 1`. A backfill-only completion produces the completion series directly. A newly launched query-only process has no samples for these gauges.

## Coverage

- PostgreSQL and PGlite. The single-store writer exposes progress for all three orderings on its HTTP endpoint. The Omnichain case uses two chains whose timestamps constrain which blocks can be committed. Finished jobs restore positions after a cold reopen. A rejected callback does not advance the block gauge.
- Separate business and sync stores. A finite Isolated chain completes while its peer is still running. The continuous tests inspect the height, timestamp and phase at commit, during an RPC outage, on rollback to the common ancestor and at a finite end. The Omnichain history test finishes a sparse log scan, checks the actual end header beyond the last matching callback and reopens from disk.
- The fixed Ponder 0.18.2 reference at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` exercises the original isolated realtime generator, reorg handler and registry with headers from a disposable Anvil chain. The subscription delivers the mined block in process. Resetting indexing clears height and phases and retains the last timestamp. The Go registry follows that reset behavior.

## Reproduction

Use Go 1.25.11, Anvil 1.8.5, a private PostgreSQL 14.24 database and the repository PGlite 0.2.13 bridge. Configure the existing test fixtures. Run the database suites in sequence and give each invocation its own disposable managed-fixture root.

```sh
unset PONDER_UPSTREAM_ROOT
rt='^(TestApplicationHistoricalTimings|TestCompiledMultichainIndexer|TestApplicationServeOnly|TestAtomicHandlerFailureHasNoHeadSample)$'
hist='^(TestRuntimeIsolatedFinalized|TestRuntimeIsolatedLiveOutage|TestRuntimeContinuousFutureEnd|TestRuntimeBoundedReorg|TestRuntimeOmnichainLogRanges)$'
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 ./metrics
  go test $flags -p 1 -count=1 -timeout=25m -run "$rt" ./runtime
  go test $flags -p 1 -count=1 -timeout=25m -run "$hist" ./historical
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

Start an empty local Anvil node with mining disabled. From the pinned upstream checkout's `packages/core`, run these two checks. Replace the script path with its absolute location in the Go checkout. The probe mines one block.

```sh
ANVIL_URL=http://127.0.0.1:8545 node_modules/.bin/tsx --tsconfig tsconfig.json /path/to/parity/runs/native-chain-sync-metrics/oracle.cjs
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/internal/metrics.test.ts src/runtime/historical.test.ts
```

The final ordinary and race runs each passed the same 44 distinct Go leaf cases (11 registry, 15 single-store runtime and application, 18 dual-store). All 51 module packages compiled and `go vet -p 1 ./...` passed. Repository tooling passed 503 Node and 43 Python checks. One earlier overlapping race run lost a PGlite bridge with a broken pipe and a killed child process. The complete dual-store selection subsequently passed in sequence on fresh fixtures. These are focused results. The full module ordinary and race suites have a separate release queue.

The reference probe and 17 original tests passed. The Go writer now registers 21 of 39 explicitly named upstream metric families. The remaining 18 cover database operations (3), historical sync (9) and indexing (6). The pinned upstream test inventory still records 883 Go mappings executed, 866 passed and 17 provider-specific failures. [Usage](../../../docs/USAGE.md#查看每条链的同步位置) · [Metric inventory](../../../docs/RUNTIME_SUPPORT.md#prometheus-coverage) · [Reference probe](oracle.cjs)
