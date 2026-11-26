# Native Go indexer scope audit

The comparison uses Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` and the Go product tree on `main` at `9fa1b8e173ea461c7dc4595c184a3412a7041d5a`. It covers applications written in Go using the current database format for the selected runtime.

## Runtime behavior

| Entry point | Continuous indexing | Remaining work |
| --- | --- | --- |
| `runtime.NewIndexer` and `runtime.NewIndexerFromSource` | Default multichain, Isolated and Omnichain support backfill, live blocks and reorg recovery. | Exercise database engine failure while replacing a branch with mixed factory, contract and account events. [Mixed-source reorgs](../native-mixed-source-reorg/RESULT.md) · [Engine failures during block callbacks](../native-multichain-engine-recovery/RESULT.md) |
| Default multichain with separate business and sync stores | Live polling and coordinated rollback are available. | Extend fault coverage across source combinations and RPC outages. [Coordinated recovery](../native-coordinated-engine-recovery/RESULT.md) |
| Isolated with separate stores | Each chain can initialize and backfill a finite range up to its own finalized head. | Follow unfinalized heads, undo only the forked chain's writes and reconcile both stores on restart while preserving peer progress. [Admission checks](../../../historical/isolated_finalized_test.go) · [Per-chain setup](../native-isolated-setup/RESULT.md) |
| Omnichain with separate stores | All configured ranges must fit within a common finalized checkpoint. | Continue across the history-to-live boundary, roll back the affected global suffix on a fork, and replay dependent peer callbacks in checkpoint order. [Admission checks](../../../historical/live_runtime_omnichain_source_test.go) · [Finality checks](../../../historical/live_runtime_omnichain.go) |

PostgreSQL coverage for writer and deployment-manager roles applies to the native deployment. Equivalent least-privilege tests for the separate business and sync stores remain open. Additional recovery work includes host power loss, engine failure during dual-store Omnichain setup or activation, and further PGlite interruption scenarios. The public-chain templates also need historical replay against the matching archive data. [Deployment roles](../native-deployment-roles/RESULT.md) · [Omnichain engine recovery](../native-omnichain-engine-recovery/RESULT.md) · [Template evidence](../native-reference-erc20/RESULT.md)

## Source inventory corrections

- **End-to-end indexing.** The two pinned declarations map to `TestSourceApplications/erc20` and `TestSourceApplications/factory`. The tests deploy contracts on Anvil, run Go handlers and query the resulting rows over HTTP on PostgreSQL and PGlite. Both original declarations and both Go mappings have passing records. [Go tests](../../../runtime/application_source_test.go) · [Per-case results](../../test-ledger.json)
- **Simulation and performance.** The eight simulation applications use seeds and reference tables. The Go simulation harness, datasets and seeded result comparisons remain open. The indexing benchmark contains separate rETH and Uniswap workloads. Comparable upstream and Go runs on the same data, RPC configuration and database remain open. [Discovery inventory](../../discovery-audit.json) · [Upstream simulation](https://github.com/ponder-sh/ponder/tree/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/simulation-test) · [Upstream benchmarks](https://github.com/ponder-sh/ponder/tree/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/benchmark)
- **Utilities.** Go has equivalents for the five runtime exports from the upstream utilities package. The four files covering ABI merge, bigint replacement, load balancing and rate limiting contain 12 passing declarations. The provider fixtures exercising historical log requests contain 42 passing records, 17 failures and four upstream skips. Sixteen failures concern gateway limits. The Swell case expects 58 canonical historical logs. These failures are already included in the module's 883 mapped Go declarations. [Upstream exports](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/utils/src/index.ts) · [Provider observations](../provider-policy-assertions/RESULT.md) · [Ledger](../../test-ledger.json)
- **Go models.** The typed generator already supports JSON, bytes and arrays. Enum and hex columns currently generate Go string fields. [Generator](../../../orm/generate.go) · [Generated models and backend round trips](../../../orm/container_models_test.go)

## Focused verification

Run from the Go repository with Go 1.25.11, Anvil 1.8.5, the pinned upstream checkout, PostgreSQL and the local PGlite bridge configured.

```sh
selection='TestSourceApplications|TestSourceLoadBalance|TestSourceRateLimit|TestSourceValueUtilities|TestRuntimeIsolatedPreflight|TestRuntimeOmnichainPreflightBoundary'
go test -p 1 -v -count=1 -run "^($selection)$" ./runtime ./rpc ./values ./historical
go test -p 1 -v -count=1 -run '^TestGeneratedContainerModels$' ./orm
go test -p 1 -race -v -count=1 -run "^($selection|TestGeneratedContainerModels)$" ./runtime ./rpc ./values ./historical ./orm
```

The focused selection passed 37 leaf cases in ordinary mode and 37 with the race detector, with zero skips in both. These include the four application/backend combinations and the two model/backend combinations. The Isolated and Omnichain admission cases check the limits of the dual-store runner before it opens storage.

The separate module-wide ordinary run on the same product tree recorded PostgreSQL startup connection refusals in the deployment-role, managed dev and managed serve CLI tests. For the targeted rerun, each disposable PostgreSQL 14.24 container used a fresh 256 MiB tmpfs for its data directory. All four selected CLI leaves passed, including all three initial failures. The fixture storage change applied only to these CLI tests. The module-wide ordinary and race results are separate from the counts above.

```sh
go test -p 1 -v -count=1 -run '^(TestGeneratedDeploymentRoles|TestManagedMultichainCLIStart|TestManagedMultichainCLIDev)$/^(postgres|postgres-serve)$' ./cmd/ponder
```
