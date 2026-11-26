# Empty and block-driven Go projects

`ponder init --template empty my-indexer` generates an editable Go project with an empty ABI, an `ExampleContract` source and a table with a text primary key and an optional name. The Go handler registry starts empty. The network, placeholder address, start height and schema follow the pinned upstream empty project.

`ponder init --template blocks my-prices` generates the Chainlink price example. Its Ethereum mainnet block source starts at 19,750,000 and runs every five blocks. The Go handler calls `latestAnswer()` on `0xD10aBbC76679a20055E167BB80A24ac851b37056` at the event's block height, decodes a signed 256-bit integer, divides by 10⁸ and inserts the price under the block timestamp. Both templates reuse the existing Go application's startup, database and query code.

## Generated project checks

The native test checks the default network settings before configuring the fixture. Each generated project starts through the Go CLI, reaches HTTP readiness, and answers SQL queries on both PostgreSQL 14.24 and PGlite 0.2.13.

A local Anvil 1.8.5 chain mines 17 blocks before indexing begins. The test sets the inclusive source range to 2–12. The empty project exposes an empty `example` table. The block project reads a local oracle that accepts only the `latestAnswer()` selector and returns `(block.number - 4) * 12500000`. Querying all rows yields the following results.

| Block | Timestamp | Price |
| --- | --- | --- |
| 2 | 1700000002 | -0.25 |
| 7 | 1700000007 | 0.375 |
| 12 | 1700000012 | 1 |

This covers the source offset, interval, inclusive end, signed decoding, price scale and historical reads while the chain head is at block 17. Public mainnet oracle history was not replayed.

## Reproduction

With the project's Anvil and integration database environment configured, run the following commands from the Go repository. The race selection instruments the generated Go applications as well as the test process.

```sh
selection='^Test(GoEmptyAndBlocksTemplates|InitGoProject|CreateEmptyProject|CreateProjectConcurrent|GoProjectTemplates|GoAccountsTemplate)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder
go test -race -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

At upstream revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, the 34 runtime filter and event tests passed. The create-ponder empty-project test passed in a scratch copy with the pinned package and lockfile. Starting Vitest through pnpm supplies the package-manager identity expected by that test.

```sh
# packages/core
node_modules/.bin/vitest run --maxWorkers=1 src/runtime/filter.test.ts src/runtime/events.test.ts
# packages/create-ponder
pnpm run test --maxWorkers=1 src/_test/cli.test.ts
```

The Go selections passed without skips in ordinary and race mode, including the existing token, account, proxy and factory template checks. All packages compiled and vet passed. Verification utilities passed 503 Node and 43 Python cases. These native project checks do not change the upstream case count. Whole-module ordinary and race execution uses the separate serialized regression queue.
