# Per-chain setup for finalized Isolated backfills

A Go indexer using the independent business and sync stores can register a contract setup handler on selected chains. The runner authenticates each initial block against that chain's finalized head. All setup writes on a chain and its initial progress, safe and finalized checkpoints share one transaction. Chains without setup handlers still acquire an initial checkpoint. Event delivery starts after every chain is initialized.

## Failure and restart

The integration test uses three controlled HTTP JSON-RPC endpoints and runs on PostgreSQL 14.24 and PGlite 0.2.13. Chains 1 and 2 run a contract setup handler at heights 1 and 0 respectively. Chain 2 disables the RPC cache. Chain 3 has only a block callback.

| Opening | Checks |
| --- | --- |
| First | The chain 1 handler reads the chain, checks that cross-chain access is rejected, inserts a row and fails. Chain 2 commits its row. Chain 3 saves its initial position. The closed business database contains one setup row and checkpoints for exactly chains 2 and 3. |
| Retry under a new owner | Only chain 1 retries setup. The post-setup observer stops the run before an event can execute. The business rows for both handlers and the initial positions for all three chains are durable. |
| Resume | All three chains receive both block callbacks. Each initialized chain sees its own setup row. Chain 3 has no setup row. |
| Reopen after completion | The indexer is ready, reports zero historical blocks, and preserves all callback counts and final positions. |

The finality admission test also supplies a setup block above one chain's finalized height. Opening returns an error before the storage connection is used.

## Reproduction

Use the repository's PostgreSQL, PGlite and local Anvil test configuration with Go 1.25.11. The target includes existing bounded default-multichain and Omnichain setup behavior, plus the usual finalized Isolated run without setup.

```sh
selection='^(TestRuntimeIsolatedSetup|TestRuntimeIsolatedPreflight|TestRuntimeIsolatedFinalized|TestRuntimeOmnichainSetup|TestRuntimeOmnichainSetupFinality|TestRuntimeOmnichainSetupLineage|TestBoundedRuntimeSetupAndCache)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./historical
go test -p 1 -race -v -count=1 -timeout=45m -run "$selection" ./historical
go test -p 1 -v -count=1 -run '^(TestManagedLive(Segments|Finalize|Reorg)Unit.*|TestOmnichainAtomicSafeReceiptBinding)$' ./store
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The focused ordinary and race runs each passed all 22 leaf cases with zero skips. The final assertions for all three public checkpoint fields and both setup RPC heights also passed on both backends under the race detector. The storage regression passed 160 leaf cases. All Go packages compiled and passed vet. Repository verification passed 503 Node.js tests and 43 Python tests.

The reference at Ponder 0.18.2 commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` passed the six selected setup cases. Run the following in `packages/core`.

```sh
node_modules/.bin/vitest run --maxWorkers=1 src/indexing/index.test.ts -t 'processSetupEvents|executeSetup'
```

The Isolated runner still requires bounded finalized ranges. Continuous dual-store indexing and chain-local recovery from unfinalized forks remain open.
