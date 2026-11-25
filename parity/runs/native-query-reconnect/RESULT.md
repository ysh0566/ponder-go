# PostgreSQL live-query listener recovery

Go 1.25.11, real HTTP/SSE and PostgreSQL 14.24. The new regression failed on the main tree at c78262a5 with “subscription ended during the outage”. The test creates a dedicated database, temporarily refuses new connections to that database and terminates only the targeted listener or reader. [Reproduction on the prior version](../native-query-deployment-audit/RESULT.md) · [Regression test](../../../query/notification_reconnect_test.go)

1. A query server subscribed to an external writer receives the first inserted row through one HTTP stream.
2. With the listener disconnected and new connections refused, two external commits are missed by LISTEN. Two independent SQL requests see the respective current table contents. A second query server opens against the existing store and waits for its first LISTEN. Closing another pending subscription cancels its retry.
3. Restoring connections wakes the original HTTP stream with all three rows. The second server establishes its initial subscription. A subsequent notification on the restored listener delivers the fourth row on the original stream.
4. With the notification session still connected, the test holds a table lock and terminates the SQL backend running the stream's pending SELECT. Retrying the read delivers the fifth row after the writer commits, without a new notification. Removing the table produces a permanent query error and ends the stream.

The 250 ms retry applies to transient PostgreSQL connection errors after the store is open. Existing subscriptions refresh the latest committed query result after reconnecting. PostgreSQL notifications are ephemeral, so intermediate states during the outage can be coalesced. The PGlite notification transport uses its embedded engine process. [Pinned upstream listener](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/client/index.ts#L239-L345)

## Verification

The complete `query` package and the selected store tests ran in ordinary and race modes. Both covered PostgreSQL and PGlite. The PostgreSQL reconnect regression also ran three consecutive times in each mode. All packages compiled and passed vet.

```sh
go test -count=1 ./query
go test -race -count=1 ./query
go test -count=1 ./store -run 'Test(DatabaseNotifications|LiveQuery|PostgresStore|PGliteStore)'
go test -race -count=1 ./store -run 'Test(DatabaseNotifications|LiveQuery|PostgresStore|PGliteStore)'
go test -run '^$' ./...
go vet ./...
```
