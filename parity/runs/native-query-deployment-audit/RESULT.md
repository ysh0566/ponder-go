# Go query-only deployments

Audited `main` at `7dc6c8f0968f6ec042e28dfa5ea6d4d3444b9c09` against Ponder 0.18.2. This check concerns a Go-authored indexer and a separate query process using the current store format.

## Query notifications

On a task-owned PostgreSQL 14.24 database, the probe called the public `store.QueryChanges.Listen` for a unique namespace, subscribed to changes and sent a committed `pg_notify` for the `account` table on a second connection. The subscriber received the first notification.

The probe selected the one session whose database matched the test database and whose last command exactly matched `LISTEN "<unique namespace>_live_query"`. After terminating just that session, the subscription's `Done()` closed and all dependent queries were invalidated once. The probe consumed that invalidation before sending a second notification. There was no notification within one second, and `QueryVersion` reported no active listener. Explicitly calling `Listen` again restored delivery of a third notification. PostgreSQL and the client process stayed running throughout.

The HTTP stream exits when its subscription's `Done()` closes. Fresh SQL reads remain available. A long-running Go query service needs to recreate the listener and reread the affected results after reconnecting. The pinned upstream PostgreSQL middleware retries its listener after 250 ms. [Go listener](../../../store/query_changes.go) · [Go HTTP stream](../../../query/http.go) · [Upstream middleware](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/client/index.ts#L239-L345)

## Generated Go starter

`ponder init` embeds `examples/erc20`. The starter validates the database URL, RPC URL and contract address before selecting serve mode. Building the entry point and launching it with `PONDER_COMMAND=serve` and a task-owned PostgreSQL URL, with the RPC URL and contract unset, exited with code 1 and requested the two chain settings.

The entry point opens PostgreSQL and uses `query.New`, which subscribes to in-process store changes. A standalone application can select PGlite using `store.OpenPGlite`. Applications that run separate SQL/SSE processes can select a writer's published channel using `query.NewAtNamespace`. [Starter](../../../examples/erc20/main.go) · [CLI scaffold](../../../cmd/ponder/scaffold.go) · [Cross-process SQL and cache tests](../sql-client-queries/RESULT.md)
