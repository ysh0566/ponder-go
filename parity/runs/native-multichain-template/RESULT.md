# Go template for multichain WETH deposits

`ponder init --template multichain my-weth` generates an Isolated indexer configured for Ethereum (1), Base (8453), Optimism (10) and Polygon (137). Each chain pins its own inclusive starting height. The source configuration, addresses, Deposit ABI, event handler and composite account key follow the feature-multichain example at Ponder 0.18.2 revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. The business value is the sum of indexed deposits in wei. [Go usage guide](../../../docs/USAGE.md#选择-go-项目模板)

## Local chain verification

The CLI generates the project and launches it against independent Anvil 1.8.5 nodes with the four reference chain IDs. Each node uses a fixture contract at its reference address and another copy at an unrelated address. Different starting heads, the same depositor across chains and initial amounts above 64 bits exercise the deployment's per-chain state. The test enables the per-chain WebSocket URLs and sets unusable single-chain overrides.

* On initial catch-up, SQL and GraphQL return one business row for each chain. Events before each first head are excluded. An Optimism deposit arrives while the indexer is running and changes its balance.
* After shutdown, the test mines deposits at watched and unrelated addresses on every chain. The next process uses the original database and includes only the watched deposits. A second Polygon depositor exercises the address part of the composite key.
* After another shutdown, the test replaces the Base branch. On reopening, the replacement deposit changes only the Base balance. The status endpoint reports all four chain names and heights, and GraphQL works at both supported paths.
* On PostgreSQL a separate `ponder serve` process reads the same balances and chain statuses after the writer exits, with the RPC and WebSocket variables cleared.

The tests exercise PostgreSQL 14.24 and PGlite 0.2.13. They use local event data and do not replay public chains. The fixture contains only a payable deposit method. Regenerate its runtime bytecode with solc 0.8.37.

```sh
solc --optimize --evm-version paris --metadata-hash none --bin-runtime cmd/ponder/testdata/templates/WrappedDeposits.sol
```

## Reproduction

From the Go repository with Go 1.25.11, Anvil and the task database test environment, run these selections. The race selection also instruments the generated programs.

```sh
selection='^Test(GoMultichainTemplate|GoFilterAndAPITemplates|GoEmptyAndBlocksTemplates|GoContractTemplates|GoProjectTemplates|GoAccountsTemplate|InitGoProject|CreateProjectConcurrent|CreateEmptyProject|GeneratedGoServe|GeneratedGoStartViews|IsolatedIndexer|IsolatedBoundedCompletion|HeadNotifications)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder ./runtime
go test -race -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder ./runtime
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The pinned upstream run used `node_modules/.bin/vitest run --maxWorkers=1 src/build/config.test.ts src/build/schema.test.ts src/runtime/events.test.ts` from packages/core. All three files passed, with 74 passing tests and one source-declared skip for a public RPC URL warning. Network IDs, addresses, RPC and WebSocket environment variable names, start tags, ordering and the Deposit ABI were also compared against the actual upstream example.

The selected Go CLI and runtime tests passed in both ordinary and race mode with zero skips. All packages compiled and vet passed. The repository tooling passed 503 Node.js tests and 43 Python tests. Whole-module Go execution is tracked separately.
