# Go templates for event filtering and custom HTTP routes

The source examples are feature-filter and feature-api-functions at Ponder 0.18.2 revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. The Go projects use the same contract addresses, chain IDs, reference heights, event signatures and handler inputs. Their usage and HTTP routes are documented in the [Go guide](../../../docs/USAGE.md#选择-go-项目模板).

The test generates two projects with the CLI and runs each against its own Anvil chain and a new PostgreSQL namespace or PGlite directory. The first block emits matching events from an unrelated contract. The second emits at the configured address, and the third repeats the transactions beyond the configured end block. The application is queried through its public HTTP listener.

* The filter project keeps all ten reference event definitions and compiles a source filter for Swap. The test inspects outgoing eth_getLogs requests for the specific contract and Swap topic, checks the payer and recipient in SQL, and queries the event through GraphQL at both / and /graphql. The contract emits a URI log alongside the Swap.
* The API project checks zero counts and the empty ranking response, twelve indexed transfers including a self-transfer, two approvals on the same allowance, and the first-seen account behavior. The ten-row endpoint sorts by the sender's current balance. The fixture covers negative balances, a fractional wei-to-ether conversion, whole amounts and checksum address input. GraphQL independently returns the indexed balances, allowances and event records. The template leaves the built-in SQL and SQL live routes unmounted, matching the upstream API example.

The fixture's call targets and logs are local. Public mainnet history was not replayed. To regenerate the fixture with solc 0.8.37, use the runtime bytecode line from the following command.

```sh
solc --optimize --evm-version paris --metadata-hash none --bin-runtime cmd/ponder/testdata/templates/FilterAndAPI.sol
```

## Verification

Run with Go 1.25.11, Anvil 1.8.5 and task-owned PostgreSQL 14.24 and PGlite 0.2.13 databases. The race selection instruments the generated indexers.

```sh
selection='^Test(GoFilterAndAPITemplates|GoContractTemplates|GoEmptyAndBlocksTemplates|GoProjectTemplates|GoAccountsTemplate|InitGoProject|CreateProjectConcurrent|CreateEmptyProject|GeneratedGoServe|GeneratedGoStartViews|SourceHTTPServer|HTTPServerBoundaries)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder ./query
go test -race -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder ./query
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The upstream oracle ran from packages/core with `node_modules/.bin/vitest run --maxWorkers=1 src/build/config.test.ts src/graphql/middleware.test.ts`. Both files passed, with 46 passing cases and one source-declared skip for the public RPC URL warning. The checked-in event ABIs and default networks were also compared directly with the source examples.

The ordinary and race selections each passed in both Go packages with zero skips. All packages compiled and vet passed. The tooling checks passed 503 Node.js tests and 43 Python tests. Whole-module ordinary and race execution is tracked separately.
