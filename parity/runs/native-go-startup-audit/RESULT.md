# Go indexer startup and remaining work

This audit uses the Go product at `58b7d1f765c3e922dd0a43e629f707d3f5dbfcb8` and Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. The application is written in Go and uses the current database format for its runtime.

## When one of two chains is offline

| Go entry point or phase | Observed behavior |
| --- | --- |
| Single store, `runtime.NewIndexer` with Isolated ordering | With the second chain returning HTTP 503 from startup, the first chain committed its first block. Deployment readiness became true after both chains caught up. |
| Preparing `config.BuildSourceIndexing` | The unavailable first endpoint returned HTTP 503. The build returned an error before any callbacks ran. Both the single-store `NewIndexerFromSource` and the dual-store runner consume a compiled build. The builder requests heads for all configured chains before it filters chains by registered callbacks. |
| Dual-store Isolated, opening with a previously compiled build | Both the initial open and the cold reopen returned HTTP 503 with no runner. The healthy chain's callback count stayed at zero on the initial attempt and at five on the reopen. The same observations held for PostgreSQL and PGlite. |
| Dual-store Isolated, outage after startup | The healthy chain committed block 4 while the peer's gateway was returning HTTP 503. When the gateway returned a replacement branch, only the affected chain replayed block 2. The final ten rows had canonical hashes. |
| Dual-store Omnichain | Preflight rejects live polling and unbounded sources. A finite job can wait until every source fits the global finalized range. Durable live handoff, cross-chain rollback and restart during unfinalized work remain open. |

The direct single-store test uses two Anvil nodes. The dual-store test uses two controlled JSON-RPC chains, a switchable HTTP gateway and real databases. The startup probe sets client retries to zero so a persistent outage is observable on the first failed attempt. [Atomic workers](../../../runtime/isolated_indexer_test.go) · [Source builder](../../../config/source_indexing.go) · [Dual-store open](../../../historical/live_runtime.go) · [Omnichain preflight controls](../../../historical/live_runtime_omnichain_source_test.go)

## Other open work

- The writer exposes 15 of the 39 explicitly named upstream metric families. The 24 missing names cover database operations (3), historical sync (9), indexing (6), chain sync (4) and process information (2). [Metric groups](../../../docs/RUNTIME_SUPPORT.md#prometheus-coverage)
- Of eight simulation applications, `feature-multichain` and `reference-erc20` have checked-in historical table comparisons. The other six need reference data and Go replays for comparison. The two existing comparisons still need live fault and restart runs. Both rETH and Uniswap need comparable benchmark measurements. [Application inventory and runs](../../../docs/RUNTIME_SUPPORT.md#application-verification)
- Production verification remains open for host power loss, further PGlite interruption scenarios, database interruption during mixed-source dual-store reorgs and separate writer/deployment-manager privileges on the dual-store PostgreSQL runtime. [Recovery and role coverage](../../../docs/RUNTIME_SUPPORT.md)
- The Go ledger records 883 executed upstream mappings, with 866 passes and 17 provider failures. Sixteen depend on gateway policies, and one requires 58 canonical Swell logs. The local fault-injection probes in this report add no new upstream mappings. [Per-case results](../../test-ledger.json) · [Provider responses](../provider-policy-assertions/RESULT.md)

The 16 upstream starter menu entries have Go equivalents. All 21 named callback RPC actions have high-level Go methods. Generated Go columns support chained inner and left equality joins, including three-table approval queries. [Go templates](../../../docs/USAGE.md#选择-go-项目模板) · [Callback actions](../../../docs/RUNTIME_SUPPORT.md#callback-rpc-methods) · [Join verification](../native-chained-joins/RESULT.md)

## Reproduction

In a disposable checkout of the audited product revision, apply the [audit probe](opening.patch) to the existing live outage test. The patch adds observations at source compilation, fresh open and cold reopen. Configure Go 1.25.11, Anvil 1.8.5, a private PostgreSQL 14.24 test database and the PGlite 0.2.13 bridge. Set `PONDER_M45_TEST_ROOT` to a fresh absolute directory. Run from the repository root.

```sh
git apply --unidiff-zero /path/to/opening.patch
selection='^(TestIsolatedIndexer|TestRuntimeIsolatedLiveOutage|TestRuntimeOmnichainPreflightBoundary)$'
go test -p 1 -count=1 -v -timeout 20m -run "$selection" ./historical ./runtime
go test -race -p 1 -count=1 -v -timeout 20m -run "$selection" ./historical ./runtime
```

The three selected Go tests passed all 14 leaf cases in ordinary mode and all 14 with the race detector. Four original controls passed on the pinned upstream revision. The following command selects them from `packages/core`.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/database/actions.test.ts src/sync-realtime/index.test.ts -t 'commitBlock\(\) isolated|handleReorg\('
```

Repository verification passed 503 Node and 43 Python checks. These are focused results on the audited Go revision. The module-wide regressions and the 17 provider cases have separate execution records.
