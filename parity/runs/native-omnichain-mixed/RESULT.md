# Finalized Omnichain with different callbacks on each chain

A Go project using independent PostgreSQL or PGlite business and sync stores can combine a chain with a covering block callback and a chain with only contract log callbacks. Each range must end under the acquired global finalized watermark. A chain that registers account or contract CALL callbacks needs one block callback with interval 1 and offset 0 covering its sources.

The runner maintains one cross-chain execution order. A closing-block chain advances block by block to its configured end. A log-only chain authenticates skipped heights and the empty tail of its finite range. Only that chain writes a log scan receipt at activation. The deployment identity binds the callback configuration and uses the current mixed format `managed-live-omnichain-mixed-v1`. On reopen, both the last executed log and the closing chains' committed positions constrain the next callback.

## Verification

`TestRuntimeOmnichainMixedCallbacks` builds Go source declarations and uses two Anvil chains with different factory births. It swaps the log-only role between chain 101 (cached) and 202 (uncached). The other chain handles blocks, factory logs, transactions, transfers and CALL traces. Two event blocks tie on timestamp and must be ordered by chain ID. The test injects a handler error after a business write. On two cold opens it checks the durable shared row, exact callback sequence, completed closing chain and log scan through the empty final block. Only the cached chain has a persisted child address. Both role assignments run on PostgreSQL 14.24 and PGlite 0.2.13 with Anvil 1.8.5.

Store tests cover missing, foreign and conflicting chain roles, event checkpoint types and attempted substitution of a closing chain for a required log scan. Ponder encodes block and log events with the same event-type number. The closing block uses the maximum transaction index, which the log position validator rejects. The selections also exercise paged sparse storage, interval coverage, header lineage and ABI-skipped logs.

```sh
feature='^Test(MixedOmnichainRoles|RuntimeOmnichainMixedCallbacks|RuntimeOmnichainLogOnlyPhysicalRange|RuntimeOmnichainSparseGroupHeights|LogOnly.*|LogFactoryScanEvidence|CachedLogFactoryEmitter|RuntimeOmnichainFactoryPreflight)$'
go test -p 1 -v -count=1 -timeout=45m -run "$feature" ./store ./historical
go test -race -p 1 -v -count=1 -timeout=45m -run "$feature" ./store ./historical
controls='^Test(RuntimeOmnichainFactory|RuntimeOmnichainLogFactory)$'
go test -p 1 -v -count=1 -timeout=45m -run "$controls" ./store ./historical
go test -race -p 1 -v -count=1 -timeout=45m -run "$controls" ./store ./historical
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The feature and control selections passed with and without the race detector on both database backends. The whole module compiled and passed vet.

The pinned Ponder 0.18.2 tests for event pagination checkpoints, Omnichain history, recovery checkpoints and global event merging passed (four selected cases). The source was unchanged.

```sh
# packages/core at 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334
vitest run --maxWorkers=1 --hookTimeout=45000 -t 'getHistoricalEvents\(\)|getLocalEventGenerator\(\) pagination checkpoints|mergeAsyncGeneratorsWithEventOrder\(\)' src/runtime/historical.test.ts src/runtime/index.test.ts
```

The verification tools passed 503 Node tests, 40 Python discovery tests and three observer tests. The tool suite was rerun after restoring its archived inputs and registering the affected native test files.

The native integration and store tests add coverage beyond the pinned inventory of 883 Go cases (866 passed and 17 provider RPC failures). Full-module ordinary and race regressions for this revision are pending.
