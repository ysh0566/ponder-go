# PGlite request cancellation

Queries, writes, transaction starts and notification subscriptions waiting for the single PGlite connection return when their Go context ends. Concurrent operations in a transaction serialize their requests and responses. A canceled waiter leaves its SQL unsent and the owner can still commit. A final context check covers cancellation during parameter encoding. After sending a statement, the driver waits for the real engine response. Commit and rollback release the connection after that response. [Driver](../../../store/backend.go) · [Notification subscriptions](../../../store/notifications.go) · [Usage](../../../docs/USAGE.md#使用本地-pglite)

The regression test holds a real PGlite transaction open, cancels each kind of waiting operation, then commits two rows and verifies exactly those rows. It subscribes to the same notification channel afterwards and receives a new notification. The transaction test pauses argument encoding before dispatch, expires another statement and checks both a successful commit and a canceled commit. Another case cancels the context immediately after the real COMMIT frame is written and checks the committed table using the same connection. On the parent revision, a waiting query stayed blocked and a waiting write exceeded its deadline. [Native tests](../../../store/pglite_context_test.go)

## Verification

Use the Go repository’s PGlite dependency and Node test prerequisites. Run the following from the Go checkout.

```sh
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 -run '^(TestPGliteQueuedCancellation|TestPGliteTransactionQueue|TestPGliteSubmittedCommit|TestPGliteCancelledCommit|TestPGliteStore|TestBridgeStartupReportsProcessFailure)$' ./store
  go test $mode -p 1 -count=1 -run '^(TestDatabaseNotifications|TestUpstreamLiveQueryNotify|TestDatabaseEngineRecovery)$/pglite' ./store
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The pinned upstream `createPglite` factory opens the actual PGlite 0.2.13 memory database. With a 5 ms PostgreSQL statement timeout, its CPU-bound sum over 1,500,000 values still returned the complete result. This probe documents the remaining in-flight limit on the tested engine. Run it from the upstream `packages/core` directory with `node_modules/.bin/tsx --tsconfig tsconfig.json <go-checkout>/parity/runs/native-pglite-queued-cancel/oracle.cjs`. [Upstream probe](oracle.cjs) · [Pinned factory](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/utils/pglite.ts)

All 25 selected Go cases passed in ordinary mode and all 25 passed with the race detector. The selection includes the PGlite store, engine interruption and reopen, commit cancellation, subscription cancellation, shared listeners, and the original large-payload notification scenario. All 51 Go packages compiled and passed vet. The repository verification tools passed 503 Node cases and 43 Python cases. Full-module ordinary and race regressions are tracked separately.

The upstream probe returned the full sum in 628 ms. Its first concurrent run exited under shared-worker memory pressure and the separate run completed. The original Vitest case `live query notify trigger batches large payloads` also passed on PGlite. The name filter selected one of the eleven database action cases. [Original notification test](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/database/actions.test.ts#L185)
