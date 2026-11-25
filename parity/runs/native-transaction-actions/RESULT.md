# Transaction actions in Go callbacks

A Go event handler can query an account nonce, a block’s transaction count, a transaction, its receipt and its confirmation count. Callers can select transactions by hash or by block and index. Count actions default to the current head and accept explicit block selectors. Transaction and receipt objects retain JSON-RPC fields and hexadecimal quantities, following the existing Go GetBlock convention. Counts return uint64 and confirmations return *big.Int.

A hash identifies the transaction, but its inclusion and receipt may change under a reorg. Transaction hashes, moving block tags and non-genesis block numbers therefore read the node each time. An explicit block hash or genesis selector can use the callback cache. Confirmation counts use the live head and the supplied receipt or current transaction location. Missing transactions and receipts use the existing ten-attempt, cancellable empty-response policy.

On PostgreSQL 14.24 and PGlite 0.2.13, the test dispatches a registered callback through the indexing service and uses an actual database-backed RPC cache. Each backend runs a fresh Anvil 1.8.5 chain with automatic mining disabled.

- The callback submits a transfer, sees a pending transaction with zero confirmations, and observes a missing receipt with retries off. A separate mining operation runs after the first empty reply to the retry-enabled lookup. The next attempt returns a successful receipt.
- Hash, block number and block hash selectors all find transaction index zero. Genesis and latest nonce differ. The next empty block updates the latest block transaction count and brings confirmations to two for both receipt and hash selectors.
- A fixed block hash count reaches the durable cache. A missing hash produces an identifiable Go error and cancelling the retry returns the caller's context error.
- Reverting the Anvil snapshot removes the receipt. A fresh transaction lookup agrees with the node about the reverted transfer. After mining an empty replacement at the same height, its transaction count and the sender nonce are zero, and the previous transaction index is absent. The height-based count reproduced a stale cached value before the fix. Conflicting and absent selectors are rejected before transport use.

[Callback and selector tests](../../../indexing/client_transactions_test.go) · [Go actions](../../../indexing/client_transactions.go) · [Pinned upstream method list](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/indexing/client.ts#L122-L163)

## Verification

- `go test -count=2 -v ./indexing -run 'Test(CallbackTransactionActions|TransactionLookupGuards)$'` and the same selection with `-race` passed. Each mode ran the replacement-block test twice on each database.
- The full `rpc` package passed `go test -count=1 ./rpc` and `go test -race -count=1 ./rpc` with the local test environment.
- `go test -run '^$' ./...` compiled all packages, and `go vet ./...` completed successfully.
- All 503 Node verification-tool tests passed. These check the repository's test tooling and evidence handling.
- Comparing the three action lists in the pinned upstream callback client to exported Go methods yields 12 of 21 names. The [method matrix](../../../docs/RUNTIME_SUPPORT.md#callback-rpc-methods) lists the other nine.
