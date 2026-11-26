# Native Go Isolated indexing with multiple sources

The fixture runs the public `runtime.NewIndexerWithOptions` entry point against two Anvil 1.8.5 chains. Chain A discovers a child contract and account from a factory. Its callbacks receive setup, blocks, a child log, a contract CALL, an incoming transaction and a transfer. Chain B receives a static contract log. Its end height differs from chain A's. The table key includes `chain_id`, as required for Isolated ordering.

One CALL handler writes a row and returns an error. The test closes the database connection or engine, reopens the same deployment, and runs to finalized completion. It checks callback order within each chain, receipt and event data, every persisted key and its chain ID, and the removal of the interrupted write. Another run at the same heights must deliver zero additional callbacks. PostgreSQL 14.24 uses the RPC cache and PGlite 0.2.13 disables it in this fixture.

The same selection exercises the existing block-callback fork test. Chain A switches branches while chain B has finalized two blocks. Both of chain B's hashes and its callback count survive the rollback and reopen. A live fork during the mixed-source fixture remains an open test scenario.

## Reproduction

Run against fresh test databases with the repository Go and Foundry toolchains. The ordinary and race selections include the two other native ordering modes and both dual-store admission checks.

```sh
selection='^(TestIsolatedMixedSources|TestDefaultMixedSources|TestOrderedMixedSources|TestIsolatedIndexer|TestIsolatedBoundedCompletion|TestRuntimePreflightRejectsUnsupported|TestRuntimeOmnichainPreflightBoundary)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./historical ./runtime
go test -p 1 -race -v -count=1 -timeout=45m -run "$selection" ./historical ./runtime
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

The ordinary and race selections each passed all 27 leaf tests. All packages compiled and passed vet. Repository verification passed 503 Node.js tests and 43 Python tests.

The upstream schema and database-action suites at Ponder 0.18.2 commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` passed all 35 tests. From its `packages/core` directory, run `node_modules/.bin/vitest run --maxWorkers=1 src/build/schema.test.ts src/database/actions.test.ts`.

The independent business/sync runner rejects Isolated before opening storage. Its Omnichain runner accepts finite ranges up to the global finalized watermark and rejects live mode. The native mixed-source test adds coverage without changing those product limits or the upstream test mappings.
