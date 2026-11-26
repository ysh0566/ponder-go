# Go projects for FileStore reads and Multicall3 traces

The native CLI offers `ponder init --template read-contract my-files` and `ponder init --template call-traces my-multicalls`. The projects share the existing Go server and database startup. Their three embedded ABIs were compared with the corresponding FileStore events, frontend function and viem Multicall3 function from Ponder 0.18.2. The mainnet chain ID, source addresses, start heights, frontend address and callback names follow the pinned examples.

## What the fixtures verify

The test generates each project through the CLI, builds it, starts it against a mined Anvil chain and queries its business rows through the public HTTP SQL service. It runs each project on both PostgreSQL and PGlite.

* FileStore subscribes to creation and deletion. A Unicode filename and body exercise ABI string encoding and the distinction between a file's byte size and its character count. Valid metadata populates the three optional columns. A routed creation has malformed JSON, leaving those columns null. The recipient stores a different body from the emitter and clears that body in a later block. The query must return the routed body from the event block. The deleted file and the event before the start block produce no final rows.
* Multicall3 records a row per trace caller. The direct and forwarded calls use different senders at the trace level. The fixture makes real subcalls that succeed or revert, exercises empty calldata and an empty batch, and reverts an entire batch when failure is disallowed. The test independently reads the actual gas from transaction call traces. It expects three successful and one failed subcall for the direct sender, and one success and one failure for the forwarder. Their input counters are four and three. The reference column called `bytes` counts each subcall's calldata in four-byte units rounded up before summing.

The self-hosted chain exercises event-time contract state and nested EVM calls. Public mainnet FileStore and Multicall3 history was not replayed.

## Reproduction

With Go 1.25.11, Anvil 1.8.5 and the task database test environment, run these selections from the Go repository. The release uses PostgreSQL 14.24 and PGlite 0.2.13. The race build also instruments the generated indexers.

```sh
selection='^Test(GoContractTemplates|GoEmptyAndBlocksTemplates|GoProjectTemplates|GoAccountsTemplate|InitGoProject|CreateProjectConcurrent|CreateEmptyProject|ContractTraceRuntime|ContractLogAndTraceBatch)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder ./runtime
go test -race -p 1 -v -count=1 -timeout=45m -run "$selection" ./cmd/ponder ./runtime
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The Solidity source and corresponding runtime bytecode are in `cmd/ponder/testdata/templates`. The fixtures use the following compiler invocation. The JSON fixture contains the runtime code for the three contracts and the input methods used by the test driver.

```sh
solc --optimize --evm-version paris --metadata-hash none --combined-json abi,bin-runtime cmd/ponder/testdata/templates/ContractSources.sol
```

At upstream revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, all 25 event and callback-client tests passed with `node_modules/.bin/vitest run --maxWorkers=1 src/runtime/events.test.ts src/indexing/client.test.ts` from `packages/core`.

The ordinary and race selections above passed in both Go packages with zero skips. They cover the two generated indexers, the neighboring Go templates and contract trace regressions on PostgreSQL and PGlite. All Go packages compiled, `go vet` passed, and the repository tooling passed 503 Node.js and 43 Python checks. These results cover the selected scenarios. Whole-module ordinary and race execution is tracked separately.
