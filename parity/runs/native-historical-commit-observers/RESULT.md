# Historical commit notifications and Omnichain timing

Bounded Isolated and Omnichain Go jobs accept `RuntimeOptions.Historical.OnCommitted` on both dual-store constructors. Their batch transaction commits the business writes and the safe cursor together. The notification runs after that transaction returns. One Omnichain transaction can produce several per-chain notifications. If any observer fails, the others in the same transaction still receive their committed groups before the runner stops. A cold opening resumes after the durable checkpoints. The observer has no durable delivery log. [Usage](../../../docs/USAGE.md#接收双库回填的提交通知)

`GroupResult` includes the source group range, event count, checkpoint, execution attempts and commit reconciliation status. An exact receipt can confirm a lost database acknowledgement. An unknown outcome returns an error. PostgreSQL and PGlite use the same result fields.

A custom Omnichain `Clock` supplies time to the historical coordinator and to local range planning. Header timestamps and their derived checkpoints come from the chains. The pinned historical reference is Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. All 14 tests in the original historical runtime file passed using PostgreSQL 14.24.

```sh
cd packages/core
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/historical.test.ts
```

## Test scenarios

- The Omnichain job uses the fixed two-chain block and log fixture. Both observers can read their committed safe checkpoints. They return separate errors for the same transaction, and the runner returns both. Reopening twice preserves the original 38 ordered callbacks and final business rows. On the resumed run, advancing the injected clock by one minute triggers one additional head read per chain.
- The Isolated job includes genesis, a cached chain, an uncached peer and account transaction callbacks. A block handler requests an extra column. Its notification counts both executions after refetch. A later rejected callback rolls back without a notification. Retry and cold reopen deliver exactly one notification per committed block.
- Database tests inject loss of the COMMIT acknowledgement before commit, after commit and during receipt readback. The two backends confirm reconciliation only for the committed operation. An exact repeat avoids running the handler again.
- Constructor admission rejects a historical clock on other orderings and an observer on live Isolated or default multichain. Setup and readiness hooks retain their separate boundaries.

## Verification commands

Run from the Go module with Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and the PGlite bridge configured.

```sh
selection='^(TestRuntimeOmnichainCommitObserver|TestRuntimeIsolatedFinalized|TestRuntimeRejectsHistoricalOptions|TestRuntimeOmnichainPreflightBoundary)$'
go test -p 1 -count=1 -timeout=35m -v -run "$selection" ./historical
go test -p 1 -count=1 -timeout=20m -v -run '^TestManagedLiveDatabaseClosure$/(postgres|pglite)/batch_ack' ./store
go test -p 1 -count=1 -timeout=35m -v -race -run "$selection" ./historical
go test -p 1 -count=1 -timeout=20m -v -race -run '^TestManagedLiveDatabaseClosure$/(postgres|pglite)/batch_ack' ./store
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

During test development, an account source projected the same block column that the block handler requested. Narrowing every source in the fixture to its required fields made the refetch observable. Two intermediate PostgreSQL probes could not start while the workspace disk was full. Completed test fixtures were archived and checked against their originals before repeating the checks with fresh database namespaces and directories.

The final ordinary selection and the race selection each passed 26 leaf tests with zero skips. All 50 Go packages compiled and passed vet. Repository checks passed 503 Node.js and 43 Python tests. The module-wide ordinary and race regressions use a separate submission for this revision and are pending.
