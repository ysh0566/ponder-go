# Native ERC-20 reference project and default GraphQL routes

The pinned Ponder 0.18.2 source is `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. The Go CLI generates `reference-erc20` for the Ethereum mainnet contract at `0x32353A6C91143bfd6C7d363B546e62a9A2489A20`, from block 13,142,655 through 13,150,000 inclusive. Its network, ABI events and physical table definitions match the custom ERC-20 API example. Each generated project includes the files it needs. Their transfer handlers differ when a recipient first appears. The dedicated reference project credits the initial receipt and stores every transfer and approval.

All 16 entries in the pinned creation menu have a corresponding Go scaffold. The Go CLI also offers a local ERC-20 tutorial. [Upstream template menu](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/create-ponder/src/index.ts#L60-L137)

The ordinary and race selections each passed all 28 generated-project leaf cases with zero skips. The final factory log checks were also run separately on both backends in both modes. All Go packages compiled, vet passed, and the three runtime admission checks reproduced their documented limits. The four upstream suites passed all 79 cases. Repository tool tests passed 503 Node.js and 43 Python cases. Whole-module Go execution is tracked separately.

## Local acceptance

The test generates and starts the application against Anvil 1.8.5. It verifies the mainnet defaults before substituting a local block interval and contract. Both PostgreSQL and PGlite receive real events from a transaction. The fixture includes a first receipt of 999 ether, a self-transfer, repeated transfers and two approvals for the same pair. SQL checks all three resulting balances, the current allowance and the approval history. Queries at both GraphQL mounts follow all twelve transfers in both directions and resolve both nested accounts. An unrelated emitter and an event after the inclusive end are outside the expected state.

The empty, blocks, read-contract, call-traces, ERC-721, ERC-1155, ERC-4626, factory, accounts and proxy scaffolds also serve their GraphQL schema at `/` and `/graphql`. Generated-project tests check both mounts, the reference names and independent SQL results. The factory exposes its empty `llama` table as well as its Go event history. The factory test checks the log message and emitting child address from each reference handler. The local ERC-20 tutorial and the custom API retain their own entry points.

## Reproduction

Use the repository's task-owned PostgreSQL and PGlite test configuration, Go 1.25.11 and Anvil 1.8.5.

```sh
selection='^TestGo(FilterAndAPI|EmptyAndBlocks|Project|Contract)Templates$|^TestGoAccountsTemplate$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder
go test -p 1 -race -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

From the pinned upstream `packages/core` checkout, run the four corresponding suites.

```sh
node_modules/.bin/vitest run --maxWorkers=1 src/graphql/index.test.ts src/graphql/middleware.test.ts src/build/schema.test.ts src/runtime/events.test.ts
```

These tests use local contracts and deterministic events. Replaying historical data from the public Ethereum deployment requires its archive logs.

## Go runtime scope check

The ordinary Go indexer supports live Omnichain and Isolated ordering. The independent business/sync runtime still rejects both live Omnichain and Isolated. Even for finalized Omnichain backfills, a chain registering block callbacks can only have one, with interval 1 and offset 0, spanning all its event filters. The capability tests exercise the live, isolated and interval-2 rejections. [Runtime constructor](../../../historical/live_runtime.go) · [Block callback validation](../../../historical/live_runtime_omnichain_callbacks.go)

```sh
go test -p 1 -count=1 -v -run '^Test(RuntimePreflightRejectsUnsupported|RuntimeOmnichainPreflightBoundary)$/^(experimental_isolated|interval|live)$' ./historical
```
