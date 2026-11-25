# Native Go omnichain indexing

Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13 ran two independent EVM chains against one shared application Store. Both tests exercise the public `NewIndexerWithOptions` and `RunIndexing` entry points. The PostgreSQL selection enables the protocol cache and the PGlite selection disables it.

The continuous test configures chains in descending ID order. Both genesis blocks have the same timestamp. A shared ledger records chain 101 before 202, then callbacks by timestamp up to the earliest known head. Replacing chain 101's first two blocks rewinds and rebuilds the shared suffix, including chain 202's genesis. After a cold open neither chain repeats a callback. Forward progress resumes on both chains. A stale completion proof on the second chain rolls back the whole attempted finalization. Once both heads advance beyond the reorg window, a fork below the globally safe position leaves the business rows intact and requires a rebuild.

The bounded test combines a genesis setup callback, a factory that discovers an account and a contract, a closing block handler, transactions, value transfers and contract CALL traces on one chain. The other chain supplies only static ABI log callbacks. The faster chain's pending log waits for the earlier block on the other chain. A callback writes a row and returns an injected error. After closing and reopening the database, that write is absent, successful callbacks keep their exact order, and every expected business row appears once. Both configured end heights finalize, the undo journal is empty and another run performs no callbacks.

| Selection | Commands |
| --- | --- |
| Native ordering, existing isolated, single-chain, subscription and source workers | `go test -count=1 -run '^(TestOrderedIndexer\|TestOrderedMixedSources\|TestIsolatedIndexer\|TestIsolatedBoundedCompletion\|TestContinuousFinality\|TestHeadNotifications\|TestApplicationRPCRecovery\|TestFactoryRuntime\|TestSourceConfigurationRuntime)$' ./runtime` and the same with `-race` |
| Store ordering, scoped reorgs and staged actions | `go test -count=1 -run '^(TestPGliteStore\|TestPostgresStore\|TestUpstreamStagedActions\|TestUpstreamStagedDataActions)$' ./store` and the same with `-race` |
| Module-wide build and static checks | `go test -run '^$' ./...` and `go vet ./...` |

The managed historical runner retains its separate admission rules and storage domains. Native regression cases leave the upstream inventory totals unchanged.
