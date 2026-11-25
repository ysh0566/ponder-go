# Current database formats

A fresh standalone recovery database writes `managed-live-recovery-v3`. A successful cold recovery preserves this format and advances the generation. Reading a different control version fails before recovery writes. The recovery receipt has its own current identifier, `managed-live-recovery-receipt-v2`.

Static log-only omnichain history uses the same v17 paged header proofs across all supported ranges. Reopening a v16 namespace requires rebuilding the index in a fresh namespace. The current RPC cache uses the pinned nine-table `ponder_rpc_cache_1` DDL. A fresh startup can retry after an interrupted creation or a lost commit acknowledgement. A current cache can reopen beside an older namespace and preserves the rows in both.

| Verification | Result |
| --- | --- |
| Native storage on PostgreSQL 14.24 and PGlite 0.2.13 | Normal and race each passed 16 top-level tests and 106 subtests (98 leaf cases). |
| Native log-only runtime | All 44 leaf cases passed in normal and race modes across short histories, sparse histories and PostgreSQL application exits. |
| Original `packages/core/src/database/index.test.ts` | All 18 declarations passed in a fresh run on each backend, including the five historical migration cases. |
| Go module | Build and vet passed. The full normal and race suite is running separately. |
| Repository checks | All 500 Node checks and 35 Python tests passed. |
| Scope accounting | The original 905 records retain their inputs, assertions and source states. The Go project maps 562, has 338 pending implementation and excludes five old-cache migrations. |

The full original inventory combines unchanged upstream reports at the pinned Ponder revision. It records 874 passes, 19 failures, 10 upstream skips and two upstream todos. The 19 failures belong to the provider RPC tests. The fresh database runs above cover the entire original database test file. The upstream snapshot-copy case returns early on PGlite and exercises the database lock on PostgreSQL. [Inventory and source IDs](upstream.json)

The machine-readable report contains native test names, unique leaf counts and SHA-256 digests. The normal and race runs have identical sets of 44 runtime leaf cases. [Stage results](results.json)

## Reproduce the database checks

Use the pinned sibling upstream checkout with its Node dependencies, Go 1.25.11, and an isolated PostgreSQL 14.24 test database. Set `PONDER_TEST_POSTGRES` to its connection URL, `PONDER_M45_POSTGRES_DIRECTORY` to its server-side data directory, and `PONDER_M45_POSTGRES_DATABASE` to its database name. Point `PONDER_M44_TEST_ROOT` and `PONDER_M45_TEST_ROOT` at a fresh directory for each run.

```bash
storage='^(TestLogOnly.*|TestPaged.*|TestManagedLiveRecoveryDatabase|TestManagedLiveRecoveryBoundedFork|TestManagedLiveRecoveryFormatIsolation|TestUpstreamSyncMigration|TestSyncMigration.*)$'
go test -count=1 -timeout=60m -json -run "$storage" ./store
go test -count=1 -timeout=60m -json -race -run "$storage" ./store

go test -count=1 -timeout=60m -json -run '^TestRuntimeOmnichainLogOnlySource$' ./historical
go test -count=1 -timeout=60m -json -run '^TestRuntimeOmnichain(LogOnlyApplicationExit|SparseRanges|SparseApplicationExit)$' ./historical
go test -count=1 -timeout=60m -json -race -run '^TestRuntimeOmnichain(LogOnlySource|LogOnlyApplicationExit|SparseRanges|SparseApplicationExit)$' ./historical
```

The race evidence uses the completed short-history and application-exit test events from the combined command, plus successful package exits for these two backend selections. The combined process was stopped after those three top-level tests passed and the dedicated backend jobs took over the sparse selection. The report checks the disjoint union against all 44 normal case names.

```bash
go test -count=1 -timeout=120m -json -race -run '^TestRuntimeOmnichainSparseRanges$/^postgres$' ./historical
go test -count=1 -timeout=120m -json -race -run '^TestRuntimeOmnichainSparseRanges$/^pglite$' ./historical
```

For the original database verification, build the pinned upstream client and utils packages and generate the upstream test contracts with Foundry 1.8.5. Run the following from upstream `packages/core` once with `DATABASE_URL` pointing to a dedicated PostgreSQL test cluster and once with it unset for PGlite.

```bash
./node_modules/.bin/vitest run --configLoader native --maxWorkers=1 \
  --reporter=json --outputFile="$report" src/database/index.test.ts
```
