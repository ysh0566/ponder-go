# Versioned sync-cache migration

`NewSyncMigrator(backend, scope)` implements the fixed source's
`ponder_sync → ponder_rpc_cache_1` migration inside an explicit task-owned scope.
`Lifecycle.MigrateSync` delegates to it using that lifecycle's namespace as scope.
It can run before indexing migration and does not create indexing app metadata.

The native names are `<scope>_legacy` and `<scope>_rpc_1`, where scope must be a
`ponder_go_` identifier. Existing schemas need their matching ownership comment;
unowned schemas are rejected. This route copies a prepared legacy shadow, never
adopts or alters the real global `ponder_sync` or an existing business database.
There is no direct real-database import/takeover API in this milestone.

The two production SQL assets and the original legacy test fixture retain their
exact baseline bytes. Execution replaces only quoted schema identifiers. Their
source paths and SHA-256 hashes are in `parity/sync-migration-assets.json`.
The latest schema contains all nine original relations, with original columns,
numeric widths, identity sequence, multirange type, primary/unique constraints
and indexes. An ownership comment adds no extra relation to the source catalog.

## Migration behavior

A fresh scope creates only the latest schema. An existing latest schema is
checked and left intact. If a legacy shadow exists, it must contain the original
`2025_02_26_1_rpc_request_results` migration entry, even if latest already exists.
The legacy history and data are read without updates.

Creation and copying share one repeatable-read transaction, preserving one
snapshot across copied tables. Original SQL copies blocks, transactions,
transaction receipts, logs, RPC results and eligible intervals. It does not copy
legacy trace rows, factories, child addresses, trace/transfer intervals or
factory-derived fragments. The source exclusion SQL is retained verbatim.

PostgreSQL startups share an advisory transaction lock. A repeatable-read
snapshot may still predate a concurrent creator; the source's specific namespace
conflict is handled by rolling back and freshly checking the committed owner and
complete latest schema. Other SQL errors are propagated. PGlite migrations share
one existing backend instance and execute serially; concurrent processes on a
single PGlite data directory are not supported.

An interrupted or failed copy rolls back its entire latest schema. If commit
completed but its outcome is unclear to the caller, a retry finds the complete
owned latest schema and preserves its data. Partial-schema states are rejected.

`InsertRPCResult` preserves the original plain insert/constraint behavior;
`RPCResults` returns typed chain ID, nullable block number and opaque result text.
Scalar Go integers cross the JSON bridge as decimal strings. This prevents
PGlite/JavaScript rounding values beyond 2^53, including signed-bigint limits.
Integer parameter normalization is shared with the existing native backend;
JSON document values retain their separate column codec behavior.

## Current integration boundary

The versioned nine-table schema remains separate from indexing data. The native
single-chain event path now attaches it for normalized block/transaction/log/receipt
writes, RPC results, intervals, factory discoveries and attached-cache reorg cleanup.
See PROTOCOL_CACHE.md for tested boundaries and remaining gaps. Full source CRUD
tests, fragment/query planning, automatic traces, staged/multichain scheduling and
shared-cache options remain unfinished. Source raw-layout compatibility and real
old DB takeover remain separate tracks.

The eight source migration cases retain all 13 original assertions and their
fixtures. PostgreSQL runs eight primary mappings; PGlite runs seven applicable
companions. The original snapshot test returns before its fixture/assertion on
PGlite, so it receives no invented companion credit. Its PG version uses an
actual ACCESS EXCLUSIVE lock, observes the migration waiting in pg_locks, commits
the late interval and verifies that the earlier snapshot excludes it.

Additional tests verify all copied column values, NULL/large integers/opaque text,
legacy immutability, rollback and retry, an uncertain commit acknowledgment,
16 independent migrators, ownership checks and incomplete schemas. Four real
application-process exits cover partial copying and post-commit reopening on
PostgreSQL and disk-backed PGlite. They do not establish server/host power-loss
behavior. The complete original database test oracle still requires the pending
Foundry-generated fixture; it remains distinct from native execution evidence.
