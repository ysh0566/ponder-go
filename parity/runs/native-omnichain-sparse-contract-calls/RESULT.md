# Direct contract CALL callbacks in finalized Omnichain ranges

A Go application with independent business and sync stores can subscribe to contract CALLs over finite, globally finalized ranges without registering a block callback. Both static and factory-discovered addresses are supported. Set the contract source's `includeCallTraces` option and supply the function ABI. Each chain can enable or disable RPC caching independently. [Usage](../../../docs/USAGE.md#独立存储的有限区间索引)

The cached path queries candidate traces and transactions from the sync store. With the cache disabled, the scanner reads a block by its finalized hash and verifies its transaction count, receipts and call trace roots. A successful root CALL's input must agree with the transaction. A nested CALL uses its own input. Reverted subtrees are excluded. Blocks between disjoint source ranges do not require a trace request. Both paths apply the source's height, address, factory birth and function selector before decoding the arguments and return values. Each matching ABI gets its own decode attempt. The durable proof binds the ABI definitions even if every candidate is skipped.

The Go integration test starts two Anvil chains with interleaved timestamps, a cached static contract, and an uncached contract created by a factory. It exercises calls to unrelated addresses, a call before factory creation, an empty internal interval and a trailing interval. The writer handles the expected transactions and decoded return values, interrupts the last callback after a business write, and reopens twice. Both database backends must retain exactly the committed prefix, finish at block five on both chains, and retain block four as the last business event.

The underlying database checkpoint format already represents contract CALL and transfer positions. Receipts additionally verify that the persisted event kind matches a configured source at that height. Historical indexing uses the pinned upstream callback ordering and decoding behavior. Cross-chain live reorgs and Isolated ordering remain open for this independent-store runner.

## Reproduction

Run from the Go repository with a task-owned PostgreSQL database, the PGlite worker and Anvil 1.8.5. The runtime integration cases expect the repository's managed test environment.

```sh
selection='^Test(RuntimeOmnichainSparseContractCalls|RuntimeOmnichainPreflightBoundary|RuntimeOmnichainStaticLogsCapabilities|OmnichainAccountScan|OmnichainContractCallScanEvidence|OmnichainContractCallScanRPCData|OmnichainUncachedContractCallEvidence|OmnichainAccountScanEvidence|OmnichainAccountScanWithABILog|OmnichainUncachedAccountEvidence|OmnichainUncachedAccountFactoryAndLog)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./store ./historical
go test -race -p 1 -v -count=1 -timeout=45m -run "$selection" ./store ./historical
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

From `packages/core` at upstream revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, run the event, historical ordering and factory oracles.

```sh
node_modules/.bin/vitest run --maxWorkers=1 src/runtime/events.test.ts src/runtime/historical.test.ts src/build/factory.test.ts
```

The final selections passed all 91 Go leaf cases in both ordinary and race mode, with zero skips. An early PGlite run was interrupted by insufficient workspace storage. The completed runs used fresh test databases after reclaiming temporary space. All Go packages compiled and vet passed. The three upstream files passed 31 tests. The repository tooling passed 503 Node.js and 43 Python tests. Whole-module ordinary and race execution is tracked separately.
