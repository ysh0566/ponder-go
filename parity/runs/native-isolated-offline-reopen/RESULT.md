# Cold restart of a dual-store Isolated Go indexer during an RPC outage

Continuous indexing can reopen a previously initialized deployment while an RPC endpoint is unavailable. The writer authenticates the prior business snapshot and sync ledger under both existing owner locks. It recovers missing RPC heads from the later of the last audited opening finality and the committed business finality, then performs the ordinary recovery and sync-cache fence. The database format and persisted receipt structure are unchanged.

During startup the healthy chains backfill to their own opening heads and continue live polling. Deployment readiness waits for the missing chains. Each returning endpoint must supply a fresh finalized block and canonical ancestry through the durable safe cursor before the first replayed callback. This check also applies when an orphaned block is still available by hash. Fresh databases and deployments with incomplete initialization still require opening RPC data for all participating chains. Bounded finalized jobs with no live poller also require each endpoint on reopen. The runner preserves the RPC error when an initial cursor is missing. [Startup](../../../historical/live_runtime.go) · [Recovery](../../../store/managed_live_domains_bootstrap.go) · [Live admission](../../../historical/live_runtime_continuous.go)

## Verification

The two-chain test starts with blocks 1 and 2 indexed on both chains. At cold restart one endpoint answers HTTP 503. The healthy chain commits through block 4 while the peer's cursor stays fixed and the deployment is unready. Returning a replacement branch above the safe block allows both chains to reach block 5. The test compares the returned chain's business rows with canonical hashes. An endpoint that is still below the retained finalized height remains pending. A replacement at the already-finalized height stops the writer before any callback on the divergent chain. That scenario also starts with both endpoints offline and restores the healthy chain first. Both PostgreSQL and PGlite use the original task-owned database on reopen. [Integration test](../../../historical/isolated_live_outage_test.go)

Run from the Go module root with Go 1.25.11, an explicit task-owned PostgreSQL 14.24 database, PGlite 0.2.13 and the pinned upstream fixtures. Set `PONDER_M45_TEST_ROOT` and `PONDER_M44_TEST_ROOT` to a fresh fixture directory for each group. The following selections use separate processes for the longer PGlite reorgs.

```sh
unset PONDER_UPSTREAM_ROOT
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 -run '^(TestIsolatedReturningFinality|TestRuntimeIsolatedOfflineReopen)$' ./historical
  go test $flags -p 1 -count=1 -run '^TestRuntimeIsolatedLiveOutage$' ./historical
  go test $flags -p 1 -count=1 -run '^TestRuntimeIsolatedLiveReorg$/^postgres$' ./historical
  go test $flags -p 1 -count=1 -run '^TestRuntimeIsolatedLiveReorg$/^pglite$/^completed$' ./historical
  go test $flags -p 1 -count=1 -run '^TestRuntimeIsolatedLiveReorg$/^pglite$/^interrupted$' ./historical
  go test $flags -p 1 -count=1 -run '^TestRuntime(OmnichainPreflightBoundary|PreflightRejectsUnsupported)$' ./historical
  go test $flags -p 1 -count=1 -run '^TestManagedLiveDomainsBootstrap$' ./store
done
```

The upstream controls ran in `packages/core` on Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, with a private PostgreSQL database. All four selected original cases passed. The cold-start scheduling and dual-store failure scenarios are native integration coverage.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/database/actions.test.ts src/sync-realtime/index.test.ts -t 'commitBlock\(\) isolated|handleReorg\('
```

All 33 selected Go cases passed in ordinary mode and all 33 passed under the race detector. Five cover cold restart or returning finality, including the sync metrics at the first replayed commit. The other 28 exercise existing live outages, completed and interrupted reorgs, preflight validation, and storage initialization. All 51 packages compiled and `go vet` passed. The repository tooling passed 503 Node tests and 43 Python tests.

An earlier concurrent run exhausted workspace memory and terminated a PGlite process during the interrupted-reorg case. That case passed in a separate process in each mode. Full-module ordinary and race regression results are tracked separately from this selection.
