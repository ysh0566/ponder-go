# Mixed-source reorganization during RPC failover

The public Go indexer runs continuously with each of the three ordering modes on PostgreSQL 14.24 and PGlite 0.2.13. Three Anvil 1.8.5 processes serve two independent branches of chain 101 and one unchanged chain 202. Both branches share genesis and reach height three at the same time. The PostgreSQL case enables the RPC cache and the PGlite case disables it.

At height one a factory discovers a different child address on each branch. At height two the same sender transfers funds and calls both candidate contracts. Both emit valid logs. Each branch must deliver the incoming account transaction, contract log, contract CALL and incoming transfer for its own discovered child, in that order. Chain 202 also emits a static log. In default multichain and Omnichain its handler reads the amount stored by the chain 101 log. In Isolated it writes to its own chain.

| Observation | Isolated | Default multichain and Omnichain |
| --- | --- | --- |
| Both chain 101 gateways fail with HTTP 503 and 429 | Readiness becomes false. Both chains keep their business data, callback counts and checkpoints. | Same. |
| The standby returns a replacement branch | Chain 101 replaces the child, block hashes and event values. Chain 202 keeps its original hash, one callback and value `23`. | Chain 101 replaces the same four events. The chain 202 callback runs again against the same peer log, changing its stored value from `23/7` to `23/11`. |
| The indexer and database connection or local engine close and reopen | The canonical rows, discovered child, headers, checkpoints and delivery counts persist. | Same. |

The fixture compares the persisted header at each event checkpoint with the corresponding node. Two competing tips can share the same height and timestamp. The block hash distinguishes them. It also checks the business payloads, including the trace target, log value and transfer amount, before and after failover. The peer checkpoint stays stable through the fork even when its callback must replay.

## Verification

The Go 1.25.11 selection includes the earlier block-only provider switch and the bounded mixed-source cases. Use the repository test environment with fresh PostgreSQL and PGlite storage.

```sh
selection='^(TestMixedSourceProviderReorg|TestMultiProviderCrossChainReorg|TestIsolatedMixedSources|TestOrderedMixedSources|TestDefaultMixedSources)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./runtime
go test -p 1 -race -v -count=1 -timeout=45m -run "$selection" ./runtime
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

All 16 selected leaf cases passed in both ordinary and race modes with zero skips. All Go packages compiled and passed vet. The repository verification tools passed 503 Node.js tests and 43 Python tests.

The pinned Ponder 0.18.2 oracle passed 49 cases. Run `node_modules/.bin/vitest run --maxWorkers=1 src/runtime/events.test.ts src/sync-realtime/index.test.ts src/rpc/index.test.ts` from `packages/core` at commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

The business and sync data in this fixture use the atomic Store. Engine interruption during a replacement mixed-source callback and the independent-store Isolated and live Omnichain modes remain open.
