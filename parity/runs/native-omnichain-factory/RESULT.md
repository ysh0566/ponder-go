# Factory sources in bounded independent Omnichain indexing

A Go indexer using the independent business and sync stores can discover addresses from a factory event and register contract logs, CALL traces, account transactions and native transfers against those addresses. Each chain needs one closing block callback per height covering its factory and event ranges. The interval must fall within global finality. Enable both `FactorySources` and `SourceCheckpoints` and leave `Live` unset.

## Recovery scenario

Two local Anvil chains have the same parent address and two potential children running identical bytecode. One candidate is discovered on chain 101 at block 2. The other is discovered on chain 202 at block 1. Every business block includes calls to both addresses. The handlers check the selected transaction hash and the decoded CALL result, and append their keys to one shared business row. Eight closing block callbacks and 28 child callbacks are expected in cross-chain checkpoint order.

The test interrupts chain 202's final child-log callback after its business write. It reads the committed prefix from the closed storage and verifies that it ends at a whole block, after the uncached chain's discovery. A cold reopen must replay exactly the remaining callbacks. A further reopen must invoke none. The sync table must contain exactly the cached chain's child with its correct birth height. The uncached chain reconstructs its child set from the configured discovery range.

Three preflight cases cover a log-only factory, a missing factory opt-in and a discovery range exceeding its closing block range. All three must stop before opening the runner's RPC clients or stores.

## Verification

```sh
go test -v -count=1 -run '^TestRuntimeOmnichainFactory(Preflight)?$' ./historical
go test -race -v -count=1 -run '^TestRuntimeOmnichainFactory(Preflight)?$' ./historical
```

The reference run used the original Ponder 0.18.2 source at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Thirteen selected cases passed across historical Omnichain ordering and cached and in-memory factory acquisition. The Go integration test separately exercises the combined two-chain database lifecycle.

```sh
vitest run --maxWorkers=1 --hookTimeout=45000 src/sync-historical/index.test.ts src/sync-historical/in-memory.test.ts src/runtime/historical.test.ts -t 'log factory|factory addresses|trace factory|getHistoricalEvents\(\) omnichain'
```

The five new cases passed in ordinary and race runs with Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13. The storage controls passed in both modes. Seven existing ordinary controls passed across default-ordering uncached discovery, Omnichain uncached event refetch and startup validation.

```sh
go test -count=1 -run '^Test(LogOnly|OmnichainAtomicSafeReceiptBinding)' ./store
go test -race -count=1 -run '^Test(LogOnly|OmnichainAtomicSafeReceiptBinding)' ./store
go test -v -count=1 -run '^Test(OmnichainClosingUncachedRefetch|RuntimeFactoryLifecycle|RuntimeOmnichainPreflightBoundary)$/^(postgres|pglite|factory|live|cache-false)$/^early_birth_uncached$' ./historical
```

All Go packages compiled, vet passed and the Go usage example produced the expected factory declaration. Project verification tools passed 503 Node checks, 40 Python discovery tests and three observer tests. The full-module ordinary and race runs for this revision are pending. The 883-case Go port inventory is unchanged.
