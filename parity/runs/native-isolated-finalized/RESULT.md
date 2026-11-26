# Independent-store Isolated finalized ranges

The Go historical runtime accepts bounded Isolated deployments on PostgreSQL and PGlite. Admission checks the end of each chain against its own finalized height. The business schema uses a `chain_id` primary-key component. Every processed block, including an empty block, commits its business transaction and its per-chain safe receipt together. The deployment uses the current `managed-live-isolated-finalized-v1` format.

## Two-chain recovery

Two local Anvil 1.8.5 chains have different block times and finalized heights. Chain 101 starts at genesis and ends at block 3, later in time than chain 202's finalized head. Chain 202 starts at block 1 and ends at block 2 with its RPC cache disabled. Both chains register a block callback and an incoming account transaction callback.

| Step | Observed result |
| --- | --- |
| Hold chain 101's first non-genesis block request | Chain 202 runs both of its block callbacks. |
| Chain 101 writes at block 2, attempts to delete a chain 202 row and returns an error | The typed database rejects the cross-chain access. The failed block's write rolls back. Both chain 202 rows and its transaction row remain. |
| Reopen the same storage and build under a new owner | Chain 101 retries block 2 and completes block 3. The chain 202 callbacks and both chains' transaction callbacks retain their original delivery counts. Both finalized cursors bind to the hashes returned by the nodes. |
| Close and open a third time | The run completes with zero historical blocks and identical business rows. |

Preflight cases exercise a live request, a setup callback, an end beyond one chain's finality and a table missing the chain key. Receipt tests bind the safe record to its ordering mode, owner, generation, predecessor, exact heads and source positions.

## Verification

Use fresh task-owned PostgreSQL and PGlite storage, Anvil and Go 1.25.11. Run the pinned reference checks in `packages/core` of Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

```sh
selection='^(TestRuntimeIsolatedFinalized|TestRuntimeIsolatedPreflight|TestRuntimePreflightRejectsUnsupported|TestRuntimeOmnichainPreflightBoundary|TestRuntimeOmnichainFinalizedOrderAndReopen|TestRuntimeBoundedLifecycle|TestManagedFiniteOptionsAndSchemaSnapshot|TestOmnichainAtomicSafeReceiptBinding)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./historical ./store
go test -p 1 -race -v -count=1 -timeout=45m -run "$selection" ./historical ./store
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
node_modules/.bin/vitest run --maxWorkers=1 src/build/schema.test.ts src/database/actions.test.ts src/runtime/historical.test.ts
```

Each focused Go run passed all 47 leaf cases with zero skips. All Go packages compiled and passed vet. All 49 pinned reference cases passed. Repository tooling passed 503 Node.js cases and 43 Python cases.

Both RPC endpoints must be available during acquisition. A callback error ends the run. Per-chain setup, continuous tracking and chain-local recovery from unfinalized forks remain open for the independent-store runner.
