# HTTP provider failover during a cross-chain reorg

The same Go acceptance case runs with default multichain and Omnichain ordering, each on PostgreSQL 14.24 and PGlite 0.2.13. It uses the native Go indexer, real Anvil 1.8.5 responses and three independent nodes. The PostgreSQL run enables protocol caching and the PGlite run disables it.

1. Two nodes for chain 101 share genesis but mine different blocks at height one. The first block timestamp is genesis +10 on the original node and +15 on the replacement. Both reach height two. Chain 202 mines a block at +20 whose callback reads the value committed by chain 101. Initially only the original provider serves chain 101 and the two business rows contain its hash.
2. Both gateways for chain 101 reject requests with HTTP 503 and 429. The readiness callback reports false. Both gateways record rejections, both business rows survive, the callback sequence remains unchanged and the stored checkpoints agree with their pre-outage values.
3. The replacement provider starts serving. The indexer rewinds the old suffix, replaces the first chain's row and reruns the second chain's dependent callback. Both rows contain the replacement hash. The four deliveries across the original run and the replay are checked in order, including the unchanged hash on the dependent chain. The persisted headers and the timestamps and chain IDs in the checkpoints are checked against the independent underlying nodes.
4. The same physical store reopens on the replacement provider with no new callback deliveries. The original provider independently rebuilds the replacement branch, including an identical height-two hash. Serving resumes on it while the replacement gateway rejects requests. Its successful request count advances, with the same two rows, stored headers, checkpoints and callback sequence.

This fixture exercises failover between responding and unavailable providers. When service returns on the original provider, both nodes have already converged. Expectations about specific commercial gateways and historical logs remain in the separate provider inventory.

## Verification

Go 1.25.11 ordinary and race runs each passed 15 leaf cases across the runtime and RPC packages, including all four new mode/backend combinations.

```sh
selection='^(TestMultiProviderCrossChainReorg|TestDefaultIndexer|TestOrderedIndexer|TestApplicationRPCRecovery|TestIsolatedIndexer|TestRetryAndCache|TestSourceLoadBalance|TestInvalidParamsIsNotRetried)$'
go test -v -count=1 -timeout=15m -run "$selection" ./runtime ./rpc
go test -race -v -count=1 -timeout=15m -run "$selection" ./runtime ./rpc
go test -run '^$' ./...
go vet ./...
```

The pinned upstream at 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334 passed the six selected retry and reorg cases with `vitest run --maxWorkers=1 src/rpc/index.test.ts src/sync-realtime/index.test.ts -t 'handleReorg\(\)|createRpc\('`. There were 32 tests outside that name filter. Module-wide compilation, vet and 503 verification-tool checks passed. The new acceptance cases leave the upstream test inventory unchanged. Whole-module ordinary and race results for this release are pending in the shared regression queue.
