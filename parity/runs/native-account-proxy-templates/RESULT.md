# Go account and proxy starter projects

The Go CLI generates BeaverBuilder account and Astaria router proxy projects from the pinned Ponder 0.18.2 examples at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Both use Ethereum chain ID 1. The account project starts inclusively at the head seen on first use of the deployment. The router project starts at block 17,942,156 and subscribes to logs emitted by the proxy address. Its event ABI contains the three proxy administration events and the two implementation events used by the handlers.

## Local integration

The generated projects start through the public `ponder start` command on Anvil 1.8.5. Tests wait for HTTP readiness and query the application's SQL endpoint. The node and monitored address point to local fixtures. These runs do not replay Ethereum mainnet history.

| Project | Evidence on PostgreSQL and PGlite |
| --- | --- |
| Account | An outgoing transaction before the initial head is excluded. Two outgoing transactions in the first indexed block sum to 18,446,744,073,709,551,618 wei with calldata `0xcd`. Contract creation has no recipient and adds no row. An incoming transfer appears in the application log. The process stops while the node mines two more blocks. On restart the recipient total is 18,446,744,073,709,551,626 wei with calldata `0x42`. |
| Proxy | A local router invokes its implementation directly and by delegatecall. Both calls emit Liquidation and OwnershipTransferred. Only the delegated pair is recorded under the router address. SQL returns exactly one liquidator and one new owner, with address values 42 and 43 respectively. |

The start-block store test covers a failed RPC observation, two starters pinning different heads, a pinned genesis height, rereading without RPC, and an independent start height for a second chain. The store retains its current database format.

## Reproduction

The focused selection includes the account and proxy projects, all four earlier reference templates, the default project's CLI and query paths, and the start-block store. The generated subprocesses inherit Go's race flag in the instrumented run. Use the task PostgreSQL database, local PGlite driver and Anvil on PATH.

```sh
selection='^Test(GoProjectTemplates|GoAccountsTemplate|ResolveStartBlock|InitGoProject|GeneratedGoStartViews|GeneratedGoServe)$'
go test -p 1 -count=1 -timeout=45m -run "$selection" ./cmd/ponder ./store
go test -race -p 1 -count=1 -timeout=45m -run "$selection" ./cmd/ponder ./store
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
solc --optimize --evm-version paris --metadata-hash none --combined-json bin cmd/ponder/testdata/templates/Proxy.sol
# From packages/core in the pinned upstream checkout
vitest run src/runtime/events.test.ts
```

Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13 were used. The two network configurations, four callback names and five event signatures were compared with the pinned source. Recompiling the local router with solc 0.8.37 reproduced the fixture bytecode. All 11 upstream event cases passed. The existing upstream test ledger and provider observations are unchanged.

The focused ordinary and race runs passed on both backends, including all six named template/backend pairs per backend. The module compiled and passed vet. Verification utilities passed 503 Node tests and 43 Python tests. Full-module ordinary and race tests use the existing serialized release lane and are reported separately from these results.
