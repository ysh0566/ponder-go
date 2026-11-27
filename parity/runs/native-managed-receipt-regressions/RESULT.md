# Managed write errors and transaction receipt verification

The managed live store now checks ownership at the public entry points for atomic callbacks and block completion. A caller attempting either operation receives the managed-mutation error, the cancellation error, the lost-lease error or the closed-owner error for its state. This check precedes handler validation and any database work. An ordinary atomic store still requires a handler for `Apply`. Empty block completion continues to use its own entry point. [Store implementation](../../../store/store.go) · [Guard test](../../../store/managed_live_test.go)

The native historical composition test dispatches six events from two chains in a caller-owned transaction. It verifies that an aborted attempt leaves no business rows, journal, receipt or checkpoint. For the committed attempt it checks the six business rows, six ordered journal entries, their chain and checkpoint, the receipt and final progress. The default multichain writer reserves a sequence position on each side of the executed callback. The receipt check now counts actual journal entries strictly between those positions. A PostgreSQL negative control narrowed the interval to exclude the last entry and the assertion failed with five operations. [Historical composition](../../../runtime/source_indexing_service_integration_test.go) · [Cross-chain suffix replay](../../../store/multichain_receipt_test.go)

The original mutable-value case increments a number inside a JSON object and checks nested objects, arrays, byte buffers and dates after mutating earlier snapshots. The Go fixture now reads that integer with `json.Number.Int64()`. The native JSON codec preserves the decimal representation of incoming numbers. The old callback cast panicked on PostgreSQL, and an earlier full run also recorded the panic on PGlite. [Go fixture](../../../store/orm_upstream_test.go) · [Column normalization](../../../store/column_values.go) · [Pinned original case](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/indexing-store/index.test.ts#L135-L249)

## Verification

The original guard and receipt failures were reproduced on `190a3d7f`. The former returned the missing-handler error for a managed live store. In the latter, both six-row checks passed and the receipt sequence difference was seven. An overlay using the parent version of the mutable-value test reproduced its numeric type assertion panic.

Run with the project's private PostgreSQL and PGlite test fixtures.

```sh
for mode in '' '-race'; do
  go test $mode -count=1 -p 1 -run '^Test(ManagedLiveLegacyMutationGuard|MultichainPartialPeerBlock|PostgresStore|PGliteStore|UpstreamORM|ORMJSONNullCharacterUndo)$' ./store
  go test $mode -count=1 -p 1 -run '^TestSourceHistoricalIndexingServiceComposition$' ./runtime
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The source comparisons use Vitest in the pinned upstream `packages/core` directory, a single worker and two source aliases. The aliases resolve `@` to `packages/core/src` and `@ponder/utils` to `packages/utils/src/index.ts`. The PostgreSQL run uses a dedicated cluster and the PGlite run uses the original in-memory setup.

All 33 selected native cases passed in ordinary mode and all 33 passed under the race detector with both databases configured. An initial race run of the broader ORM group lost PGlite subprocesses during cgroup out-of-memory kills. The entire group passed when rerun serially. On PostgreSQL the pinned indexing-store file passed all 29 active tests and retained its three original skips for time, date and interval. On PGlite the selected original mutable-value test passed. The repository checks passed 503 Node and 43 Python tests, all 51 Go packages compiled and `go vet` passed. The full-module ordinary and race runs are tracked separately.
