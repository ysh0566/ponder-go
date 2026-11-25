# Default multichain indexing through the native Go entry point

The fixture runs two Anvil 1.8.5 chains against a PostgreSQL 14.24 or PGlite 0.2.13 atomic Store. A callback on chain A executes with no writes, then chain B commits and finalizes a row that depends on the shared state. Replacing A's block causes B's callback to run again and derive a different value. Both chain RPCs return HTTP 503. A alone reconnects first. The rewind and a cold store reopen leave replay pending until B returns. Replacing an empty block on A preserves B's callback count. A second reopen preserves the rebuilt rows. If B subsequently changes an already-finalized hash while offline during another A rewind, indexing stops at the finality fence.

The finite-source case exercises setup at genesis, factory discovery, static logs, transfers, transactions and contract function traces. PostgreSQL uses the RPC cache and PGlite disables it. The faster chain commits its log while the slower chain is still below its timestamp. A failed callback rolls back its transaction. The reopened worker reaches both end blocks, leaves no undo rows and repeats no completed callback. A storage regression retains a peer's proven header and its earlier callback if a global rewind splits that peer block.

| Selection | Commands |
| --- | --- |
| Two-chain runtime, outage, ordinary and global ordering | `go test -count=1 -run '^(TestDefaultIndexer\|TestDefaultMixedSources\|TestOrderedIndexer\|TestOrderedMixedSources\|TestIsolatedIndexer\|TestIsolatedBoundedCompletion\|TestContinuousFinality\|TestApplicationRPCRecovery)$' ./runtime` and the same selection with `-race` |
| Storage, reorg, format and staged regressions | `go test -count=1 -run '^(TestMultichainPartialPeerBlock\|TestPGliteStore\|TestPostgresStore\|TestStorageVersionRejectsOldJournal\|TestStoreFormatMarkers\|TestOpenExistingDeployment\|TestReorgCrashRecovery\|TestUpstreamStagedActions\|TestUpstreamStagedDataActions)$' ./store` and the same selection with `-race` |
| Original oracle | Pinned core Vitest on PGlite, `src/sync-realtime/index.test.ts`. The selected block delivery, duplicate notification, finalize and common-ancestor cases passed (4). |
| Static checks | `go test -run '^$' ./...` and `go vet ./...` |

The runtime selection has 14 leaf cases and the storage selection has 58. The repository verification tools passed 503 checks. The Go cases exercise native scheduling and durable recovery. The pinned source inventory and mapping counts are unchanged. The separately deployed historical runner retains its own source and storage constraints.
