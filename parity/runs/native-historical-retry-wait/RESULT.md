# Retry scheduling for historical PostgreSQL business transactions

This scope covers the dual-store runtime's setup and historical business transactions. A PostgreSQL serialization failure (40001) or deadlock (40P01) reuses the retained batch, original operation ID and source inputs. The source retry budget is ten attempts with nine cancellable waits beginning at 125 ms. When PostgreSQL reports a conflict at COMMIT, the store checks the lease, durable receipt and original predecessor before permitting another attempt. PGlite executes a single attempt. Unknown commit outcomes, lost connections and ordinary callback failures return to the caller.

## Evidence

The historical tests create a separate fixture schema on the task-owned PostgreSQL instance. A server-side exception raises a deadlock during a SQL statement. A deferred constraint trigger aborts a later attempt at COMMIT. The tests run actual Go callbacks and check both chains' persisted balances. The cancellation case reopens the same deployment and completes its remaining work. The PGlite case checks that a database error is returned without waiting, followed by a successful cold reopen. [Runtime tests](../../../historical/runtime_retry_wait_test.go)

The PostgreSQL statement fault is injected with PL/pgSQL, and the deferred constraint trigger makes PostgreSQL abort at COMMIT. These cases verify how the runner handles the server response and what it persists after the aborted transaction. The callback catches the statement error in the test, so successful recovery also checks the runtime's pending database error. Omnichain and Isolated group observers report three attempts for the retried batch. PostgreSQL keeps both chains' final balances at their expected values after setup or historical retries, and PGlite returns the original failure without invoking the wait hook.

### Go commands

Run from the Go repository with Go 1.25.11, PostgreSQL 14.24, the local PGlite bridge and fresh task-owned data directories.

```sh
go test -p 1 -count=1 -run '^TestRuntimeHistoricalRetry' ./historical
go test -p 1 -race -count=1 -run '^TestRuntimeHistoricalRetry' ./historical
selection='^(TestRuntimeHistoricalRetry.*|TestRuntimeRejectsHistoricalOptions|TestFiniteHistoricalRunnerRetry|TestFiniteHistoricalRunnerRetryPolicy)$'
go test -p 1 -count=1 -run "$selection" ./historical
go test -p 1 -race -count=1 -run "$selection" ./historical
go test -p 1 -count=1 -run '^TestManagedLiveDatabaseClosure$/(postgres|pglite)/batch_ack' ./store
go test -p 1 -race -count=1 -run '^TestManagedLiveDatabaseClosure$/(postgres|pglite)/batch_ack' ./store
```

The broader historical selection passed 20 leaf cases per mode before the final terminal-error regression was added. The managed-store selection passed six per mode, including failures before COMMIT, after COMMIT and during readback. The indexing checks `TestDatabaseErrorSourceClassification`, `TestDatabaseStickyOnlyRetryable` and `TestServiceSupplementalStickyErrorTiming` passed in both modes. Repository checks passed 499 Node tool and service tests, four loopback tests and 43 Python tests.

The final source passed all eight targeted cases in each mode. These include an explicitly terminal Go error carrying a PostgreSQL conflict as its cause. The full 50-package tree compiled with `go test -p 1 -run '^$' ./...`, and `go vet -p 1 ./...` passed. Whole-module ordinary and race results for this revision are pending.

### Upstream

In the pinned upstream checkout, all six tests in `packages/core/src/database/queryBuilder.test.ts` passed on a fresh PostgreSQL 14.24 instance.

```sh
cd packages/core
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/database/queryBuilder.test.ts
```
