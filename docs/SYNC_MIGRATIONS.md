# Current sync-cache schema

`NewSyncMigrator(backend, scope).Migrate(ctx)` creates the current RPC cache
in `<scope>_rpc_1`. `Lifecycle.MigrateSync` uses the lifecycle namespace as
its scope. A fresh cache has nine tables. Reopening checks the ownership
comment and catalog, then preserves the cached results. Scope names start with
`ponder_go_` and are at most 45 characters.

Startup requires a fresh scope or an existing current cache. If only an old
`<scope>_legacy` namespace exists, choose a fresh scope and let indexing
rebuild the cache. When a valid current cache already exists, startup uses it
directly. Historical contents are never copied or dropped.

PostgreSQL serializes concurrent creators with an advisory transaction lock.
A namespace conflict triggers a fresh owner and catalog check. PGlite uses
the transaction queue of the opened backend. Schema creation runs in one
transaction. A failed creation can be retried, including after a lost commit
acknowledgment.

The pinned DDL and its digest are listed in [the SQL asset manifest](../parity/sync-migration-assets.json).
The original cases for fresh creation, repeat startup and concurrent startup
remain mapped to Go. The five upstream migration cases retired from the Go
contract remain individually identifiable in [the scope inventory](../parity/out-of-scope.json).
