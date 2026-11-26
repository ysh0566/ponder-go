# SQL bind parameter budget for the dual-store Go runtime

Both public dual-store constructors accept `RuntimeOptions.Historical.MaxQueryParameters`. Acquisition, transaction-bound writes and cache verification share the configured sync store. The setting applies to batched intervals, factory children, block, log, transaction, receipt and trace rows. The default is 16,000. An explicit budget must be at least four, and statement batching caps the effective value at the PostgreSQL Bind limit of 65,535. A data row that cannot fit in a single statement identifies the table, required width and configured limit in the error. [Store](../../../store/source_intervals.go) · [Runtime](../../../historical/live_runtime.go)

The value can change between openings. Recovery uses the durable source positions, business model, build identity and storage locations. The budget does not affect checkpoint encoding or the history query page size.

## Coverage

- The SQL test records the actual bound arguments on PostgreSQL and PGlite. A single transaction inserts five distinct logs with a budget of 25 and issues three INSERTs with 24, 24 and 12 parameters. All five rows persist.
- A two-chain Omnichain fixture starts with a budget of four, which cannot encode a log or block. The run stops before the first business callback. On reopening with 32, the runner commits part of the range before a deliberate handler failure. Reopening the same deployment with 64 resumes the expected callback suffix. A final opening uses the default budget, with zero further calls. Each opening checks committed rows, undo entries and both chains' logical positions against the pinned reference snapshots.
- The default multichain and Isolated cases use two chains with finite ends in the future. A cache write first fails during backfill under a four-parameter limit. Both stores reopen with a limit of 32. Each chain executes its block-one callback during backfill, advances to block two after activation and commits its live callback. The test checks completion, the two durable block cursors and the number of handler invocations.
- Invalid explicit budgets fail before the Go runtime opens an RPC transport. A zero budget selects the upstream default. The effective upper limit is checked at and above the protocol boundary.

The upstream selection uses Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` and PostgreSQL 14.24. From `packages/core`, the following command passed 12 cases and excluded 26 other tests by name.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/sync-store/index.test.ts -t 'supports every batch remainder|insertChildAddresses|insertLogs|insertBlocks|insertTransactions|insertTransactionReceipts|insertTraces'
```

## Go verification

With Go 1.25.11, PostgreSQL 14.24 and PGlite configured, the following commands also exercise existing transaction rollback and admission checks.

```sh
selection='^(TestLiveRuntimeSyncBudget|TestRuntimeSyncParameterBudget|TestRuntimeInvalidSyncBudget|TestSourceQueryParameterBudget|TestSourceSyncBudgetSplitsLogs|TestRuntimeRejectsHistoricalOptions|TestRuntimeIsolatedPreflight|TestRuntimeOmnichainPreflightBoundary|TestSourceSyncTransaction)$'
go test -p 1 -count=1 -timeout=35m -v -run "$selection" ./historical ./store
go test -p 1 -count=1 -timeout=35m -v -race -run "$selection" ./historical ./store
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The focused selection passed 46 leaf cases with zero skips in ordinary mode and again under the race detector. An earlier race invocation lost a PGlite child process to a system kill. The complete selection subsequently passed in both modes against fresh fixtures. All 50 Go packages compiled and passed vet. Repository checks passed 503 Node.js and 43 Python tests. Module-wide ordinary and race regressions have a separate queue and are pending.
