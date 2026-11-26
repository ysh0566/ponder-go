# Account event scans in the independent Omnichain runtime

A finite, globally finalized chain can index account transactions and native transfers with its RPC cache enabled and no block handler. The storage layer checks cached filter intervals and reads candidate transactions, receipts and traces when proving empty ranges. The event builder applies the same receipt status, address and native-value rules as during indexing. A pending event prevents the range from being certified empty. Contract CALLs and uncached account events still require a covering block callback.

## Integration fixture

Two different Anvil chains produce incoming transactions and transfers at blocks 1 and 4 and outgoing ones at block 2. Block 3 is a gap and the range ends at empty block 5. No block callback is registered. The two chains use staggered timestamps, SQL page size 1 and one shared business row. Each account callback checks the originating transaction hash. A failure after the last transfer writes to the row rolls back its event group. The next owner appends only the remaining events. A third owner opens the completed deployment and runs no callbacks. The tests check the full ordering of all 12 callbacks, the last event, both scan heads and the stability of the per-chain checkpoints.

Store-level cases distinguish successful and reverted transactions, unrelated senders, value-bearing calls, zero-value calls and delegatecalls. They also reject missing headers and required receipts. The preflight rejects an uncached account-only chain before opening runtime RPC or storage.

## Scope

These fixtures use local chains and separate task-owned business and sync stores on PostgreSQL and PGlite. The current marker for deployments using these account scans is `managed-live-omnichain-account-scan-v1`. The application can reopen its own completed deployment using the same filters and cache policy. Other runtime configurations retain their own current storage formats.

## Reproduction

The Go selections use Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13. Set the test PostgreSQL URL and task-owned retained-fixture directories before running. A fresh directory per invocation isolates the PGlite engines.

```sh
selection='^Test(OmnichainAccountScan.*|OmnichainAccountPartition|RuntimeOmnichainPreflightBoundary|LogOnlyPhysicalInputs|LogOnlyPagedFormatSelection|LogFactoryScanEvidence)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./store ./historical
go test -race -p 1 -v -count=1 -timeout=45m -run "$selection" ./store ./historical
go test -p 1 -v -count=1 -run '^Test(OmnichainAccountScan.*|LogOnlyScanDecodedCoverage)$' ./store
go test -race -p 1 -v -count=1 -run '^Test(OmnichainAccountScan.*|LogOnlyScanDecodedCoverage)$' ./store
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
# From packages/core of the Ponder 0.18.2 checkout
vitest run --maxWorkers=1 src/runtime/events.test.ts src/runtime/historical.test.ts
```

The new account scenarios passed in ordinary and race mode on both backends. Combined account and log evidence also rejected a pending transaction when a log failed ABI decoding, and rejected a decodable log beside a reverted transaction. All 25 selected upstream tests passed at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. The module compiled and passed vet. Verification utilities passed 503 Node and 43 Python tests. The upstream test inventory is unchanged.

The initial test assumed every chain's logical cursor always equals its last callback. The coordinator can commit the ending block position of an event group. The final assertion checks the actual last global event separately, requires each chain's logical cursor to identify its last transfer or its scanned endpoint, and compares the saved logical positions across a completed reopen. An exploratory parallel run exhausted memory in a PGlite process. The release runs use separate on-disk directories and run the database suites sequentially.

The adjacent regression selections exercise the pre-existing closing-block account path, factory logs, mixed chain roles and the all-empty, failed-page and ABI-skipped trailing-log cases. The final code review records their ordinary and race results. Full-module ordinary and race runs use the serialized release lane and are separate from the focused results above.

```sh
go test -p 1 -count=1 -timeout=45m -run '^Test(OmnichainStaticAccountEvents|RuntimeOmnichainLogFactory|RuntimeOmnichainMixedCallbacks)$' ./historical
go test -p 1 -count=1 -timeout=45m -run '^TestRuntimeOmnichainLogOnlySource$/.*/^(all_empty|small_failure|abi_skip_trailing_empty)$' ./historical
# Repeat each selection with -race.
```
