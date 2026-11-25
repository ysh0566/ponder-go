# Current database formats

A fresh standalone recovery database writes `managed-live-recovery-v3`. A successful cold recovery preserves this format and advances the generation. Reading a different control version fails before recovery writes. The recovery receipt has its own current identifier, `managed-live-recovery-receipt-v2`.

Static log-only omnichain history uses the same v17 paged header proofs across all supported ranges. Reopening a v16 namespace requires rebuilding the index in a fresh namespace. The current RPC cache uses the pinned nine-table `ponder_rpc_cache_1` DDL. A fresh startup can retry after an interrupted creation or a lost commit acknowledgement. A current cache can reopen beside an older namespace and preserves the rows in both.

| Verification | Result |
| --- | --- |
| Native storage on PostgreSQL 14.24 and PGlite 0.2.13 | Normal and race each passed 16 top-level tests and 106 subtests. |
| Original `packages/core/src/database/index.test.ts` | All 18 declarations passed in a fresh run on each backend, including the five historical migration cases. |
| Go module | Build and vet passed. The full normal and race suite is running separately. |
| Repository checks | All 500 Node checks and 35 Python tests passed. |
| Scope accounting | The original 905 records retain their inputs, assertions and source states. The Go project maps 562, has 338 pending implementation and excludes five old-cache migrations. |

The full original inventory combines unchanged upstream reports at the pinned Ponder revision. It records 874 passes, 19 failures, 10 upstream skips and two upstream todos. The 19 failures belong to the provider RPC tests. The fresh database runs above cover the entire original database test file. [Inventory and source IDs](upstream.json)

The machine-readable report contains native test names, unique leaf counts and SHA-256 digests. Final runtime jobs are listed as pending until each process exits successfully. [Stage results](results.json)
