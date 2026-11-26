# Historical SQL query size in the dual-store Go runtime

A Go indexer can set `RuntimeOptions.Historical.QuerySize` for default multichain, Isolated and bounded finalized Omnichain on either PostgreSQL or PGlite. Zero uses the default budget of 12000. The accepted explicit range is 1 to 12000.

Default multichain and Omnichain use `round(budget / (chains + 1)) + 6` rows per initial query. An Isolated worker starts with the integer quotient of the budget and chain count, then applies that formula with one local chain. The two worker paths in Ponder 0.18.2 divide the budget before constructing the single-chain indexing build. [Historical queries](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/runtime/historical.ts#L218-L225) · [Isolated controller](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/bin/isolatedController.ts#L151-L166) · [Isolated worker](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/bin/isolatedWorker.ts#L132-L150)

For default multichain and Isolated, the runner reads one finalized block at a time. It also reads the newly finalized blocks it persists during live polling. Each cached event-table query starts with the derived row limit. If a single block fills the query, the sync store doubles the limit and re-reads until it can decode the whole block. A chain with RPC caching disabled decodes its acquired data in memory. [SQL expansion](../../../store/source_query.go) · [Historical block acquisition](../../../historical/live_runtime_completion.go) · [Finalization during live polling](../../../historical/live_runtime_continuous.go)

## Verification

The two-chain regression puts 21 ABI logs in block 1 of each chain. Chain 1 uses the SQL cache and chain 2 uses in-memory decoding. With a budget of 12, the callback fails after writing log index 13 on chain 1. After closing both stores, the test verifies that chain 1 has zero committed rows. Reopening with the same build, a fresh opening ID and a budget of 1 produces all 42 expected primary keys and decoded values. The pre-change runtime rejected the first opening for both orderings. [Regression and rounding cases](../../../historical/runtime_historical_query_size_test.go)

| Run on a dedicated PostgreSQL 14.24 server | Observed row limits bound on the cached log SELECT |
| --- | --- |
| Initial default multichain, budget 12 | 10, 20, 40 |
| Initial Isolated, budget 12 | 9, 18, 36 |
| Reopening either ordering, budget 1 | 6, 12, 24 |

The server logged these event queries for the cached chain. The uncached chain produced its 21 rows from the RPC fixture.

The dense-block recovery case passed for both orderings on both engines in ordinary and race runs. The first PGlite race run lost its bridge process under host memory pressure. Repeating that run with one Go scheduler thread and fresh data directories passed both orderings. PostgreSQL regression controls also passed for all five bounded Omnichain static-log scenarios, Isolated live reorgs and default multichain retained finality.

Run the following with Go 1.25.11, an explicit task-owned PostgreSQL database, the PGlite bridge and fresh absolute test roots for both engines.

```sh
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 -run '^(TestHistoricalQueryLimit|TestRuntimeHistoricalQuerySizeDenseBlock|TestRuntimeOmnichainStaticLogsCapabilities|TestRuntimeRejectsHistoricalOptions)$' ./historical
done
```

Six original pagination and filter-range cases passed on pinned Ponder 0.18.2. The Go recovery test is additional native coverage. The inventory retains its existing 883 Go source-case mappings.

```sh
cd ../ponder-upstream/packages/core
node_modules/.bin/vitest run --maxWorkers=1 --no-file-parallelism src/runtime/historical.test.ts src/sync-store/index.test.ts -t 'getLocalEventGenerator\(\) pagination|getEventBlockData\(\) pagination|getEventData\(\) applies one block range'
```
