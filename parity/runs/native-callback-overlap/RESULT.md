# Overlapping Go callbacks

The fixture uses Anvil 1.8.5 and the ordinary Go application entry point. A single transaction emits a log and produces a root CALL. PostgreSQL 14 and PGlite 0.2.13 each run the fixture in a fresh namespace.

- The ABI declares `Marked(uint256 indexed left, uint256 right)` and `Marked(uint256 left, uint256 indexed right)`. A second source subscribes to the same address with receipts disabled. The indexed word 7 and data word 11 produce three callback deliveries with values (7,11), (11,7) and (7,11). A declaration requiring two data words fails to decode independently.
- The same transaction calls selector `0x42966c68`. Both `burn(uint256)` and `collate_propagate_storage(bytes16)` receive it. Their arguments decode to the numeric value of the calldata word and its first 16 bytes respectively. The three log deliveries share an event ID. The two traces share a different event ID.
- The second log handler writes and then fails. Its transaction rolls back. After the store reopens, the first callback stays committed, the second retries and the remaining three callbacks run. Polling the same head again preserves all five rows. Anvil replaces the block and changes the emitted data to 13. The store removes the old rows and commits all five replacements.
- A separate assertion imports the replacement block, transaction, receipt and log into the SQL source cache. A page with limit one contains both compiled log callbacks at the same checkpoint. The prior live deliveries are recognized on the same store. On a fresh business store, dispatching the page twice commits each callback once.
- Running the regression over the parent implementation (44776751) on PostgreSQL reached only the second contract source and the two CALL handlers. Neither canonical overloaded event callback ran. A deployment that used ambiguous events with this shared topic needs a fresh namespace and backfill to reconstruct previously missed deliveries.

## Verification

The pinned upstream reference is 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334. Running its original runtime event tests uses `vitest run --maxWorkers=1 src/runtime/events.test.ts` from `packages/core`. A direct invocation of its `buildEvents` and `decodeEvents` with the fixture values above produces six raw matches and five decoded deliveries. It independently confirms the arguments, receipt projection and shared checkpoint identities.

The primary Go regression is `go test -v -count=1 -run '^TestNativeCallbackOverlap$' ./runtime` with the repository's PostgreSQL, PGlite and Anvil test environment. This additional native case does not increase the number of upstream test cases ported or passed.

The original upstream file passed all 11 tests. The following native selection passed all 83 leaf cases in both ordinary and race modes with separate fresh test databases.

```sh
selection='^(TestNativeCallbackOverlap|TestContractLogAndTraceBatch|TestContractTraceRuntime|TestSourcePageRuntime|TestSourceConfigurationRuntime|TestContractLogProjection|TestDecodePendingPreservesNormalizedCallbackIndex|TestOrderedMixedSources|TestDefaultMixedSources|TestFunctionEntriesPreserveDuplicatesAndCollisions|TestUpstreamEventLog|TestABICatalogGoNameCollisions|TestSourceABIUtilityTypes)$'
go test -v -count=1 -timeout=15m -run "$selection" ./runtime ./abi
go test -race -v -count=1 -timeout=15m -run "$selection" ./runtime ./abi
go test -run '^$' ./...
go vet ./...
```

Module-wide test compilation, vet and all 503 verification-tool checks passed. The whole-module ordinary and race executions for this release follow earlier full runs in the shared regression queue. Their pass status is pending.
