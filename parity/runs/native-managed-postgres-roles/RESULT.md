# PostgreSQL roles for Go indexers with independent business and sync stores

A coordinated reopen verifies both ledgers, the prior binding and the complete sync catalog under the two owner locks. An authenticated cache opens with its existing storage markers and layout. Fresh or interrupted unbound initialization still follows the provisioning path. [Bootstrap](../../../store/managed_live_domains_bootstrap.go) · [Open](../../../store/independent_storage.go)

The acceptance test uses real login connections to a fresh PostgreSQL 14.24 cluster for each ordering. It revokes the database and public schema defaults, prepares the deployment under its writer identity, revokes database CREATE, and executes the documented handoff as the ordinary manager. Both logins lack superuser, role creation and database creation capabilities. The manager owns the business namespaces and manages a no-login cache owner. The writer has business object ownership for activation and schema usage with scoped DML for the sync cache. PostgreSQL temporary table privileges support transaction-local notifications. [Documented SQL](../../../docs/examples/postgres-managed-writer.sql) · [Usage](../../../docs/USAGE.md#双库-postgresql-的写入与管理权限)

## Cases

- Default live multichain prepares the published schema and an index over balances. The restricted writer reopens, processes both chains, rolls back a fork, replays, closes, and cold-reopens again. The published view returns two balances of five. Callback balance reads use the local JSON-RPC transport.
- Finalized Isolated and Omnichain first open and close without running callbacks. The writer then activates, backfills both ranges, reopens a finished deployment without replaying history, and returns all six expected chain/height rows under each ordering.
- PostgreSQL rejects database schema creation, dropping the business namespace, cache DDL, writes to immutable cache metadata and the ledger specification, deleting prior sync log entries, and access to the unrelated schema. Neither privileged role can be assumed by the writer. The test revokes the sync log SELECT grant, observes SQLSTATE 42501 on reopen with the ledger sequence unchanged, then restores that grant and reopens. The manager also lacks access to an unrelated table. The writer can alter its own business tables and activation objects.

[Integration test](../../../historical/postgres_roles_test.go) · [Sync integrity cases](../../../store/managed_live_domains_integrity_database_test.go)

## Reproduction

Use the pinned Go toolchain, PostgreSQL 14.24 engine fixture and PGlite. The role tests start their own private clusters via `PONDER_TEST_POSTGRES_BINDIR` or `PONDER_TEST_POSTGRES_ENGINE_IMAGE`. The other database tests require the repository's task-owned database and fixture roots.

```sh
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 -run '^TestRuntimePostgresDeploymentRoles$' ./historical
  go test $mode -p 1 -count=1 -run '^TestRuntimeBoundedLifecycle$' ./historical
  go test $mode -p 1 -count=1 -run '^Test(SyncMigrationBoundaries|ManagedLiveDomainsBootstrap|OpenExistingDeployment)$' ./store
  go test $mode -p 1 -count=1 -run '^TestManagedLiveDomainsIntegrity$/^postgres$' ./store
  go test $mode -p 1 -count=1 -run '^TestManagedLiveDomainsIntegrity$/^pglite$/^(native_meta_missing|cache_owner|rpc_trigger)$' ./store
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

Six pinned upstream database-action controls passed using `vitest run --maxWorkers=1 src/database/actions.test.ts -t 'createIndexes\(|createTriggers\(|empty schema|live query notify trigger batches large payloads'` against a task-owned PostgreSQL database. Five unrelated cases were filtered by name. The Go role cases are supplemental acceptance coverage.

All 35 selected Go cases passed once in ordinary mode and once with the race detector. The selection includes 3 role cases, 12 migration cases, 4 coordinated bootstrap cases, 12 corruption cases, 2 existing-store cases and 2 bounded runtime cases. All 51 Go packages compiled and `go vet -p 1 ./...` completed without findings. The tool suite passed 503 Node tests and 43 Python tests. Full-module ordinary and race results are tracked separately from this selection.

## Go indexer scope check

A separate Go probe opened an in-memory PGlite database and ran `SELECT sum(i)::text AS total FROM generate_series(1,1000000) AS g(i)` with a 40 ms context deadline. The context expired during the query. The call returned the correct sum of 500000500000 after about 870 ms and reported no SQL error. This confirms the documented limitation for in-flight cancellation. [Go driver](../../../store/backend.go) · [Usage](../../../docs/USAGE.md#使用本地-pglite)
