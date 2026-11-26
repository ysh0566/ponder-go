# Four native Go indexer templates

`ponder init --template erc721|erc1155|erc4626|factory <directory>` generates a Go project with handlers, ABI, schema and chain configuration. The ERC-20 starter is available through the default, `go` and `erc20`. The four new projects share the ERC-20 application's database, CLI and query startup code.

The reference chain ID, name, deployment address and inclusive block bounds were compared to the four example configurations in Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. All nine selected ABI events matched their original argument types, names, ordering and indexed flags. The Llama factory's creation event and its two child-address parameters were checked separately.

## Native integration

Each generated project starts through the Go CLI and uses a local Anvil chain with its reference chain ID. The test deploys its event fixture at block 1, retains the reference chain name and range mode, and substitutes the local deployment address and bounds. It checks readiness and queries rows through the public HTTP SQL endpoint. The end-to-end results cover local fixtures. Public chain history remains unverified for these four projects.

| Template | Database assertions on PostgreSQL and PGlite |
| --- | --- |
| ERC-721 | Two transfers of the same 129-bit token ID, the final owner, three account records and the ordered transfer history. |
| ERC-1155 | One single transfer and a batch containing a repeated token ID. Checks balances on both sides of the transfers and all four event rows, including each batch index. |
| ERC-4626 | Share balances, two grants for the same spender, the resulting allowance, transfers, deposits and withdrawals. A first-seen sender starts at zero as in the reference handler. |
| Factory | One creation log registers a Core and a Policy address. Both children and an unrelated contract emit both events in the same transaction. Only Core's ActionCreated and Policy's Initialized appear in the query results. The Go starter persists the event name, log ID and emitting contract. |

The local fixture was recompiled with Solidity 0.8.37 and all four creation bytecodes matched the checked-in fixture. Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13 were used for the integration runs.

```sh
solc --optimize --evm-version paris --metadata-hash none --combined-json bin cmd/ponder/testdata/templates/Events.sol
selection='^Test(GoProjectTemplates|InitGoProject|CreateEmptyProject|CreateProjectConcurrent|GeneratedGoServe|GeneratedGoStartViews|GeneratedLocalPGlite|GeneratedDevTerminal|GeneratedDeploymentRoles)$'
go test -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder
templates='^Test(GoProjectTemplates|InitGoProject|CreateEmptyProject|CreateProjectConcurrent|GeneratedGoServe|GeneratedGoStartViews)$'
go test -race -v -count=1 -timeout=45m -run "$templates" ./cmd/ponder
go test -v -count=1 -timeout=45m -run '^TestGeneratedDeploymentRoles$' ./cmd/ponder
go test -race -v -count=1 -timeout=45m -run '^TestGeneratedDeploymentRoles$' ./cmd/ponder
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

All eight template and backend combinations passed in ordinary and race runs. The race build includes instrumentation in the generated indexers. An initial run encountered two timing issues. The template test now mines explicitly before reading the deployment receipt. The role test retries a temporary transaction lock while requiring the active writer result. A low-disk concurrent attempt was rerun in smaller selections.

## Pinned upstream checks

The six factory builder cases, the factory end-to-end test and the create-project CLI file test passed. These eight upstream checks are independent of the eight new template/backend combinations.

```sh
# packages/core
vitest run src/build/factory.test.ts
vitest run src/_test/e2e/factory/factory.test.ts
# packages/create-ponder
vitest run src/_test/cli.test.ts
```

The verification tools passed 503 Node tests, 40 Python tests and three observer tests. The existing upstream inventory records 883 cases ported to Go, with 866 passing and 17 provider RPC failures. Full-module ordinary and race regressions for this revision are pending.
