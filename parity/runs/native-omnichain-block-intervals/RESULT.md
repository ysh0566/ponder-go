# Finalized Omnichain block sampling with independent stores

The Go runner accepts multiple block sources per chain, each with a finite range and a positive interval. A single every-block source covering the chain's event ranges uses its existing dense receipt. Other schedules use the sparse receipt. Each committed block checks the expected source IDs in registration order. The receipt binds the source schedule, and skipped heights are checked arithmetically even if a cache row is missing. Two callbacks at one height share a checkpoint and commit in the same transaction.

Both the ordinary and race selections passed all 85 leaf cases with zero skips. All Go packages compiled and vet passed. The three upstream suites passed 48 cases. Repository tooling passed 503 Node.js and 43 Python cases. Whole-module execution for this change is tracked separately.

## Reproduction

Use the repository's task-owned PostgreSQL and PGlite test configuration, Go 1.25.11 and Anvil 1.8.5. The two-chain test mines real transactions and blocks and runs with opposite cache policies in both directions. On one chain, callbacks at intervals five and two coincide at block six. The other chain combines sampling with a genesis-only source and a shorter every-block source. The tests check the ordered business rows, interrupt the second simultaneous handler after its write, reopen the same stores, verify that the empty tail reached block eight, and reopen once more to check that completed handlers do not repeat.

```sh
selection='^Test(RuntimeOmnichain(BlockIntervals|PreflightBoundary|FactoryPreflight|StaticLogsCapabilities|LogOnlyPhysicalRange|SourceOracle)|SparseBlockScheduleEvidence|LogOnly(PhysicalInputs|ScanDecodedCoverage|ScanReceiptBinding)|Omnichain(AccountScanEvidence|ContractCallScanEvidence))$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./historical ./store
go test -p 1 -race -v -count=1 -timeout=45m -run "$selection" ./historical ./store
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

The upstream oracle is pinned to Ponder 0.18.2 commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Run from its `packages/core` directory.

```sh
node_modules/.bin/vitest run --maxWorkers=1 src/runtime/filter.test.ts src/runtime/events.test.ts src/runtime/historical.test.ts
```

All sources in this runner must end within the global finalized watermark. Real-time Omnichain reorganization handling and independent-store Isolated ordering remain open.
