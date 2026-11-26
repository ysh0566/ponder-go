# Reorg counts for Go indexers

The Go writer exports `ponder_realtime_reorg_total{chain="…"}` after it successfully rolls back indexed blocks on a forked chain. Ponder 0.18.2 declares the counter with this name and chain label, and clears it on reset. A search of the pinned core source found the type, declaration and reset. The count assertions below exercise the Go writer's behavior. [Upstream registry](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L344-L350) · [Go usage](../../../docs/USAGE.md#查看发生分叉的链)

The atomic indexer test starts Anvil nodes, indexes factory, log, CALL, transfer and transaction events, takes both RPC endpoints for chain A offline and brings a node back with a replacement branch. It scrapes the writer at catch-up, during the outage, after replay, and after reopening the same database with a fresh registry. A contributes one count. B's dependent callbacks replay in the global orderings. Isolated preserves B's callbacks. In all three cases only A has a sample. [HTTP assertions](../../../runtime/mixed_source_reorg_test.go)

The independent-store tests cover two consecutive forks, groups that make no business writes, a fork at the end of a bounded run, a different parent, interruption during replay, and rejection at finality. Isolated tests also interrupt a durable reorg intent. The counter belongs to the process that receives the successful coordinator result. Recovering a pending operation during open can therefore leave the counter empty even though the database records the rollback. [Continuous](../../../historical/live_runtime_reorg_test.go) · [Bounded](../../../historical/live_runtime_bounded_reorg_test.go) · [Isolated](../../../historical/isolated_live_test.go)

The upstream registry defines 39 metrics with an explicit `ponder_` name. Comparing those names with the Go registry after this change yields 12 matches. The 27 additional names include realtime block arrival and processing latency, database method timing and errors, sync progress and indexing measurements. This comparison counts metric families. Labels, units and sample behavior require separate checks. [Upstream definitions](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts) · [Go registry](../../../metrics/registry.go)

## Reproduction

Run in the Go repository with Go 1.25.11, Anvil 1.8.5, a dedicated PostgreSQL 14.24 test database and the PGlite 0.2.13 bridge. Set `PONDER_M45_TEST_ROOT` to a fresh absolute directory for retained independent-store fixtures.

```sh
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 ./metrics
  go test $flags -p 1 -count=1 -run '^TestMixedSourceProviderReorg$' ./runtime
  go test $flags -p 1 -count=1 -run '^TestRuntime(ContinuousReorg|BoundedReorg|IsolatedLiveReorg)$' ./historical
done
```

From `packages/core` in the pinned upstream checkout, both selections passed. The second filter selects three reorg tests and excludes 27 unrelated declarations.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/internal/metrics.test.ts
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/sync-realtime/index.test.ts -t 'handleReorg\('
```

Go 1.25.11 passed the registry package in ordinary and race modes. The initial backend verification passed all three application orderings and the eight independent-store scenarios on PostgreSQL in each mode. PGlite passed all eleven scenarios in ordinary mode, and the three application orderings also passed with the race detector. The initial combined PGlite process was killed under resource pressure. The individual selections used precompiled test binaries, one retained directory per run and temporary storage with sufficient space. The pull request review records the final race selection on this revision.

The whole-module compile check and static analysis passed with `go test -p 1 -run '^$' ./...` and `go vet -p 1 ./...`. The tooling regression passed 503 Node and 43 Python tests. These are separate from the module-wide ordinary and race regressions and the existing 883 mapped upstream cases.
