# Uncached account sources in finalized Omnichain ranges

A Go indexer using the independent business and sync stores can subscribe to account transactions and native transfers on a chain with `cacheRpcRequests: false`. It can omit the block callback. A source needs a finite range inside the global finalized watermark. Contract CALL callbacks still require a covering block callback.

## What the runner checks

A fresh transport reads each active account block by its finalized hash, checks its number, timestamp, parent, transaction count and positions, and validates receipts against those transactions. It can fall back to individual transaction receipts. Transfer subscriptions additionally require hash-addressed call traces with one root per transaction. Reverted frames and their descendants are excluded. The event builder applies the same address, factory birth and native value rules used for historical events. Overlapping account sources share the block read. Disjoint sources leave the intervening gap unqueried by the account scanner.

A matching event prevents the runner from recording an empty interval. Missing archive data, partial trace roots and inconsistent responses stop the scan. Completed work keeps its durable cursor. This path depends on historical RPC availability when reopening the deployment.

## Verification

The integration fixture uses two real Anvil 1.8.5 chains, staggered timestamps, separate business and sync stores, and a shared business row. It runs on PostgreSQL 14.24 and PGlite 0.2.13 with both chains cached and with the second chain uncached. Blocks 1, 2 and 4 produce twelve ordered incoming/outgoing transaction and transfer callbacks. Block 3 is empty. Block 5 contains a reverted outgoing payment. There is no block callback.

The last transfer fails after a write. A cold owner resumes at the committed group and a third owner reopens the completed deployment. Assertions cover the shared row, the global last transfer, both scan heads, stable per-chain logical cursors, and the presence or absence of sync-cache transaction, receipt, trace and interval rows. The uncached chain can commit the block-end position of its last event group while its scan head reaches the end of the source.

Store tests exercise missing and duplicate data, incorrect receipts and trace roots, per-transaction receipt fallback, nested value transfers, reverted and zero-value calls, optional log ABIs, disjoint filter ranges, and factory birth before or at an account event alongside ABI-skipped and decodable logs. These local tests add native coverage to the existing upstream test inventory.

With the repository's integration-test environment and disposable database directories, run the following selections. Repeat the Go test selections with `-race`.

```sh
selection='^Test(OmnichainAccountScan.*|OmnichainUncachedAccount.*|UncachedLogScanEvidence|LogFactoryScanEvidence|RuntimeOmnichainPreflightBoundary|OmnichainUncachedArchiveRetry)$'
go test -p 1 -count=1 -timeout=45m -run "$selection" ./store ./historical
go test -p 1 -count=1 ./rpc
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

At upstream revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, run `vitest run --maxWorkers=1 src/runtime/events.test.ts src/runtime/historical.test.ts` from `packages/core`. All 25 tests passed.

The focused Go selection passed with zero skips in ordinary and race mode on both database backends. It includes 33 uncached account evidence cases and the adjacent archive-outage recovery test. The offline RPC package passed in both modes. All packages compiled and vet passed. Verification utilities passed 503 Node and 43 Python cases. The 17 opt-in provider cases retain their separately recorded live results. The whole-module ordinary and race runs use the serialized regression queue.
