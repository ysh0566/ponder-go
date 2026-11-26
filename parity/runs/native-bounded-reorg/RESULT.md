# Bounded default multichain fork recovery

The Go dual-store runner checks each next block's parent and checks the canonical hash of each unfinalized end block before returning. A fork pauses the writer, joins speculative reads, identifies the accepted branch from durable business receipts, and coordinates sync invalidation and business rollback. Replay includes other chains' later commits. Readiness returns after reaching the recorded per-chain replay targets. A fork crossing the opening finalized block fails closed.

The two-chain fixture changes the value of a callback's historical balance read on the replacement branch. It checks the final shared row, the replaced block hash, the unchanged peer's repeated notification, renewed readiness, and completion at the configured heights. It covers replacement during acquisition, replacement at the completed end, reopening after the first replay commit, and a fork crossing finality.

Go 1.25.11 compiled all 50 packages and `go vet -p 1 ./...` passed. Verification tooling passed 499 Node cases, four loopback cases and 43 Python cases. The integration selection uses a dedicated PostgreSQL 14.24 database and fresh PGlite storage.

```sh
selection='^(TestRuntimeBoundedReorg|TestRuntimeBoundedLifecycle|TestRuntimeGroupNotifications|TestRuntimeContinuousReorg|TestRuntimeCachePolicySnapshot)$'
go test -p 1 -v -count=1 -timeout=20m -run "$selection" ./historical
go test -p 1 -race -v -count=1 -timeout=20m -run "$selection" ./historical
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

All 20 leaves passed in ordinary mode. The combined race run passed 18, including all eight bounded fork cases. The PGlite bridge was killed during the later bounded lifecycle case, and the fixture guard refused to reuse that directory for the notifications case. Both affected leaves passed in fresh PGlite storage with the race detector. The same 20 distinct leaves therefore passed in both modes, with zero skips.

```sh
go test -p 1 -race -v -count=1 -timeout=10m -run '^(TestRuntimeBoundedLifecycle|TestRuntimeGroupNotifications)$/^pglite$' ./historical
```

The original Ponder 0.18.2 sync-realtime selection passed all four selected cases. Its other 26 cases were outside this selection. Run from the pinned upstream packages/core directory.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/sync-realtime/index.test.ts -t 'handleReorg|finalize event'
```

Full-module ordinary and race results for this revision are pending. These native integration tests add no upstream test mappings.
