# Continuous Isolated startup through a chain outage

A dual-store Go writer can open on empty stores while one or all participating chains are temporarily unreachable. Open and recovery receipts record the heads the writer has acquired. A returning worker acquires its finalized head and proves the initial block before the writer commits that chain’s baseline and setup. When an earlier opening recorded a head, the returning node must also prove that head. A node below the recorded height or a new chain below its setup height keeps waiting. An incompatible finalized branch fails verification. [Runtime](../../../historical/live_runtime_continuous.go) · [Per-chain bootstrap](../../../historical/live_runtime_setup.go)

## Database and callback checks

The PostgreSQL and persistent PGlite cases use the public Go configuration builder, open functions and runner, two local JSON-RPC endpoints, and business tables keyed by chain and height.

- With setup callbacks enabled, chain 1 is offline on first opening. Chain 2 reaches block 2 and releases chain 1’s RPC. The test holds chain 1’s first baseline fetch until chain 2 commits block 3. At that point chain 1 still has no opening head, cursor, setup row or callback. Readiness stays false.
- With block callbacks only, both chains start offline. Chain 2 returns and indexes on its own. While chain 1 is waiting for its baseline, chain 2 forks at block 2. Chain 2 rolls back and replays that block, then commits block 3 before the baseline read is released.
- Chain 1 processes its inclusive start at block 0. A fork on chain 2 replaces block 3 and extends to block 4. Both cases check the full callback-height sequence and every committed row against the canonical hashes. The setup case also checks one committed setup row per chain and a single deployment-wide setup notification.
- A fresh writer reopens the same business and sync stores and completes without repeating setup. The partial-setup regression also reopens through an outage after a failed setup, preserving the initialized peer. The finality test exercises a lagging endpoint and a replacement of a previously acquired opening block. [Integration and finality tests](../../../historical/isolated_live_outage_test.go)

## Verification

With the task-owned PostgreSQL 14.24 database, PGlite 0.2.13 bridge and Go 1.25.11, the focused checks ran with these selectors.

```sh
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 -run '^(TestRuntimeIsolatedDeferredBootstrap|TestRuntimeIsolatedOfflineReopen|TestRuntimeIsolatedPartialSetupReopen|TestRuntimeIsolatedLiveOutage|TestIsolatedReturningFinality)$' ./historical
  go test $mode -p 1 -count=1 -run 'TestManagedLive(ReorgUnit|FinalizeUnit|RecoveryPublicProjection)' ./store
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The PostgreSQL control ran the two new scenarios against parent commit `6db5699b` with a test-only overlay. Both failed to open because an unavailable chain lacked a committed initial cursor. On the pinned upstream checkout, the four selected Isolated commit and realtime reorg cases passed. The name filter excluded 37 unrelated cases.

All 102 selected Go leaf cases passed in ordinary and race modes. The four fresh-start cases covered both databases. Both backends also passed the scenario with a stalled baseline read, a peer fork before enrollment and another fork after enrollment. Some early concurrent PGlite processes reached the executor memory limit. The affected checks passed when rerun serially. All 51 packages compiled and Go vet completed without findings.

The verification tooling passed 503 Node tests and 43 Python tests. The upstream creation menu still maps to 16 Go templates. The source ledger remains at 883 executed Go ports with 866 passes and 17 provider-specific failures. Full-module ordinary and race runs are tracked separately.
