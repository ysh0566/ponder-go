# Go notifications for committed event groups

The independent business/sync runner exposes `RuntimeOptions.OnCommitted` across its supported execution modes. Each notification identifies one chain's group, the business operation, the physical head and whether the durable recovery position already covers that group. Executing a replacement branch or replaying after reopen creates new operation IDs. Delivery runs outside the business transaction. A crash between commit and delivery can interrupt notification.

The test fixtures use fresh PostgreSQL 14.24 and PGlite stores. Default bounded indexing stops from the observer after a live-tail commit and reopens the same deployment. The continuous default case checks all eight physical heads across two openings. The Isolated fork fixture compares the original and replacement block hashes, peer progress and business rows. The bounded Omnichain fixture compares group metadata against the source event sequence and collects errors from both observers before stopping. [Default bounded case](../../../historical/runtime_group_notifications_test.go) · [Continuous replay](../../../historical/live_runtime_continuous_test.go) · [Isolated reorg](../../../historical/isolated_live_test.go) · [Omnichain groups](../../../historical/runtime_commit_observer_test.go)

## Native verification

The ordinary and race commands use Go 1.25.11, a fresh PostgreSQL 14.24 instance and the workspace PGlite bridge. The final ordinary selection used a disposable 256 MiB tmpfs for PostgreSQL data after the container disk filled during an earlier database initialization. The bounded default test also checks that the observer can read the committed control, that closing recursively returns the busy error, and that failed delivery leaves one persisted balance change per event after cold replay. The receipt-level test exercises a confirmed lost commit acknowledgement and suppression of an already-applied operation.

```sh
selection='^Test(RuntimeGroupNotifications|RuntimeCommitReceipt|RuntimeOmnichainCommitObserver|RuntimeContinuousFutureEnd|RuntimeIsolatedLiveReorg)$'
go test -p 1 -count=1 -timeout=40m -run "$selection" ./historical
integration='^Test(RuntimeGroupNotifications|RuntimeOmnichainCommitObserver|RuntimeContinuousFutureEnd|RuntimeIsolatedLiveReorg)$'
go test -p 1 -race -count=1 -timeout=40m -run "$integration" ./historical
go test -p 1 -race -count=1 -run '^TestRuntimeCommitReceipt$' ./historical
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

All 11 historical package cases passed in ordinary mode and with the race detector, with zero skips. The race selection ran the ten integration cases and the receipt case separately. All 50 Go packages compiled and passed vet. Verification tooling passed 499 Node cases, four additional loopback cases and 43 Python cases. Module-wide ordinary and race regression results for this revision are pending.

## Pinned runtime semantics

In `packages/core` at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, the 14 historical generator tests passed and the realtime selection passed four reorg and finalization tests. Vitest filtered 26 unrelated realtime cases. The public callback is a Go API.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/historical.test.ts
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/sync-realtime/index.test.ts -t 'handleReorg|finalize event'
```

## Go indexer scope check

With live polling disabled, the bounded default runtime advances to the available end height. Its block-acquisition path checks parent hashes and returns an error on a fork in the unfinalized range. Reopening the deployment runs durable recovery. With live polling enabled, the runner hands the historical cursor to its continuous reorg driver. [Bounded path](../../../historical/live_runtime_completion.go) · [Continuous driver](../../../historical/live_runtime_continuous.go)

The Go model generator resolves built-in columns, arrays and schema enums. The schema and generator have no extension point for an application-defined column codec. The default Go Prometheus registry also lacks the pinned upstream `ponder_historical_extract_duration` and `ponder_historical_transform_duration` families. The four existing metrics package cases passed with `go test -p 1 -count=1 ./metrics` and with `-race`. [Column validation](../../../schema/schema.go) · [Model generation](../../../orm/generate.go) · [Go registry](../../../metrics/registry.go) · [Pinned metrics](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts)
