# Separate Go query service

The generated Go application can use an existing atomic PostgreSQL deployment. Its query-only process checks the schema, ordering and current storage fingerprint without creating schemas, running DDL or initializing an RPC client. The writer installs transactional notifications for business tables and checkpoint changes. The reader pins the deployment against administrative pruning until its backend closes.

- `TestGeneratedGoServe` creates a standalone module through `ponder init`, points its dependency to the code under test, runs `go mod tidy` and launches the real `ponder serve` child. Its RPC and contract settings are empty and the connection uses `default_transaction_read_only=on`. An independent writer inserts a balance and rolls it back. The same HTTP/SSE connection returns the balance and then an empty result. A separate SQL request reads the committed row.
- `TestOpenExistingDeployment` exercises PostgreSQL 14.24 and PGlite 0.2.13. Opening a missing deployment leaves both namespaces absent. An existing deployment rejects a different model or ordering. Enabling notifications twice succeeds. A colliding function keeps its original value. Aborted writes produce no notification. A later committed insert delivers the table name.
- A separate PostgreSQL probe created a new non-superuser login with schema USAGE and SELECT on the business table, the business and sync metadata tables, and the business progress table. The login opened the deployment, read rows and status, and established LISTEN. A direct INSERT using that login returned SQLSTATE 42501. The published example grants are recorded in the [usage guide](../../../docs/USAGE.md#重开索引或只提供查询).

The independent process test uses PostgreSQL. PGlite verification covers deployment checks and notifications in the same engine. [Generated project test](../../../cmd/ponder/serve_queries_test.go) · [Storage tests](../../../store/open_existing_test.go)

## Verification

The complete CLI and query packages passed in ordinary and race modes. Store selections included read-only deployment opens, transactional live-query changes, raw notifications and both database stores. The CLI ran its real PostgreSQL publish/prune tests, including pruning a deployment with notification triggers installed. All packages compiled and passed vet.

```sh
go test -count=1 ./cmd/ponder ./query
go test -race -count=1 ./cmd/ponder ./query
go test -count=1 ./store -run 'Test(OpenExistingDeployment|LiveQuery|PostgresStore|PGliteStore|DatabaseNotifications|LifecycleViews)'
go test -race -count=1 ./store -run 'Test(OpenExistingDeployment|LiveQuery|PostgresStore|PGliteStore|DatabaseNotifications|LifecycleViews)'
go test -run '^$' ./...
go vet ./...
```
