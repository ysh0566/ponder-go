# Go projects for Friendtech trades and Uniswap V3 flash loans

Both projects are generated with `ponder init --template`, run as compiled Go programs and expose SQL and GraphQL. Their default chain IDs, monitored addresses, start heights and selected event or function ABIs follow Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. [Usage guide](../../../docs/USAGE.md#选择-go-项目模板)

## Business behavior

* Friendtech ignores zero-share trades. The first trade creates zero-valued aggregates for a new subject or trader. Trades and share positions are recorded immediately. Subsequent aggregates account for fees, whole-number prices, earnings and spend. The test includes a position above 64 bits, buy and sell events and an unrelated emitter. Both SQL and GraphQL read the event type.
* Uniswap V3 discovers pools through the factory's `PoolCreated` event. Anvil creates two pools that share a token and emits flashes in the creation block. The per-token borrowed total accumulates each loan amount. Following the reference handler, the paid total starts with the first payment and adds the loan amount on later flashes. The test chooses different amounts for payment and borrowing and includes an unrelated pool.
* The tests fill PostgreSQL and PGlite, stop each generated project, mine further events, and restart it with the original database. For Uniswap, the RPC recorder verifies that only the four immutable token addresses from the watched pools are fetched, all at latest, with no new fetch after restart. These checks use local contracts and data.

## Reproduction

Use Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13. The helper for the race selection also instruments generated Go programs. From the Go repository run the following commands.

```sh
selection='^Test(GoProtocolTemplates|GoFilterAndAPITemplates|GoMultichainTemplate|GoContractTemplates|GoProjectTemplates|InitGoProject|CreateProjectConcurrent|CreateEmptyProject)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder
go test -race -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

The Solidity fixture can be reproduced by selecting `ProtocolExamples` from `solc --optimize --evm-version paris --metadata-hash none --combined-json bin-runtime cmd/ponder/testdata/templates/ProtocolExamples.sol` with solc 0.8.37. The pinned upstream oracle runs from `packages/core` with the following command.

```sh
node_modules/.bin/vitest run --maxWorkers=1 src/build/factory.test.ts src/runtime/events.test.ts src/indexing/client.test.ts src/build/schema.test.ts
```

The four upstream files passed all 55 tests. Whole-module Go test execution is tracked separately.

## Go-only scope audit

The ordinary `runtime.NewIndexer` supports Isolated and live Omnichain. In the separate-store `historical.RuntimeRunner`, the constructor still requires default multichain or finalized Omnichain. Live Omnichain and contract CALLs without a covering block callback are rejected during preflight. Finite static log sources are already supported without a block callback, including when another chain uses one. A native capability test still expected that valid configuration to fail. The expectation now requires reaching the storage opener and retains the existing invalid scenarios.

```sh
selection='^TestRuntimeOmnichain(PreflightBoundary|StaticLogsCapabilities|LogOnlySource)$'
go test -p 1 -count=1 -v -timeout=45m -run "$selection" ./historical
go test -race -p 1 -count=1 -v -timeout=45m -run "$selection" ./historical
go test -count=1 -run '^TestRuntimePreflightRejectsUnsupported$/^experimental_isolated$' ./historical
go test -race -count=1 -run '^TestRuntimePreflightRejectsUnsupported$/^experimental_isolated$' ./historical
```

The upstream reference ERC-20 project tracks allowances, approval and transfer history, and relations between accounts and transfers over mainnet blocks 13,142,655 through 13,150,000. The native balance starter covers local balances, while the API example implements histories without the account transfer relations. The remaining template work includes the dedicated reference project and default GraphQL setup and source naming for older projects. The Go factory example records discovered events for SQL queries. Its upstream counterpart declares an empty Llama table and prints the events. [Current support matrix](../../../docs/RUNTIME_SUPPORT.md)

The final ordinary and race selections each passed 36 CLI leaf cases, 55 Omnichain audit leaf cases and the separate Isolated rejection check, with zero skips. All Go packages compiled and vet passed. Repository tooling passed 503 Node.js and 43 Python tests. Initial concurrent executions were interrupted by memory pressure and a later setup attempt ran out of workspace storage. These results are from completed reruns after reclaiming compiler caches and executing database-backed suites sequentially.
