# Per-chain RPC caching for independent finalized Omnichain indexers

When every chain has a covering closing block callback, the source coordinator selects either persisted historical SQL pages or the in-memory RPC generator separately for each chain. Business transactions, finalized lineage and logical checkpoints use the existing independent storage format. If a callback accesses a previously omitted event field, the runner reacquires the disabled chain's block against its authenticated header. Uniform log-only scans still require the persisted evidence of their complete interval and empty tail.

The tests run static ABI logs and closing block callbacks with the first chain disabled, the second disabled, and both disabled. They seed disabled chains with stale block and handler RPC entries, fail after a shared business row has been updated, inspect the durable prefix, and compare the replayed suffix and final rows with the pinned global event order. They also check both chains' final logical cursors and a completed cold open. An attempt to reopen a mixed deployment with both chains cached is rejected before the completed open with the original policy. A separate scenario requests a previously omitted block field with one cached and one uncached chain. It also checks the nine decoded events in a refetched block and rejects a different block hash.

## Verification

Go tests used Go 1.25.11, PostgreSQL 14.24 and PGlite 0.2.13, with separate test databases and temporary business and sync directories. For each backend and mode the full selection contains three cache combinations and a callback field-refetch case. The first-disabled and refetch cases were repeated with the additional assertions on deployment identity and the actual gasUsed value.

```sh
for backend in postgres pglite; do
  for mode in ordinary race; do
    flags=()
    if [[ $mode == race ]]; then flags+=(-race); fi
    go test "${flags[@]}" -v -count=1 -timeout=45m -run "^TestOmnichainClosing(CachePolicy|UncachedRefetch)$/^${backend}$" ./historical
    go test "${flags[@]}" -v -count=1 -timeout=45m -run "^TestOmnichainClosing(CachePolicy|UncachedRefetch)$/^${backend}$/^(first-disabled)?$" ./historical
  done
done
go test -v -count=1 -timeout=45m -run '^TestRuntimeOmnichainStaticLogsSource$/^postgres$/^small$' ./historical
go test -v -count=1 -run '^(TestRuntimeOmnichainPreflightBoundary|TestRuntimeOmnichainStaticLogsCapabilities)$' ./historical
go test -run '^$' ./...
go vet ./...
```

The following reference checks ran in `packages/core` at Ponder 0.18.2 commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

```sh
vitest run --maxWorkers=1 --hookTimeout=45000 src/indexing/client.test.ts -t 'without rpc request cache'
vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/historical.test.ts -t 'getLocalInMemoryEventGenerator|getHistoricalEvents\(\) omnichain'
```

All eight selected upstream tests passed (three client cases and five historical generator/coordinator cases). The remaining 20 declarations in those two files were outside the selection. The repository verification tools passed 503 Node tests and 40 Python tests. The Go usage-guide declaration compiled.

The full focused selection passed eight backend/scenario combinations in ordinary mode and the same eight under the race detector. The strengthened first-disabled and refetch cases passed on both backends in both modes. The all-cached PostgreSQL control and capability/preflight checks passed. The module compiled and passed vet. Full-module ordinary and race results for this revision are pending in the shared regression queue. These native scenarios do not change the upstream test inventory.
