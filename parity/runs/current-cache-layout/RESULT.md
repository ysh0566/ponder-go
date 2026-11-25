# RPC cache layout verification

The previous startup check accepted nine correctly named tables with incompatible columns or keys. Seven independent changes reproduced this on PostgreSQL and PGlite. Reopen, direct read and direct write all accepted each changed schema. The writes inserted a second row into an incompatible cache.

The current opener compares the catalog to the layout produced by the pinned `ponder_rpc_cache_1` DDL. Direct cache access validates the same layout. A failed validation preserves the existing rows and reports the affected table. Ordinary secondary indexes are compatible.

| Verification | Result |
| --- | --- |
| Go storage, normal | 14 top-level tests and 99 leaf cases passed |
| Go storage, race | Same 99 leaf cases passed |
| Layout regressions | Ten incompatible layouts and one compatible index on each backend |
| Original database test file | All 18 declarations passed on PostgreSQL 14.24 and on PGlite 0.2.13 |
| Clean Go module | Build and vet passed |

The native selection also covers concurrent creation, failed creation, lost commit acknowledgment, cold rollback, source-cache reads and writes, and asynchronous access. Both modes used a dedicated PostgreSQL cluster and included PGlite. There were zero failures or skips. The original snapshot-copy case exercises its locking assertions on PostgreSQL and returns early on PGlite. [Execution digests](results.json)

The original inventory remains at 876 passed, 17 provider failures, ten skips and two todos. These additional native regressions leave the mapping count at 566 of 905. [Original inventory](../gateway-error-pagination/upstream.json)

To rerun the native checks, set `PONDER_TEST_POSTGRES` to a disposable PostgreSQL test database and `PONDER_UPSTREAM` to the pinned checkout with the core dependencies installed.

```sh
cases='^(TestSyncCacheLayout.*|TestSyncMigration.*|TestUpstreamSyncMigration|TestProtocolCacheBoundaries|TestProtocolColdRewind|TestProtocolDisabledOwnership|TestSourceIntervalRewind|TestUpstreamSourceRPCCache|TestSourceRPCBoundaries|TestSourceRPCAsyncBackends|TestSourceRPCAsyncReadBackends)$'
go test -count=1 -timeout=60m -run "$cases" ./store
go test -race -count=1 -timeout=60m -run "$cases" ./store
```
