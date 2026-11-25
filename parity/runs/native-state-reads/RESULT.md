# Fee history and EIP-1186 proofs in Go callbacks

`indexing.Client.GetFeeHistory` selects a range ending at the event block. It returns the oldest block, base fees, gas usage ratios, and optional priority fee percentiles. `GetProof` returns the account, its balance, nonce and hashes, plus the value and encoded proof nodes for each requested storage slot. The Go methods preserve integer precision and accept the same event-relative, explicit-height and immutable block options as the existing state actions.

## Historical reads and replacement blocks

A registered historical event callback ran inside a business transaction on PostgreSQL 14.24 and PGlite 0.2.13, each with a separate database-backed RPC cache and a fresh Anvil 1.8.5 node. Block 1 deployed a contract with a balance greater than 2⁶⁴ wei and stored 42 in slot zero. Block 2 updated the slot to 71. The callback for block 1 ran after the second block was mined.

- The default queries returned the proof and fee history for block 1. Hashing the first account and storage proof nodes gave the node's block state root and the account storage root, respectively. Fee and reward quantities agreed with a direct node request.
- Explicit height 2 returned 71 and a nonzero block gas usage ratio. After replacing block 2 with an empty block, the same height returned 42 and a zero gas usage ratio. The separately requested immutable proof retained 71 after pending cache writes reached the database.
- An account-only proof accepted an empty storage key list. Response-shape tests covered omitted rewards, fees wider than 64 bits, missing fields and malformed quantities.

The viem 2.35.0 actions selected by the pinned upstream lockfile ran against a separate Anvil chain with the same transactions and reorg. The historical fees, proof node roots, nonce, balance and changed storage values matched the corresponding Go assertions.

[Go implementation](../../../indexing/client_state.go) · [Backend and response tests](../../../indexing/client_state_test.go) · [Upstream block selection](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/indexing/client.ts#L548-L561)

## Commands

Both ordinary and race modes passed two consecutive runs of the following selection.

```sh
go test -count=2 -timeout=30m ./indexing -run 'Test(CallbackHistoricalFeesAndProof|FeeAndProofResponseShapes|CallbackTransactionActions|SourceClientRequests|ManagedReadContractRetryRetainsCanonicalRoot)$'
go test -race -count=2 -timeout=30m ./indexing -run 'Test(CallbackHistoricalFeesAndProof|FeeAndProofResponseShapes|CallbackTransactionActions|SourceClientRequests|ManagedReadContractRetryRetainsCanonicalRoot)$'
```

All packages compiled with `go test -run '^$' ./...`, and `go vet ./...` passed. The 503 repository verification-tool tests passed. The earlier transaction milestone's indexing-package runs and timeout continuations together recorded passes for all 86 top-level tests in both modes. Complete module-wide runs are tracked separately.
