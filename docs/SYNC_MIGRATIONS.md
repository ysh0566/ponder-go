# Current sync-cache schema

`NewSyncMigrator(backend, scope).Migrate(ctx)` creates the current RPC cache
in `<scope>_rpc_1`. `Lifecycle.MigrateSync` uses the lifecycle namespace as
its scope. The migrator checks the nine tables against the pinned DDL before
committing a new cache. On reopen it verifies the namespace ownership comment
and checks the same layout. A mismatched table raises a migration error naming
that table. Standalone cache reads and writes also verify ownership and layout.
Scope names start with `ponder_go_` and are at most 45 characters.

The catalog comparison covers column order, builtin types and modifiers,
nullability, defaults, identity generation, collation and keys. Ordinary
secondary indexes can be added without changing the format. The reference
contains one entry per table. Column entries use the PostgreSQL type name and
type modifier. Key entries record the constraint type, deferral and validation
flags, and ordered columns. [Expected catalog](../store/syncsql/layout.json)

Startup requires a fresh scope or an existing current cache. If only an old
`<scope>_legacy` namespace exists, choose a fresh scope and let indexing
rebuild the cache. When a valid current cache already exists, startup uses it
directly. Managed live domains also verify exclusive ownership of their entire
namespace family before opening either database.

PostgreSQL serializes concurrent creators with an advisory transaction lock.
A namespace conflict triggers a fresh owner and catalog check. PGlite uses
the transaction queue of the opened backend. Schema creation runs in one
transaction. A failed creation can be retried, including after a lost commit
acknowledgment.

The pinned DDL and its digest are listed in [the SQL asset manifest](../parity/sync-migration-assets.json).
The original cases for fresh creation, repeat startup and concurrent startup
remain mapped to Go. The five upstream migration cases retired from the Go
contract remain individually identifiable in [the scope inventory](../parity/out-of-scope.json).
